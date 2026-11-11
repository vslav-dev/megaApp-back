package food

import "testing"

// Regression test: cloneStatsResponse must round-trip every StatsResponse field through the
// cache. It previously omitted Summary, so every cache hit (i.e. every GetStats call after the
// first) silently returned an empty Summary — "Вехи" showed as empty in production despite the
// backend computing it correctly.
func TestStatsCacheRoundTripsSummary(t *testing.T) {
	cache := NewStatsCache()
	weight := &WeightRecord{Weight: 67.2, DateISO: "2026-08-11"}
	response := StatsResponse{
		Days: map[string]DayStats{"2026-08-11": {Weight: 67.2}},
		Summary: StatsSummary{
			DaysInDiary: 2135,
			MinWeight:   weight,
			YearAgo:     &YearAgoRecord{DateISO: "2025-08-18", WeightThen: 76, WeightNow: 69, DeltaKg: -7},
		},
		TotalEntries: 14205,
	}

	cache.Set(1, response)
	cached, ok := cache.Get(1)
	if !ok {
		t.Fatal("expected cache hit")
	}

	if cached.Summary.DaysInDiary != 2135 {
		t.Errorf("Summary.DaysInDiary = %d, want 2135", cached.Summary.DaysInDiary)
	}
	if cached.Summary.MinWeight == nil || cached.Summary.MinWeight.Weight != 67.2 {
		t.Errorf("Summary.MinWeight = %+v, want Weight 67.2", cached.Summary.MinWeight)
	}
	if cached.Summary.YearAgo == nil || cached.Summary.YearAgo.DeltaKg != -7 {
		t.Errorf("Summary.YearAgo = %+v, want DeltaKg -7", cached.Summary.YearAgo)
	}
}
