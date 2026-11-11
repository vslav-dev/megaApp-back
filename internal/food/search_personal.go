package food

import (
	"context"
	"database/sql"
	"math"
	"time"
	"unicode/utf8"
)

const (
	// searchPickHalfLife is how fast a choice fades: its weight halves every 30 days. With a steady
	// habit a new product catches up with an abandoned one exactly one half-life after the switch.
	searchPickHalfLife = 30 * 24 * time.Hour

	// searchPickRetention is how long choices are kept: 12 half-lives, when a choice weighs 0.02% and
	// dropping it changes no ranking.
	searchPickRetention = 12 * searchPickHalfLife

	// searchPickBoost scales a product's share of the choices into the score bonus. The score it is added
	// to is the vector similarity plus searchWordLift per matched word, so a habitual product (share
	// 0.8 and up) outranks the closest vector match. A single fresh choice (share 1, confidence 0.5)
	// gives 1.25, more than the largest gap between the first and the last of the 30 shown over the
	// popular real queries (1.035, measured by TestSearchPersonalBonusOverridesTheVectorOnProductionCopy).
	searchPickBoost = 2.5

	// searchPickEvidence is the total weight at which the choices count for half of their share:
	// one fresh choice weighs 1, so it is trusted at 1 / (1 + 1), ten at 10 / (10 + 1).
	searchPickEvidence = 1.0
)

// searchPickKey is the form a query is stored and looked up in: lowercase, ё as е, single spaces, cut to
// searchQueryMaxRunes like the query the search ran on. An empty query is not recorded.
func searchPickKey(query string) (string, bool) {
	key := clipSearchQuery(foldSearchText(query))
	return key, key != ""
}

// recordSearchPick stores the choice inside the transaction of the diary entry it belongs to, and drops
// the choices past the retention. A missing or unusable query records nothing: it is a hint for the
// search, not part of the entry.
func (s *Service) recordSearchPick(ctx context.Context, tx *sql.Tx, userID int64, query string, catalogueID int64) error {
	key, ok := searchPickKey(query)
	if !ok {
		return nil
	}
	now := s.clock.Now()
	if err := s.repo.InsertSearchPick(ctx, tx, userID, key, catalogueID, now.UnixMilli()); err != nil {
		return err
	}
	return s.repo.DeleteSearchPicksBefore(ctx, tx, now.Add(-searchPickRetention).UnixMilli())
}

// personalBonuses reads the user's choices related to the query and turns them into a score bonus per
// product; nil when there is no history.
func (s *Service) personalBonuses(ctx context.Context, userID int64, query string) (map[int64]float64, error) {
	key := foldSearchText(query)
	now := s.clock.Now()
	picks, err := s.repo.GetRelatedSearchPicks(ctx, userID, key, now.Add(-searchPickRetention).UnixMilli())
	if err != nil {
		return nil, err
	}
	return searchPickBonuses(picks, key, now), nil
}

// searchPickBonuses is the personal part of the ranking. Each choice weighs
//
//	closeness × 0.5^(age / half-life)
//
// where closeness is the length of the shorter of the two queries over the length of the longer (1 for
// the same query, 0.86 for "конфет" and "конфеты"), so a choice made on a longer or shorter text still
// counts, the less the further the texts are apart. A product's bonus is
//
//	boost × its share of the total weight × total / (total + evidence)
//
// The share keeps the order of habits (80% of the choices beat 17%); the confidence lets a single old
// choice fade out and a few fresh ones count at once.
func searchPickBonuses(picks []SearchPickRow, query string, now time.Time) map[int64]float64 {
	queryRunes := utf8.RuneCountInString(query)
	weights := make(map[int64]float64)
	total := 0.0
	for _, pick := range picks {
		pickRunes := utf8.RuneCountInString(pick.Query)
		closeness := float64(min(queryRunes, pickRunes)) / float64(max(queryRunes, pickRunes))
		age := max(now.Sub(time.UnixMilli(pick.PickedAt)), 0)
		weight := closeness * math.Exp2(-float64(age)/float64(searchPickHalfLife))
		weights[pick.CatalogueID] += weight
		total += weight
	}
	if total <= 0 {
		return nil
	}

	confidence := total / (total + searchPickEvidence)
	bonuses := make(map[int64]float64, len(weights))
	for catalogueID, weight := range weights {
		bonuses[catalogueID] = searchPickBoost * weight / total * confidence
	}
	return bonuses
}
