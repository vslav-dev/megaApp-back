package food

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"megaapp-back/internal/auth"
	clockplatform "megaapp-back/internal/platform/clock"
	"megaapp-back/internal/platform/idempotency"
	"megaapp-back/internal/platform/sqlite"
	wspkg "megaapp-back/internal/ws"

	"github.com/go-chi/chi/v5"
)

// steppedClock is a clock the test moves by hand.
type steppedClock struct {
	now time.Time
}

func (c *steppedClock) Now() time.Time {
	return c.now
}

func (c *steppedClock) advance(d time.Duration) {
	c.now = c.now.Add(d)
}

var _ clockplatform.Clock = (*steppedClock)(nil)

type candyCatalogue struct {
	service    *Service
	db         *sql.DB
	clock      *steppedClock
	operations int
	souffle    int64
	choc       int64
	lollipop   int64
	beer       int64
}

// newCandyCatalogue builds the catalogue of the "конфеты" case: by the vector the lollipops come third
// behind two real candies, which is the order the personal history has to change.
func newCandyCatalogue(t *testing.T) *candyCatalogue {
	t.Helper()

	generator := &countingEmbeddingGenerator{vectors: map[string][]float64{
		"Конфеты суфле":      {1, 0, 0},
		"Конфеты шоколадные": {0.95, 0.31, 0},
		"Леденцы":            {0.6, 0.8, 0},
		"Пиво":               {0, 0, 1},
		"конфеты":            {1, 0, 0},
		"конфет":             {1, 0, 0},
		"пиво":               {0, 0, 1},
	}}
	service, db := newSearchTestService(t, generator)
	clk := &steppedClock{now: time.Date(2026, time.October, 30, 12, 0, 0, 0, time.UTC)}
	service.SetClock(clk)
	return &candyCatalogue{
		service:  service,
		db:       db,
		clock:    clk,
		souffle:  saveSearchProduct(t, service, "op-souffle", "Конфеты суфле"),
		choc:     saveSearchProduct(t, service, "op-choc", "Конфеты шоколадные"),
		lollipop: saveSearchProduct(t, service, "op-lollipop", "Леденцы"),
		beer:     saveSearchProduct(t, service, "op-beer", "Пиво"),
	}
}

// eat adds a diary entry found by the query, like the app does when the user saves a searched product.
func (c *candyCatalogue) eat(t *testing.T, userID int64, productID int64, query string) {
	t.Helper()

	c.operations++
	operationID := fmt.Sprintf("op-eat-%d", c.operations)
	_, applied, err := c.service.CreateDiaryEntry(context.Background(), userID, operationID, "2026-10-30", productID, 100, []HistoryEntry{{Action: "init", Value: 100}}, query)
	if err != nil || !applied {
		t.Fatalf("CreateDiaryEntry(%q) = (applied=%v, err=%v), want success", query, applied, err)
	}
}

func (c *candyCatalogue) search(t *testing.T, userID int64, query string) []string {
	t.Helper()

	entries, err := c.service.SearchCatalogue(context.Background(), userID, query)
	if err != nil {
		t.Fatalf("SearchCatalogue(%q) error = %v", query, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func (c *candyCatalogue) pickCount(t *testing.T) int {
	t.Helper()

	var count int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM foodSearchPicks`).Scan(&count); err != nil {
		t.Fatalf("count picks error = %v", err)
	}
	return count
}

var candyByVector = []string{"Конфеты суфле", "Конфеты шоколадные", "Леденцы"}

func TestSearchPutsTheChosenProductFirstForTheSameQuery(t *testing.T) {
	c := newCandyCatalogue(t)
	if got := c.search(t, 1, "конфеты"); !slices.Equal(got, candyByVector) {
		t.Fatalf("search before any choice = %v, want the vector order %v", got, candyByVector)
	}

	c.eat(t, 1, c.lollipop, "конфеты")

	want := []string{"Леденцы", "Конфеты суфле", "Конфеты шоколадные"}
	if got := c.search(t, 1, "конфеты"); !slices.Equal(got, want) {
		t.Fatalf("search after one choice = %v, want %v", got, want)
	}
}

func TestSearchPersonalHistoryBelongsToTheUser(t *testing.T) {
	c := newCandyCatalogue(t)

	c.eat(t, 1, c.lollipop, "конфеты")

	if got := c.search(t, 2, "конфеты"); !slices.Equal(got, candyByVector) {
		t.Fatalf("another user's search = %v, want the untouched vector order %v", got, candyByVector)
	}
}

func TestSearchPersonalHistoryWorksInBothDirectionsOfTyping(t *testing.T) {
	t.Run("a choice on the full word helps while it is typed", func(t *testing.T) {
		c := newCandyCatalogue(t)
		c.eat(t, 1, c.lollipop, "конфеты")

		got := c.search(t, 1, "конф")

		if len(got) == 0 || got[0] != "Леденцы" {
			t.Fatalf("search(конф) = %v, want the lollipops first although no name word starts with the query", got)
		}
	})

	t.Run("a choice on a shorter text helps the longer one", func(t *testing.T) {
		c := newCandyCatalogue(t)
		c.eat(t, 1, c.lollipop, "конфет")

		want := []string{"Леденцы", "Конфеты суфле", "Конфеты шоколадные"}
		if got := c.search(t, 1, "конфеты"); !slices.Equal(got, want) {
			t.Fatalf("search(конфеты) = %v, want %v", got, want)
		}
	})

	t.Run("the choice made on the new text is an exact one next time", func(t *testing.T) {
		c := newCandyCatalogue(t)
		c.eat(t, 1, c.lollipop, "конфет")
		c.eat(t, 1, c.lollipop, "конфеты")

		var queries []string
		rows, err := c.db.Query(`SELECT query FROM foodSearchPicks ORDER BY id`)
		if err != nil {
			t.Fatalf("Query() error = %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var query string
			if err := rows.Scan(&query); err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
			queries = append(queries, query)
		}
		if !slices.Equal(queries, []string{"конфет", "конфеты"}) {
			t.Fatalf("stored queries = %v, want one row per choice", queries)
		}
	})
}

func TestSearchPersonalHistoryDoesNotLeakIntoOtherQueries(t *testing.T) {
	c := newCandyCatalogue(t)
	c.eat(t, 1, c.lollipop, "конфеты")

	if got := c.search(t, 1, "пиво"); !slices.Equal(got, []string{"Пиво"}) {
		t.Fatalf("search(пиво) = %v, want only the beer: the lollipops were chosen for another query", got)
	}
}

func TestSearchDoesNotWriteChoices(t *testing.T) {
	c := newCandyCatalogue(t)
	c.eat(t, 1, c.lollipop, "конфеты")
	before := c.pickCount(t)

	c.search(t, 1, "конфеты")

	if after := c.pickCount(t); after != before {
		t.Fatalf("picks after a search = %d, want %d: a search must not write", after, before)
	}
}

func TestCreateDiaryEntryRecordsTheChoiceOnce(t *testing.T) {
	c := newCandyCatalogue(t)
	history := []HistoryEntry{{Action: "init", Value: 100}}

	for range 2 {
		if _, _, err := c.service.CreateDiaryEntry(context.Background(), 1, "op-same", "2026-10-30", c.lollipop, 100, history, "Конфеты"); err != nil {
			t.Fatalf("CreateDiaryEntry() error = %v", err)
		}
	}

	if got := c.pickCount(t); got != 1 {
		t.Fatalf("picks after a create and its replay = %d, want 1", got)
	}
	var query string
	var userID, productID int64
	if err := c.db.QueryRow(`SELECT query, usersId, foodCatalogueId FROM foodSearchPicks`).Scan(&query, &userID, &productID); err != nil {
		t.Fatalf("QueryRow() error = %v", err)
	}
	if query != "конфеты" || userID != 1 || productID != c.lollipop {
		t.Fatalf("pick = (%q, user %d, product %d), want (конфеты, 1, %d)", query, userID, productID, c.lollipop)
	}
}

func TestCreateDiaryEntryIgnoresAnUnusableQuery(t *testing.T) {
	for name, query := range map[string]string{
		"missing":    "",
		"blank":      "   ",
		"whitespace": "\t\n",
	} {
		t.Run(name, func(t *testing.T) {
			c := newCandyCatalogue(t)

			c.eat(t, 1, c.lollipop, query)

			if got := c.pickCount(t); got != 0 {
				t.Fatalf("picks = %d, want none for a %s query", got, name)
			}
		})
	}
}

func TestSearchClipsAVeryLongQuery(t *testing.T) {
	c := newCandyCatalogue(t)
	long := "конфеты " + strings.Repeat("ы", 30000) // about 60 KB, the largest WebSocket message

	c.eat(t, 1, c.lollipop, long)
	var stored string
	if err := c.db.QueryRow(`SELECT query FROM foodSearchPicks`).Scan(&stored); err != nil {
		t.Fatalf("QueryRow() error = %v", err)
	}
	if want := clipSearchQuery(foldSearchText(long)); stored != want || len([]rune(stored)) > searchQueryMaxRunes {
		t.Fatalf("stored query has %d characters, want the first %d characters of the text", len([]rune(stored)), searchQueryMaxRunes)
	}

	got := c.search(t, 1, long)

	if len(got) == 0 || got[0] != "Леденцы" {
		t.Fatalf("search of the very long query = %v, want the clipped query found and the lollipops chosen for it first", got)
	}
}

func TestCreateDiaryEntryFailsAsAWholeWhenTheChoiceCannotBeStored(t *testing.T) {
	c := newCandyCatalogue(t)
	if _, err := c.db.Exec(`DROP TABLE foodSearchPicks`); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	_, applied, err := c.service.CreateDiaryEntry(context.Background(), 1, "op-broken", "2026-10-31", c.lollipop, 100, []HistoryEntry{{Action: "init", Value: 100}}, "конфеты")

	if err == nil || applied {
		t.Fatalf("CreateDiaryEntry() = (applied=%v, err=%v), want a failure", applied, err)
	}
	var entries int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM foodDiary WHERE dateISO = '2026-10-31'`).Scan(&entries); err != nil || entries != 0 {
		t.Fatalf("diary entries after the failed create = %d (err %v), want none: the entry and its choice are one transaction", entries, err)
	}
}

func TestCreateDiaryEntryOfAnArchivedProductRecordsNoChoice(t *testing.T) {
	service, db := newArchiveTestService(t)

	_, applied, err := service.CreateDiaryEntry(context.Background(), regularUserID, "op-archived", "2026-06-20", archivedItemID, 100, []HistoryEntry{{Action: "init", Value: 100}}, "apple")

	if err == nil || applied {
		t.Fatalf("CreateDiaryEntry() = (applied=%v, err=%v), want the unavailable product rejected", applied, err)
	}
	var picks int
	if err := db.QueryRow(`SELECT COUNT(*) FROM foodSearchPicks`).Scan(&picks); err != nil || picks != 0 {
		t.Fatalf("picks = %d (err %v), want none", picks, err)
	}
}

func TestSearchPicksExpireAfterTheRetention(t *testing.T) {
	t.Run("an expired choice has no effect even before it is deleted", func(t *testing.T) {
		c := newCandyCatalogue(t)
		expired := c.clock.now.Add(-searchPickRetention - time.Hour).UnixMilli()
		if _, err := c.db.Exec(`INSERT INTO foodSearchPicks(usersId, query, foodCatalogueId, pickedAt) VALUES (1, 'конфеты', ?, ?)`, c.lollipop, expired); err != nil {
			t.Fatalf("Exec() error = %v", err)
		}

		if got := c.search(t, 1, "конфеты"); !slices.Equal(got, candyByVector) {
			t.Fatalf("search = %v, want the vector order %v", got, candyByVector)
		}
	})

	t.Run("a new choice deletes the expired ones", func(t *testing.T) {
		c := newCandyCatalogue(t)
		c.eat(t, 1, c.lollipop, "конфеты")
		c.eat(t, 2, c.beer, "пиво")
		c.clock.advance(searchPickRetention + 24*time.Hour)

		c.eat(t, 1, c.souffle, "суфле")

		var queries []string
		rows, err := c.db.Query(`SELECT query FROM foodSearchPicks`)
		if err != nil {
			t.Fatalf("Query() error = %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var query string
			if err := rows.Scan(&query); err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
			queries = append(queries, query)
		}
		if !slices.Equal(queries, []string{"суфле"}) {
			t.Fatalf("stored queries after the retention = %v, want only the new one (of every user)", queries)
		}
	})

	t.Run("a choice just inside the retention still counts", func(t *testing.T) {
		c := newCandyCatalogue(t)
		inside := c.clock.now.Add(-searchPickRetention + time.Hour).UnixMilli()
		if _, err := c.db.Exec(`INSERT INTO foodSearchPicks(usersId, query, foodCatalogueId, pickedAt) VALUES (1, 'конфеты', ?, ?)`, c.lollipop, inside); err != nil {
			t.Fatalf("Exec() error = %v", err)
		}

		rows, err := c.service.repo.GetRelatedSearchPicks(context.Background(), 1, "конфеты", c.clock.now.Add(-searchPickRetention).UnixMilli())
		if err != nil || len(rows) != 1 {
			t.Fatalf("GetRelatedSearchPicks() = %v (err %v), want the one choice", rows, err)
		}
	})
}

func TestGetRelatedSearchPicks(t *testing.T) {
	c := newCandyCatalogue(t)
	const now = 1_000_000
	inserts := []struct {
		userID   int64
		query    string
		pickedAt int64
	}{
		{1, "конфет", now},
		{1, "конфеты", now},
		{1, "конфетки в шоколаде", now},
		{1, "конф", now},
		{1, "ко", now},
		{1, "к", now},
		{1, "конина", now},
		{1, "кон ф", now},
		{1, "конфеты", now - 500},
		{2, "конфеты", now},
		{1, "ёж", now},
	}
	for _, insert := range inserts {
		if _, err := c.db.Exec(`INSERT INTO foodSearchPicks(usersId, query, foodCatalogueId, pickedAt) VALUES (?, ?, 1, ?)`, insert.userID, insert.query, insert.pickedAt); err != nil {
			t.Fatalf("Exec() error = %v", err)
		}
	}

	tests := []struct {
		name  string
		query string
		since int64
		want  []string
	}{
		{
			name:  "queries that start with it and queries it starts with",
			query: "конфет",
			since: 0,
			want:  []string{"к", "ко", "конф", "конфет", "конфеты", "конфеты", "конфетки в шоколаде"},
		},
		{
			name:  "a one-letter query has no prefixes of its own",
			query: "к",
			since: 0,
			want:  []string{"к", "ко", "кон ф", "конина", "конф", "конфет", "конфеты", "конфеты", "конфетки в шоколаде"},
		},
		{
			name:  "choices older than the cutoff are left out",
			query: "конфеты",
			since: now - 100,
			want:  []string{"к", "ко", "конф", "конфет", "конфеты"},
		},
		{
			name:  "a query nothing is related to",
			query: "пиво",
			since: 0,
			want:  []string{},
		},
		{
			name:  "ё is part of the stored key as it is",
			query: "ёж",
			since: 0,
			want:  []string{"ёж"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, err := c.service.repo.GetRelatedSearchPicks(context.Background(), 1, tt.query, tt.since)
			if err != nil {
				t.Fatalf("GetRelatedSearchPicks() error = %v", err)
			}
			got := make([]string, 0, len(rows))
			for _, row := range rows {
				got = append(got, row.Query)
			}
			slices.Sort(got)
			slices.Sort(tt.want)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("GetRelatedSearchPicks(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestCreateDiaryEntryOverHTTPRecordsTheSearchQueryButRestoreDoesNot(t *testing.T) {
	db := openFoodTestDB(t)
	authService := auth.NewService(auth.NewRepository(db, sqlite.WriteDB{DB: db}), auth.SessionConfig{})
	service := NewService(NewRepository(db, sqlite.WriteDB{DB: db}), idempotency.NewStore(sqlite.WriteDB{DB: db}))
	hub := wspkg.NewHub(time.Second, wspkg.NewSyncState())
	defer func() { _ = hub.Close() }()
	realtime := NewWSRealtimePublisher(hub, clockplatform.NewRealClock())
	router := chi.NewRouter()
	RegisterWriteRoutes(router, authService, NewWriteHandler(service, realtime, &fakeMetricsRecorder{}))
	server := httptest.NewServer(router)
	defer server.Close()
	session, err := authService.CreateSession(t.Context(), 1)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	history := []map[string]any{{"action": "init", "value": 120}}

	assertJSONRequestStatus(t, http.MethodPost, server.URL+"/api/food/diary/", session.Cookie, "tab-a", map[string]any{
		"operationId": "op-create", "dateISO": "2026-06-18", "foodCatalogueId": 1, "foodWeight": 120, "history": history,
		"searchQuery": "Apple",
	}, http.StatusCreated)
	assertJSONRequestStatus(t, http.MethodPost, server.URL+"/api/food/diary/", session.Cookie, "tab-a", map[string]any{
		"operationId": "op-create-without", "dateISO": "2026-06-18", "foodCatalogueId": 2, "foodWeight": 120, "history": history,
	}, http.StatusCreated)
	assertJSONRequestStatus(t, http.MethodPost, server.URL+"/api/food/diary/day/2026-06-19/restore", session.Cookie, "tab-a", map[string]any{
		"operationId": "op-restore",
		"entries": []map[string]any{{
			"foodCatalogueId": 1, "foodWeight": 120, "history": history, "searchQuery": "apple",
		}},
	}, http.StatusCreated)

	var queries []string
	rows, err := db.Query(`SELECT query FROM foodSearchPicks`)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var query string
		if err := rows.Scan(&query); err != nil {
			t.Fatalf("Scan() error = %v", err)
		}
		queries = append(queries, query)
	}
	if !slices.Equal(queries, []string{"apple"}) {
		t.Fatalf("stored queries = %v, want only the one from the create that sent it", queries)
	}
}
