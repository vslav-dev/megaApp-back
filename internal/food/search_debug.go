package food

import (
	"context"
	"log/slog"
	"unicode/utf8"
)

// TODO(plan 48 debug): temporary logs of the search personalization. Delete this file, its test and the calls
// marked "TODO(plan 48 debug)" once the feature is accepted.

// logDiaryCreateDebug shows, per diary create, whether the client sent the search query: a create with no
// query comes from a client that predates the feature (or from a pick with no results on screen).
func logDiaryCreateDebug(ctx context.Context, userID int64, clientID string, searchQuery string, applied bool) {
	slog.InfoContext(ctx, "🍽️ diary create", "user", userID, "client", shortID(clientID), "query", searchQuery, "queryRunes", utf8.RuneCountInString(searchQuery), "applied", applied)
}

// logSearchPersonalDebug shows, per search that has personal bonuses, which product got the biggest one and
// where it landed in the results (0 = not in them).
func logSearchPersonalDebug(ctx context.Context, userID int64, query string, bonuses map[int64]float64, ids []int64) {
	if len(bonuses) == 0 {
		return
	}
	topID, topBonus, topRank := topBonus(bonuses, ids)
	slog.InfoContext(ctx, "🎯 search personal", "user", userID, "query", query, "bonuses", len(bonuses), "top", topID, "topBonus", topBonus, "topRank", topRank)
}

// topBonus returns the product with the biggest bonus (the lower id on a tie), the bonus and the product's
// 1-based place in ids, 0 when it is not there.
func topBonus(bonuses map[int64]float64, ids []int64) (id int64, bonus float64, rank int) {
	for candidate, value := range bonuses {
		if value > bonus || (value == bonus && candidate < id) {
			id, bonus = candidate, value
		}
	}
	for i, shown := range ids {
		if shown == id {
			return id, bonus, i + 1
		}
	}
	return id, bonus, 0
}

func shortID(id string) string {
	runes := []rune(id)
	if len(runes) > 6 {
		return string(runes[:6])
	}
	return id
}
