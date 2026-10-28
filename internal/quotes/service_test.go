package quotes

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"testing"
	"time"

	platformclock "megaapp-back/internal/platform/clock"
	"megaapp-back/internal/platform/sqlite"

	_ "modernc.org/sqlite"
)

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

type fakeBatchFetcher struct {
	data    map[string]map[string]float64
	outcome BatchOutcome
	err     error
}

func (f fakeBatchFetcher) Fetch(context.Context, string, string, RetryConfig) (map[string]map[string]float64, BatchOutcome, error) {
	outcome := BatchOutcome{
		Failures: append([]TickerFailure(nil), f.outcome.Failures...),
		Degraded: append([]TickerFailure(nil), f.outcome.Degraded...),
	}
	return cloneBatchRates(f.data), outcome, f.err
}

func TestServiceRunDiscoversTickersAndUpsertsRates(t *testing.T) {
	db := openQuotesTestDB(t)
	insertQuoteCurrency(t, db, "USD")
	insertQuoteCurrency(t, db, "RUB")
	insertQuoteCurrency(t, db, "EUR")
	insertQuoteCurrency(t, db, "RUB")
	insertQuoteAsset(t, db, 1, "BTC", "crypto")
	insertQuoteAsset(t, db, 2, "AAPL", "stock")
	insertQuoteAsset(t, db, 3, "OFZ", "bond")
	insertQuoteAsset(t, db, 4, "TSLA", "stock")
	insertQuoteTrade(t, db, 1, "invest_buy", `{"assetId":1,"quantity":1}`)
	insertQuoteTrade(t, db, 2, "invest_buy", `{"assetId":2,"quantity":2}`)
	insertQuoteTrade(t, db, 3, "invest_buy", `{"assetId":3,"quantity":4}`)
	insertQuoteTrade(t, db, 4, "invest_buy", `{"assetId":4,"quantity":1}`)
	insertQuoteTrade(t, db, 5, "invest_sell", `{"assetId":4,"quantity":1}`)
	insertQuoteRateHistory(t, db, "2026-07-03", `{"USD":1}`)

	service := NewService(NewRepository(db, sqlite.WriteDB{DB: db}), Config{
		FetchDays:      7,
		RetryAttempts:  3,
		RetryDelay:     time.Millisecond,
		RequestTimeout: time.Second,
	}, fixedClock{now: time.Date(2026, time.July, 10, 12, 0, 0, 0, time.UTC)}, NewMarketFetcher(time.Second))

	var currencyTickers []string
	service.currencyBatch = func(tickers []string) BatchFetcher {
		currencyTickers = append([]string(nil), tickers...)
		return fakeBatchFetcher{data: map[string]map[string]float64{
			"2026-07-04": {"RUB": 0.012, "EUR": 0.86},
		}}
	}

	var cryptoTickers []string
	service.cryptoBatch = func(assets []OpenAsset) BatchFetcher {
		for _, asset := range assets {
			cryptoTickers = append(cryptoTickers, asset.Ticker)
		}
		return fakeBatchFetcher{data: map[string]map[string]float64{
			"2026-07-04": {"BTC": 60000},
		}}
	}

	var stockTickers []string
	var stockRubRates map[string]float64
	service.stockBatchWithRates = func(assets []OpenAsset, rubUSDRates map[string]float64) BatchFetcher {
		for _, asset := range assets {
			stockTickers = append(stockTickers, asset.Ticker)
		}
		stockRubRates = cloneTickerRates(rubUSDRates)
		return fakeBatchFetcher{data: map[string]map[string]float64{
			"2026-07-04": {"AAPL": 220},
		}}
	}

	var bondTickers []string
	var bondRubRates map[string]float64
	service.bondBatchWithRates = func(assets []OpenAsset, rubUSDRates map[string]float64) BatchFetcher {
		for _, asset := range assets {
			bondTickers = append(bondTickers, asset.Ticker)
		}
		bondRubRates = cloneTickerRates(rubUSDRates)
		return fakeBatchFetcher{data: map[string]map[string]float64{
			"2026-07-04": {"OFZ": 110},
		}}
	}

	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.UpsertedCount != 2 || result.FromISO != "2026-07-03" || result.ToISO != "2026-07-09" {
		t.Fatalf("Run() result = %+v", result)
	}
	if len(result.Failures) != 0 {
		t.Fatalf("Run() failures = %v, want empty", result.Failures)
	}

	if !reflect.DeepEqual(currencyTickers, []string{"EUR", "RUB"}) {
		t.Fatalf("currencyTickers = %v, want [EUR RUB]", currencyTickers)
	}
	sort.Strings(cryptoTickers)
	if !reflect.DeepEqual(cryptoTickers, []string{"BTC"}) {
		t.Fatalf("cryptoTickers = %v, want [BTC]", cryptoTickers)
	}
	sort.Strings(stockTickers)
	if !reflect.DeepEqual(stockTickers, []string{"AAPL"}) {
		t.Fatalf("stockTickers = %v, want [AAPL]", stockTickers)
	}
	sort.Strings(bondTickers)
	if !reflect.DeepEqual(bondTickers, []string{"OFZ"}) {
		t.Fatalf("bondTickers = %v, want [OFZ]", bondTickers)
	}
	if stockRubRates["2026-07-04"] != 0.012 || bondRubRates["2026-07-04"] != 0.012 {
		t.Fatalf("rub rates = %v %v", stockRubRates, bondRubRates)
	}

	storedRates := readQuoteRates(t, db)
	assertQuoteRatesEqual(t, storedRates["2026-07-03"], map[string]float64{"USD": 1})
	assertQuoteRatesEqual(t, storedRates["2026-07-04"], map[string]float64{"RUB": 0.012, "EUR": 0.86, "BTC": 60000, "AAPL": 220, "OFZ": 110})
}

func TestFetchTickerWithFallbacksRetriesAndFallsBack(t *testing.T) {
	fetcher := NewMarketFetcher(time.Second)
	fetcher.sleep = func(context.Context, time.Duration) error { return nil }

	attempts := 0
	nextCalls := 0
	data, sourceLog, err := fetcher.fetchTickerWithFallbacks(context.Background(), "BTC", []tickerSource{
		{name: "primary", fetch: func(context.Context) (map[string]float64, error) {
			attempts++
			if attempts == 1 {
				return nil, httpError{status: http.StatusTooManyRequests, message: "rate limited"}
			}
			return map[string]float64{"2026-07-04": 1}, nil
		}},
		{name: "fallback", fetch: func(context.Context) (map[string]float64, error) {
			nextCalls++
			return map[string]float64{"2026-07-04": 2}, nil
		}},
	}, RetryConfig{Attempts: 3, Delay: time.Millisecond})
	if err != nil {
		t.Fatalf("fetchTickerWithFallbacks() error = %v", err)
	}
	wantLog := []string{"primary: http 429", "fallback: ok"}
	if !reflect.DeepEqual(sourceLog, wantLog) {
		t.Fatalf("sourceLog = %v, want %v (fallback recovered it, worth flagging as degraded)", sourceLog, wantLog)
	}
	if attempts != 1 || nextCalls != 1 {
		t.Fatalf("attempts = %d, nextCalls = %d", attempts, nextCalls)
	}
	if data["2026-07-04"] != 2 {
		t.Fatalf("data = %v, want fallback result", data)
	}
}

func TestFetchTickerWithFallbacksLogsSourceOutcomesWhenAllFail(t *testing.T) {
	fetcher := NewMarketFetcher(time.Second)
	fetcher.sleep = func(context.Context, time.Duration) error { return nil }

	data, sourceLog, err := fetcher.fetchTickerWithFallbacks(context.Background(), "BTC", []tickerSource{
		{name: "primary", fetch: func(context.Context) (map[string]float64, error) {
			return nil, httpError{status: http.StatusUnauthorized, message: "key required"}
		}},
		{name: "fallback", fetch: func(context.Context) (map[string]float64, error) {
			return map[string]float64{}, nil
		}},
	}, RetryConfig{Attempts: 3, Delay: time.Millisecond})
	if err != nil {
		t.Fatalf("fetchTickerWithFallbacks() error = %v", err)
	}
	if data != nil {
		t.Fatalf("data = %v, want nil", data)
	}
	want := []string{"primary: http 401", "fallback: empty response"}
	if !reflect.DeepEqual(sourceLog, want) {
		t.Fatalf("sourceLog = %v, want %v", sourceLog, want)
	}
}

func TestRecordTickerOutcome(t *testing.T) {
	tests := []struct {
		name         string
		data         map[string]float64
		sourceLog    []string
		wantFailures int
		wantDegraded int
	}{
		{name: "healthy, first source succeeded, nothing recorded", data: map[string]float64{"x": 1}, sourceLog: nil},
		{name: "recovered via fallback, worth flagging as degraded", data: map[string]float64{"x": 1}, sourceLog: []string{"coinGecko: http 429", "yahoo: ok"}, wantDegraded: 1},
		{name: "every source failed", data: nil, sourceLog: []string{"coinGecko: http 429", "yahoo: http 401"}, wantFailures: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var outcome BatchOutcome
			recordTickerOutcome(&outcome, "crypto", "BTC", tt.data, tt.sourceLog)
			if len(outcome.Failures) != tt.wantFailures {
				t.Fatalf("Failures = %v, want %d entries", outcome.Failures, tt.wantFailures)
			}
			if len(outcome.Degraded) != tt.wantDegraded {
				t.Fatalf("Degraded = %v, want %d entries", outcome.Degraded, tt.wantDegraded)
			}
		})
	}
}

func openQuotesTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	schema := `
		CREATE TABLE moneyCurrency (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ticker TEXT NOT NULL
		);
		CREATE TABLE moneyAsset (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ticker TEXT NOT NULL,
			type TEXT NOT NULL,
			suspendedSince TEXT
		);
		CREATE TABLE moneyTransaction (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			kind TEXT NOT NULL,
			detailsJSON TEXT
		);
		CREATE TABLE moneyRateHistory (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			dateISO TEXT NOT NULL UNIQUE,
			ratesJson TEXT NOT NULL
		);
	`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		t.Fatalf("Exec() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertQuoteCurrency(t *testing.T, db *sql.DB, ticker string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO moneyCurrency (ticker) VALUES (?)`, ticker); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
}

func insertQuoteAsset(t *testing.T, db *sql.DB, id int64, ticker string, assetType string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO moneyAsset (id, ticker, type) VALUES (?, ?, ?)`, id, ticker, assetType); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
}

func insertQuoteTrade(t *testing.T, db *sql.DB, id int64, kind string, detailsJSON string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO moneyTransaction (id, kind, detailsJSON) VALUES (?, ?, ?)`, id, kind, detailsJSON); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
}

func insertQuoteRateHistory(t *testing.T, db *sql.DB, dateISO string, ratesJSON string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO moneyRateHistory (dateISO, ratesJson) VALUES (?, ?)`, dateISO, ratesJSON); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
}

func readQuoteRates(t *testing.T, db *sql.DB) map[string]map[string]float64 {
	t.Helper()
	rows, err := db.Query(`SELECT dateISO, ratesJson FROM moneyRateHistory ORDER BY dateISO ASC`)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	defer rows.Close()
	result := make(map[string]map[string]float64)
	for rows.Next() {
		var dateISO string
		var ratesJSON string
		if err := rows.Scan(&dateISO, &ratesJSON); err != nil {
			t.Fatalf("Scan() error = %v", err)
		}
		var rates map[string]float64
		if err := json.Unmarshal([]byte(ratesJSON), &rates); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		result[dateISO] = rates
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() error = %v", err)
	}
	return result
}

func assertQuoteRatesEqual(t *testing.T, got map[string]float64, want map[string]float64) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rates = %#v, want %#v", got, want)
	}
}

func cloneBatchRates(value map[string]map[string]float64) map[string]map[string]float64 {
	result := make(map[string]map[string]float64, len(value))
	for dateISO, rates := range value {
		result[dateISO] = cloneTickerRates(rates)
	}
	return result
}

func cloneTickerRates(value map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(value))
	for ticker, rate := range value {
		result[ticker] = rate
	}
	return result
}

var _ platformclock.Clock = fixedClock{}
