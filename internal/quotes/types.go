package quotes

import "time"

type Config struct {
	FetchDays      int
	RetryAttempts  int
	RetryDelay     time.Duration
	RequestTimeout time.Duration
}

type OpenAsset struct {
	ID             int64
	Ticker         string
	Type           string
	SuspendedSince *string
}

type RateHistoryRow struct {
	ID        int64
	DateISO   string
	RatesJSON string
}

type RunResult struct {
	UpsertedCount int             `json:"upsertedCount"`
	FromISO       string          `json:"fromISO"`
	ToISO         string          `json:"toISO"`
	Failures      []TickerFailure `json:"failures"`
	Degraded      []TickerFailure `json:"degraded"`
}

// TickerFailure records a ticker whose per-source outcomes are worth surfacing: either it
// ended the run with no price at all (RunResult.Failures), or it got one only after earlier
// sources failed (RunResult.Degraded). Sources lists every source tried in order, e.g.
// ["coinGecko: http 429", "yahoo: ok"] — the last entry is "ok" when a source did succeed,
// otherwise every source failed. This is what lets a future incident be diagnosed from logs
// instead of reconstructed after the fact from raw price-history gaps.
type TickerFailure struct {
	Kind    string   `json:"kind"`
	Ticker  string   `json:"ticker"`
	Sources []string `json:"sources"`
}

type RetryConfig struct {
	Attempts int
	Delay    time.Duration
}
