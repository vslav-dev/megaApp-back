package food

import "sync"

type StatsCache struct {
	mu     sync.RWMutex
	values map[int64]StatsResponse
}

func NewStatsCache() *StatsCache {
	return &StatsCache{values: make(map[int64]StatsResponse)}
}

func (c *StatsCache) Get(userID int64) (StatsResponse, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	value, ok := c.values[userID]
	if !ok {
		return StatsResponse{}, false
	}

	return cloneStatsResponse(value), true
}

func (c *StatsCache) Set(userID int64, value StatsResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.values[userID] = cloneStatsResponse(value)
}

func (c *StatsCache) Delete(userID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.values, userID)
}

func cloneStatsResponse(input StatsResponse) StatsResponse {
	days := make(map[string]DayStats, len(input.Days))
	for key, value := range input.Days {
		days[key] = value
	}
	topProductsByKcal := make([]ProductStat, len(input.TopProductsByKcal))
	copy(topProductsByKcal, input.TopProductsByKcal)
	topProductsByWeight := make([]ProductStat, len(input.TopProductsByWeight))
	copy(topProductsByWeight, input.TopProductsByWeight)

	return StatsResponse{
		Days:                         days,
		Summary:                      input.Summary,
		TopProductsByKcal:            topProductsByKcal,
		TopProductsByWeight:          topProductsByWeight,
		TopProductsWindowTotalKcal:   input.TopProductsWindowTotalKcal,
		TopProductsWindowTotalWeight: input.TopProductsWindowTotalWeight,
		TotalEntries:                 input.TotalEntries,
	}
}
