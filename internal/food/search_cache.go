package food

import "sync"

// searchScoresCache holds the base scores per query for one search index snapshot. It lives and dies
// with the snapshot, so a catalogue change needs no separate cache clearing. The cached slices are
// never modified after Set, so Get hands them out without a copy.
type searchScoresCache struct {
	mu     sync.RWMutex
	values map[string]baseScores
}

func newSearchScoresCache() *searchScoresCache {
	return &searchScoresCache{values: make(map[string]baseScores)}
}

func (c *searchScoresCache) Get(query string) (baseScores, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	value, ok := c.values[query]
	return value, ok
}

func (c *searchScoresCache) Set(query string, scores baseScores) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.values[query] = scores
}
