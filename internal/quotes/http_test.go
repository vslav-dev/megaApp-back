package quotes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"megaapp-back/internal/platform/sqlite"

	"github.com/go-chi/chi/v5"
)

func TestDebugRouteRunsQuotesJob(t *testing.T) {
	db := openQuotesTestDB(t)
	insertQuoteCurrency(t, db, "RUB")

	service := NewService(NewRepository(db, sqlite.WriteDB{DB: db}), Config{
		FetchDays:      7,
		RetryAttempts:  3,
		RetryDelay:     time.Millisecond,
		RequestTimeout: time.Second,
	}, fixedClock{now: time.Date(2026, time.July, 10, 12, 0, 0, 0, time.UTC)}, NewMarketFetcher(time.Second))
	service.currencyBatch = func([]string) BatchFetcher {
		return fakeBatchFetcher{data: map[string]map[string]float64{
			"2026-07-04": {"RUB": 0.012},
		}}
	}
	service.cryptoBatch = func([]OpenAsset) BatchFetcher { return fakeBatchFetcher{} }
	service.stockBatchWithRates = func([]OpenAsset, map[string]float64) BatchFetcher { return fakeBatchFetcher{} }
	service.bondBatchWithRates = func([]OpenAsset, map[string]float64) BatchFetcher { return fakeBatchFetcher{} }

	router := chi.NewRouter()
	RegisterDebugRoutes(router, NewDebugHandler(service))
	server := httptest.NewServer(router)
	defer server.Close()

	response, err := http.Get(server.URL + "/api/debug/run-quotes-job")
	if err != nil {
		t.Fatalf("http.Get() error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if payload["result"] != true {
		t.Fatalf("result = %v, want true", payload["result"])
	}
	if payload["upsertedCount"].(float64) != 1 {
		t.Fatalf("upsertedCount = %v, want 1", payload["upsertedCount"])
	}
}
