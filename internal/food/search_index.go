package food

import (
	"cmp"
	"context"
	"encoding/binary"
	"math"
	"slices"
	"strings"
	"sync"
	"unicode"
)

const searchResultLimit = 30

// searchWordLift is added to a product's score for every query word that is the start of a word in the
// product's name. It sits on top of the vector similarity, which is the base of the ranking, so a name
// that holds more of the query's words ranks higher. Calibrated on hand-labelled popular queries over
// the real catalogue: the result does not change anywhere between 0.15 and 0.6, 0.25 is the middle of
// that range, and a close product with no match can still outrank a distant one with it.
const searchWordLift = 0.25

// SearchScope is the part of the catalogue a search ranks: the products in use, only the archived
// ones, or both. The values are the wire names of the WebSocket search.
type SearchScope string

const (
	SearchScopeActive   SearchScope = "active"
	SearchScopeArchived SearchScope = "archived"
	SearchScopeAll      SearchScope = "all"
)

// includes reports whether a product with this archive flag belongs to the scope.
func (s SearchScope) includes(archived bool) bool {
	switch s {
	case SearchScopeArchived:
		return archived
	case SearchScopeAll:
		return true
	default:
		return !archived
	}
}

// searchIndexEntry is one product prepared for search: the words of its name and its two embeddings
// scaled to unit length, so cosine similarity is a plain dot product.
type searchIndexEntry struct {
	id       int64
	name     string
	archived bool
	words    []string
	nameVec  []float32
	descVec  []float32
}

// searchIndex is an immutable snapshot of the whole catalogue (~450 products) plus the cache of the
// base scores of its queries. Any catalogue change drops the snapshot; the next search builds a new one.
type searchIndex struct {
	entries []searchIndexEntry
	scores  *searchScoresCache
}

// baseScore is what the catalogue alone says about a product for a query, the same for every user: the
// vector similarity plus the lift for the matched words, and whether that makes the product a candidate.
type baseScore struct {
	value     float64
	candidate bool
}

// baseScores holds a baseScore per product of the snapshot, in the order of its entries.
type baseScores []baseScore

func newSearchIndex(rows []SearchIndexRow) *searchIndex {
	entries := make([]searchIndexEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, searchIndexEntry{
			id:       row.ID,
			name:     row.Name,
			archived: row.Archived,
			words:    searchWords(row.Name),
			nameVec:  decodeUnitVector(row.NameVec),
			descVec:  decodeUnitVector(row.DescriptionVec),
		})
	}
	return &searchIndex{entries: entries, scores: newSearchScoresCache()}
}

// score is the first stage of a search, independent of the user and of the scope. A product is a
// candidate when a query word starts one of its name words or its similarity to the query vector is
// positive; without a query vector only the first kind remains. Score = similarity + wordLift × the
// number of query words that start a name word (searchWordLift in production; a parameter so the
// regression can compare other sizes).
func (idx *searchIndex) score(queryWords []string, queryVec []float32, wordLift float64) baseScores {
	stems := make([]string, 0, len(queryWords))
	for _, word := range queryWords {
		stems = append(stems, stemSearchWord(word))
	}

	scores := make(baseScores, len(idx.entries))
	for i := range idx.entries {
		entry := &idx.entries[i]
		similarity := entry.similarity(queryVec)
		matched := entry.matchedWords(stems)
		scores[i] = baseScore{
			value:     similarity + wordLift*float64(matched),
			candidate: matched > 0 || similarity > 0,
		}
	}
	return scores
}

// rank is the second stage: the products of one scope, the base score plus the user's bonus, best 30.
// A product with a bonus is a candidate even when the catalogue alone would not show it, because the
// user chose it for this query before.
func (idx *searchIndex) rank(scores baseScores, scope SearchScope, bonuses map[int64]float64) []int64 {
	type scoredEntry struct {
		entry *searchIndexEntry
		score float64
	}
	scored := make([]scoredEntry, 0, len(idx.entries))
	for i := range idx.entries {
		entry := &idx.entries[i]
		if !scope.includes(entry.archived) {
			continue
		}
		bonus := bonuses[entry.id]
		if !scores[i].candidate && bonus <= 0 {
			continue
		}
		scored = append(scored, scoredEntry{entry: entry, score: scores[i].value + bonus})
	}

	slices.SortFunc(scored, func(a, b scoredEntry) int {
		return cmp.Or(cmp.Compare(b.score, a.score), strings.Compare(a.entry.name, b.entry.name), cmp.Compare(a.entry.id, b.entry.id))
	})

	if len(scored) > searchResultLimit {
		scored = scored[:searchResultLimit]
	}
	ids := make([]int64, 0, len(scored))
	for _, item := range scored {
		ids = append(ids, item.entry.id)
	}
	return ids
}

// similarity is the mean of the name and description cosines (just the one that exists when the other
// is missing). Vectors of another size than the query's (a different embedding model) are skipped.
func (e *searchIndexEntry) similarity(queryVec []float32) float64 {
	if len(queryVec) == 0 {
		return 0
	}
	sum := 0.0
	count := 0
	for _, vec := range [][]float32{e.nameVec, e.descVec} {
		if len(vec) != len(queryVec) {
			continue
		}
		sum += float64(dotProduct(queryVec, vec))
		count++
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// matchedWords counts the stems that start at least one word of the name.
func (e *searchIndexEntry) matchedWords(stems []string) int {
	matched := 0
	for _, stem := range stems {
		if e.hasWordStartingWith(stem) {
			matched++
		}
	}
	return matched
}

func (e *searchIndexEntry) hasWordStartingWith(stem string) bool {
	for _, word := range e.words {
		if strings.HasPrefix(word, stem) {
			return true
		}
	}
	return false
}

// searchIndexHolder keeps the current snapshot. The build runs under the lock and Invalidate waits for
// it, so a build that read the catalogue before a write can never outlive that write's Invalidate.
type searchIndexHolder struct {
	mu      sync.RWMutex
	current *searchIndex
}

func (h *searchIndexHolder) Get(ctx context.Context, build func(context.Context) (*searchIndex, error)) (*searchIndex, error) {
	h.mu.RLock()
	current := h.current
	h.mu.RUnlock()
	if current != nil {
		return current, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current != nil {
		return h.current, nil
	}
	built, err := build(ctx)
	if err != nil {
		return nil, err
	}
	h.current = built
	return built, nil
}

func (h *searchIndexHolder) Invalidate() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.current = nil
}

// foldSearchText lowercases and collapses whitespace like normalizeSearchText and also treats ё as е.
func foldSearchText(value string) string {
	return strings.ReplaceAll(normalizeSearchText(value), "ё", "е")
}

// searchWords splits text into lowercase words of letters and digits, with ё folded into е.
func searchWords(value string) []string {
	return strings.FieldsFunc(foldSearchText(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// stemSearchWord is a dictionary-free stemmer: it cuts the ending off a query word (two letters from
// six letters long, one letter from four) so a form of the word in the name still starts with what is left.
func stemSearchWord(word string) string {
	runes := []rune(word)
	cut := 0
	switch {
	case len(runes) >= 6:
		cut = 2
	case len(runes) >= 4:
		cut = 1
	}
	return string(runes[:len(runes)-cut])
}

// decodeUnitVector reads a little-endian float32 blob and scales it to unit length; nil for a blob that
// is empty, malformed or all zeros.
func decodeUnitVector(blob []byte) []float32 {
	if len(blob) == 0 || len(blob)%4 != 0 {
		return nil
	}
	vec := make([]float32, len(blob)/4)
	norm := 0.0
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
		norm += float64(vec[i]) * float64(vec[i])
	}
	if norm == 0 {
		return nil
	}
	scale := float32(1 / math.Sqrt(norm))
	for i := range vec {
		vec[i] *= scale
	}
	return vec
}

func dotProduct(a []float32, b []float32) float32 {
	var sum float32
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}
