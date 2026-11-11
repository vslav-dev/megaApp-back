package food

import (
	"math"
	"testing"
	"time"
)

var searchPickTestNow = time.Date(2026, time.October, 30, 12, 0, 0, 0, time.UTC)

func searchPickRow(query string, catalogueID int64, daysAgo float64) SearchPickRow {
	pickedAt := searchPickTestNow.Add(-time.Duration(daysAgo * float64(24*time.Hour)))
	return SearchPickRow{Query: query, CatalogueID: catalogueID, PickedAt: pickedAt.UnixMilli()}
}

func repeatedSearchPicks(count int, query string, catalogueID int64, daysAgo float64) []SearchPickRow {
	picks := make([]SearchPickRow, 0, count)
	for range count {
		picks = append(picks, searchPickRow(query, catalogueID, daysAgo))
	}
	return picks
}

func TestSearchPickKey(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		want   string
		wantOK bool
	}{
		{name: "lowercases, folds ё and collapses spaces", query: "  Хлеб   ЧЁРНЫЙ ", want: "хлеб черный", wantOK: true},
		{name: "empty", query: "", wantOK: false},
		{name: "only spaces", query: "   ", wantOK: false},
		{name: "longest unclipped", query: repeatRune('а', searchQueryMaxRunes), want: repeatRune('а', searchQueryMaxRunes), wantOK: true},
		{name: "longer is clipped", query: repeatRune('а', searchQueryMaxRunes+50), want: repeatRune('а', searchQueryMaxRunes), wantOK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := searchPickKey(tt.query)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("searchPickKey(%q) = (%q, %v), want (%q, %v)", tt.query, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestClipSearchQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{name: "a short query stays", query: "молоко 2,5", want: "молоко 2,5"},
		{name: "exactly the limit stays", query: repeatRune('я', searchQueryMaxRunes), want: repeatRune('я', searchQueryMaxRunes)},
		{name: "cuts at a character, not at a byte", query: repeatRune('я', 5000), want: repeatRune('я', searchQueryMaxRunes)},
		{name: "does not leave a space at the end", query: repeatRune('я', searchQueryMaxRunes-1) + " слово", want: repeatRune('я', searchQueryMaxRunes-1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clipSearchQuery(tt.query); got != tt.want {
				t.Fatalf("clipSearchQuery() = %q, want %q", got, tt.want)
			}
		})
	}
}

func repeatRune(r rune, count int) string {
	runes := make([]rune, count)
	for i := range runes {
		runes[i] = r
	}
	return string(runes)
}

func TestSearchPickBonuses(t *testing.T) {
	tests := []struct {
		name  string
		picks []SearchPickRow
		query string
		want  map[int64]float64
	}{
		{
			name:  "no history gives no bonuses",
			picks: nil,
			query: "конфеты",
			want:  nil,
		},
		{
			name:  "one fresh choice of the same query is trusted by half",
			picks: []SearchPickRow{searchPickRow("конфеты", 1, 0)},
			query: "конфеты",
			want:  map[int64]float64{1: searchPickBoost * 0.5},
		},
		{
			name:  "three fresh choices are trusted by three quarters",
			picks: repeatedSearchPicks(3, "конфеты", 1, 0),
			query: "конфеты",
			want:  map[int64]float64{1: searchPickBoost * 0.75},
		},
		{
			name:  "a choice loses half of its weight in 30 days",
			picks: []SearchPickRow{searchPickRow("конфеты", 1, 30)},
			query: "конфеты",
			want:  map[int64]float64{1: searchPickBoost * 0.5 / 1.5},
		},
		{
			name:  "a single old choice fades out in 90 days",
			picks: []SearchPickRow{searchPickRow("конфеты", 1, 90)},
			query: "конфеты",
			want:  map[int64]float64{1: searchPickBoost * 0.125 / 1.125},
		},
		{
			name:  "a single choice is almost forgotten in half a year",
			picks: []SearchPickRow{searchPickRow("конфеты", 1, 180)},
			query: "конфеты",
			want:  map[int64]float64{1: searchPickBoost * (1.0 / 64) / (1 + 1.0/64)},
		},
		{
			name:  "a choice on a shorter text counts by the share of the longer one",
			picks: []SearchPickRow{searchPickRow("конфет", 1, 0)},
			query: "конфеты",
			want:  map[int64]float64{1: searchPickBoost * (6.0 / 7) / (1 + 6.0/7)},
		},
		{
			name:  "a choice on a longer text counts by the share typed so far",
			picks: []SearchPickRow{searchPickRow("конфеты", 1, 0)},
			query: "конф",
			want:  map[int64]float64{1: searchPickBoost * (4.0 / 7) / (1 + 4.0/7)},
		},
		{
			name:  "a choice on a far shorter text counts little",
			picks: []SearchPickRow{searchPickRow("ко", 1, 0)},
			query: "конфеты",
			want:  map[int64]float64{1: searchPickBoost * (2.0 / 7) / (1 + 2.0/7)},
		},
		{
			name:  "a choice with a time ahead of the clock counts as fresh",
			picks: []SearchPickRow{searchPickRow("конфеты", 1, -1)},
			query: "конфеты",
			want:  map[int64]float64{1: searchPickBoost * 0.5},
		},
		{
			name: "shares keep the order of habits: 16 of 20, 3 of 20, 1 of 20",
			picks: append(append(
				repeatedSearchPicks(16, "молоко", 1, 0),
				repeatedSearchPicks(3, "молоко", 2, 0)...),
				repeatedSearchPicks(1, "молоко", 3, 0)...),
			query: "молоко",
			want: map[int64]float64{
				1: searchPickBoost * 0.80 * 20.0 / 21,
				2: searchPickBoost * 0.15 * 20.0 / 21,
				3: searchPickBoost * 0.05 * 20.0 / 21,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := searchPickBonuses(tt.picks, tt.query, searchPickTestNow)

			if len(got) != len(tt.want) {
				t.Fatalf("searchPickBonuses() = %v, want %v", got, tt.want)
			}
			for id, want := range tt.want {
				if math.Abs(got[id]-want) > 1e-6 {
					t.Fatalf("bonus of %d = %.6f, want %.6f (all: %v)", id, got[id], want, got)
				}
			}
		})
	}
}

// A habit of 45 choices (every second day) is replaced by another one at the same pace: the new product
// must overtake the old one about one half-life after the switch, and win clearly after two.
func TestSearchPickBonusesFollowAChangeOfHabit(t *testing.T) {
	const day = 24 * time.Hour
	start := searchPickTestNow
	switchAt := start.Add(90 * day)

	bonusesAfter := func(daysSinceSwitch int) map[int64]float64 {
		now := switchAt.Add(time.Duration(daysSinceSwitch) * day)
		var picks []SearchPickRow
		for offset := 0; offset < 90; offset += 2 {
			picks = append(picks, SearchPickRow{Query: "молоко", CatalogueID: 1, PickedAt: start.Add(time.Duration(offset) * day).UnixMilli()})
		}
		for offset := 0; offset <= daysSinceSwitch; offset += 2 {
			picks = append(picks, SearchPickRow{Query: "молоко", CatalogueID: 2, PickedAt: switchAt.Add(time.Duration(offset) * day).UnixMilli()})
		}
		return searchPickBonuses(picks, "молоко", now)
	}

	overtakenAt := -1
	for days := 1; days <= 120; days++ {
		bonuses := bonusesAfter(days)
		if bonuses[2] > bonuses[1] {
			overtakenAt = days
			break
		}
	}
	if overtakenAt < 25 || overtakenAt > 35 {
		t.Fatalf("the new product overtakes the old one %d days after the switch, want 25..35", overtakenAt)
	}

	if bonuses := bonusesAfter(10); bonuses[1] <= bonuses[2] {
		t.Fatalf("10 days after the switch bonuses = %v, want the old habit still first", bonuses)
	}
	if bonuses := bonusesAfter(60); bonuses[2] < 2*bonuses[1] {
		t.Fatalf("60 days after the switch bonuses = %v, want the new habit clearly first", bonuses)
	}
}

func TestSearchPickBonusesStayPositiveAtTheRetentionEdge(t *testing.T) {
	picks := []SearchPickRow{searchPickRow("молоко", 1, float64(searchPickRetention/(24*time.Hour)))}

	got := searchPickBonuses(picks, "молоко", searchPickTestNow)

	if got[1] <= 0 || got[1] > 0.001 {
		t.Fatalf("bonus at the retention edge = %v, want a positive value below 0.001", got[1])
	}
}
