package food

import "testing"

// TODO(plan 48 debug): delete with search_debug.go.
func TestTopBonus(t *testing.T) {
	tests := []struct {
		name      string
		bonuses   map[int64]float64
		ids       []int64
		wantID    int64
		wantBonus float64
		wantRank  int
	}{
		{"biggest bonus and its place", map[int64]float64{7: 0.4, 9: 1.2}, []int64{3, 9, 7}, 9, 1.2, 2},
		{"not in the results", map[int64]float64{9: 1.2}, []int64{3, 7}, 9, 1.2, 0},
		{"tie goes to the lower id", map[int64]float64{9: 1, 4: 1}, []int64{9, 4}, 4, 1, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, bonus, rank := topBonus(tt.bonuses, tt.ids)
			if id != tt.wantID || bonus != tt.wantBonus || rank != tt.wantRank {
				t.Fatalf("topBonus = (%d, %v, %d), want (%d, %v, %d)", id, bonus, rank, tt.wantID, tt.wantBonus, tt.wantRank)
			}
		})
	}
}

func TestShortID(t *testing.T) {
	tests := []struct{ in, want string }{
		{"25a5af14-0c72-44b4", "25a5af"},
		{"abc", "abc"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := shortID(tt.in); got != tt.want {
			t.Fatalf("shortID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
