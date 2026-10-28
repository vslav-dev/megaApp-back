package quotes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type BatchFetcher interface {
	Fetch(ctx context.Context, fromISO string, toISO string, retry RetryConfig) (map[string]map[string]float64, BatchOutcome, error)
}

// BatchOutcome separates a batch fetch's two kinds of noteworthy ticker: Failures got no
// price from any source, Degraded got one but only after earlier sources failed first.
type BatchOutcome struct {
	Failures []TickerFailure
	Degraded []TickerFailure
}

type tickerSource struct {
	name  string
	fetch func(context.Context) (map[string]float64, error)
}

type MarketFetcher struct {
	client *http.Client
	sleep  func(context.Context, time.Duration) error
}

type currencyBatchFetcher struct {
	fetcher *MarketFetcher
	tickers []string
}

type assetBatchFetcher struct {
	fetcher     *MarketFetcher
	assets      []OpenAsset
	kind        string
	rubUSDRates map[string]float64
}

type moexRow struct {
	DateISO string
	Close   float64
}

var coinGeckoIDByTicker = map[string]string{
	"BTC":  "bitcoin",
	"ETH":  "ethereum",
	"XRP":  "ripple",
	"XMR":  "monero",
	"DASH": "dash",
	"ZEC":  "zcash",
	"GLM":  "golem",
	"ARK":  "ark",
}

var yahooCurrencySymbolByTicker = map[string]string{
	"EUR": "EURUSD=X",
	"RUB": "USDRUB=X",
}

var yahooCurrencyInvertTickers = map[string]struct{}{
	"RUB": {},
	"KZT": {},
}

var frankfurterSupportedTickers = map[string]struct{}{
	"EUR": {},
	"GBP": {},
}

func NewMarketFetcher(requestTimeout time.Duration) *MarketFetcher {
	return &MarketFetcher{
		client: &http.Client{Timeout: requestTimeout},
		sleep:  sleepContext,
	}
}

func (f *MarketFetcher) NewCurrencyBatch(tickers []string) BatchFetcher {
	return currencyBatchFetcher{fetcher: f, tickers: tickers}
}

func (f *MarketFetcher) NewCryptoBatch(assets []OpenAsset) BatchFetcher {
	return assetBatchFetcher{fetcher: f, assets: assets, kind: "crypto"}
}

func (f *MarketFetcher) NewStockBatch(assets []OpenAsset, rubUSDRates map[string]float64) BatchFetcher {
	return assetBatchFetcher{fetcher: f, assets: assets, kind: "stock", rubUSDRates: rubUSDRates}
}

func (f *MarketFetcher) NewBondBatch(assets []OpenAsset, rubUSDRates map[string]float64) BatchFetcher {
	return assetBatchFetcher{fetcher: f, assets: assets, kind: "bond", rubUSDRates: rubUSDRates}
}

func (f currencyBatchFetcher) Fetch(ctx context.Context, fromISO string, toISO string, retry RetryConfig) (map[string]map[string]float64, BatchOutcome, error) {
	results := make(map[string]map[string]float64)
	var outcome BatchOutcome
	for _, ticker := range f.tickers {
		data, sourceLog, err := f.fetcher.fetchTickerWithFallbacks(ctx, ticker, f.fetcher.currencySources(ticker, fromISO, toISO), retry)
		if err != nil {
			return nil, BatchOutcome{}, err
		}
		recordTickerOutcome(&outcome, "currency", ticker, data, sourceLog)
		if len(data) > 0 {
			mergeTickerResults(results, ticker, data)
		}
	}
	return results, outcome, nil
}

func (f assetBatchFetcher) Fetch(ctx context.Context, fromISO string, toISO string, retry RetryConfig) (map[string]map[string]float64, BatchOutcome, error) {
	results := make(map[string]map[string]float64)
	var outcome BatchOutcome
	for _, asset := range f.assets {
		var sources []tickerSource
		switch f.kind {
		case "crypto":
			sources = f.fetcher.cryptoSources(asset.Ticker, fromISO, toISO)
		case "stock":
			sources = f.fetcher.stockSources(asset.Ticker, fromISO, toISO, f.rubUSDRates)
		case "bond":
			sources = f.fetcher.bondSources(asset.Ticker, fromISO, toISO, f.rubUSDRates)
		default:
			continue
		}
		data, sourceLog, err := f.fetcher.fetchTickerWithFallbacks(ctx, asset.Ticker, sources, retry)
		if err != nil {
			return nil, BatchOutcome{}, err
		}
		recordTickerOutcome(&outcome, f.kind, asset.Ticker, data, sourceLog)
		if len(data) > 0 {
			mergeTickerResults(results, asset.Ticker, data)
		}
	}
	return results, outcome, nil
}

// recordTickerOutcome files a ticker under Degraded (got a price, but not from the first
// source tried) or Failures (got no price at all) — a nil/empty sourceLog means the first
// source succeeded outright, which is the healthy case and isn't recorded.
func recordTickerOutcome(outcome *BatchOutcome, kind string, ticker string, data map[string]float64, sourceLog []string) {
	if len(sourceLog) == 0 {
		return
	}
	entry := TickerFailure{Kind: kind, Ticker: ticker, Sources: sourceLog}
	if len(data) > 0 {
		outcome.Degraded = append(outcome.Degraded, entry)
	} else {
		outcome.Failures = append(outcome.Failures, entry)
	}
}

// fetchTickerWithFallbacks tries each source in order, retrying transient failures within a
// source before moving to the next one. The returned []string is a per-source outcome log
// ("coinGecko: http 429", "yahoo: ok", ...) in the order sources were tried. It's nil when
// the very first source succeeded outright (the healthy, common case — nothing worth
// logging); non-nil with a trailing "ok" when a later source recovered it (worth watching —
// the earlier source is degraded); non-nil with no "ok" when every source failed.
func (f *MarketFetcher) fetchTickerWithFallbacks(ctx context.Context, label string, sources []tickerSource, retry RetryConfig) (map[string]float64, []string, error) {
	var sourceLog []string
	for _, source := range sources {
		outcome := "empty response"
		for attempt := 1; attempt <= retry.Attempts; attempt++ {
			data, err := source.fetch(ctx)
			if err == nil {
				if len(data) > 0 {
					if len(sourceLog) == 0 {
						return data, nil, nil
					}
					return data, append(sourceLog, source.name+": ok"), nil
				}
				break
			}
			outcome = describeFetchError(err)
			status := httpStatusOf(err)
			isTooManyRequests := status == http.StatusTooManyRequests
			isTransient := status == 0 || status >= http.StatusInternalServerError || isTooManyRequests
			if !isTransient || isTooManyRequests || attempt >= retry.Attempts {
				break
			}
			if err := f.sleep(ctx, retry.Delay); err != nil {
				return nil, nil, fmt.Errorf("sleep after %s %s failure: %w", source.name, label, err)
			}
		}
		sourceLog = append(sourceLog, source.name+": "+outcome)
	}
	return nil, sourceLog, nil
}

func describeFetchError(err error) string {
	if status := httpStatusOf(err); status != 0 {
		return fmt.Sprintf("http %d", status)
	}
	return "network error: " + err.Error()
}

func (f *MarketFetcher) currencySources(ticker string, fromISO string, toISO string) []tickerSource {
	var sources []tickerSource
	if symbol := yahooCurrencySymbolByTicker[ticker]; symbol != "" {
		_, shouldInvert := yahooCurrencyInvertTickers[ticker]
		sources = append(sources, tickerSource{
			name: "yahoo",
			fetch: func(ctx context.Context) (map[string]float64, error) {
				data, err := f.yahooFetch(ctx, symbol, fromISO, toISO)
				if err != nil {
					return nil, err
				}
				if !shouldInvert {
					return data, nil
				}
				return invertMap(data), nil
			},
		})
	}
	if _, ok := frankfurterSupportedTickers[ticker]; ok {
		sources = append(sources, tickerSource{name: "frankfurter", fetch: func(ctx context.Context) (map[string]float64, error) {
			return f.frankfurterFetch(ctx, ticker, fromISO, toISO)
		}})
	}
	sources = append(sources, tickerSource{name: "exchangeRateHost", fetch: func(ctx context.Context) (map[string]float64, error) {
		return f.exchangeRateHostFetch(ctx, ticker, fromISO, toISO)
	}})
	return sources
}

func (f *MarketFetcher) cryptoSources(ticker string, fromISO string, toISO string) []tickerSource {
	return []tickerSource{
		{name: "coinGecko", fetch: func(ctx context.Context) (map[string]float64, error) {
			return f.coinGeckoFetch(ctx, ticker, fromISO, toISO)
		}},
		{name: "yahoo", fetch: func(ctx context.Context) (map[string]float64, error) {
			return f.yahooFetch(ctx, ticker+"-USD", fromISO, toISO)
		}},
		{name: "binance", fetch: func(ctx context.Context) (map[string]float64, error) {
			return f.binanceFetch(ctx, ticker, fromISO, toISO)
		}},
	}
}

func (f *MarketFetcher) stockSources(ticker string, fromISO string, toISO string, rubUSDRates map[string]float64) []tickerSource {
	return []tickerSource{{name: "moex", fetch: func(ctx context.Context) (map[string]float64, error) {
		for _, board := range []string{"TQBR", "TQTF"} {
			data, err := f.moexFetch(ctx, "shares", ticker, board, fromISO, toISO, rubUSDRates, rubToUSD)
			if err != nil {
				return nil, err
			}
			if len(data) > 0 {
				return data, nil
			}
		}
		return map[string]float64{}, nil
	}}}
}

func (f *MarketFetcher) bondSources(ticker string, fromISO string, toISO string, rubUSDRates map[string]float64) []tickerSource {
	return []tickerSource{{name: "moex", fetch: func(ctx context.Context) (map[string]float64, error) {
		return f.moexFetch(ctx, "bonds", ticker, "TQOB", fromISO, toISO, rubUSDRates, bondRubToUSD)
	}}}
}

func (f *MarketFetcher) coinGeckoFetch(ctx context.Context, ticker string, fromISO string, toISO string) (map[string]float64, error) {
	id := coinGeckoIDByTicker[ticker]
	if id == "" {
		return map[string]float64{}, nil
	}
	requestURL := fmt.Sprintf("https://api.coingecko.com/api/v3/coins/%s/market_chart/range?vs_currency=usd&from=%d&to=%d", url.PathEscape(id), toUnixSec(fromISO, false), toUnixSec(toISO, true))
	var response struct {
		Prices [][]float64 `json:"prices"`
	}
	if err := f.getJSON(ctx, requestURL, map[string]string{"Accept": "application/json"}, &response); err != nil {
		return nil, err
	}
	result := make(map[string]float64)
	for _, item := range response.Prices {
		if len(item) < 2 {
			continue
		}
		result[time.UnixMilli(int64(item[0])).UTC().Format("2006-01-02")] = item[1]
	}
	return result, nil
}

func (f *MarketFetcher) yahooFetch(ctx context.Context, symbol string, fromISO string, toISO string) (map[string]float64, error) {
	requestURL := fmt.Sprintf("https://query1.finance.yahoo.com/v8/finance/chart/%s?period1=%d&period2=%d&interval=1d", url.QueryEscape(symbol), toUnixSec(fromISO, false), toUnixSec(toISO, true))
	var response struct {
		Chart struct {
			Result []struct {
				Timestamp  []int64 `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Close []*float64 `json:"close"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
		} `json:"chart"`
	}
	if err := f.getJSON(ctx, requestURL, map[string]string{"User-Agent": "Mozilla/5.0"}, &response); err != nil {
		return nil, err
	}
	if len(response.Chart.Result) == 0 || len(response.Chart.Result[0].Indicators.Quote) == 0 {
		return map[string]float64{}, nil
	}
	result := make(map[string]float64)
	timestamps := response.Chart.Result[0].Timestamp
	closes := response.Chart.Result[0].Indicators.Quote[0].Close
	for index, timestamp := range timestamps {
		if index >= len(closes) || closes[index] == nil {
			continue
		}
		result[time.Unix(timestamp, 0).UTC().Format("2006-01-02")] = *closes[index]
	}
	return result, nil
}

func (f *MarketFetcher) binanceFetch(ctx context.Context, ticker string, fromISO string, toISO string) (map[string]float64, error) {
	requestURL := fmt.Sprintf("https://api.binance.com/api/v3/klines?symbol=%sUSDT&interval=1d&startTime=%d&endTime=%d&limit=100", url.QueryEscape(ticker), toUnixMs(fromISO, false), toUnixMs(toISO, true))
	var response [][]any
	if err := f.getJSON(ctx, requestURL, nil, &response); err != nil {
		return nil, err
	}
	result := make(map[string]float64)
	for _, item := range response {
		if len(item) < 5 {
			continue
		}
		closeValue, _ := strconv.ParseFloat(fmt.Sprint(item[4]), 64)
		if closeValue == 0 {
			continue
		}
		openTime, ok := anyInt64(item[0])
		if !ok {
			continue
		}
		result[time.UnixMilli(openTime).UTC().Format("2006-01-02")] = closeValue
	}
	return result, nil
}

func (f *MarketFetcher) frankfurterFetch(ctx context.Context, ticker string, fromISO string, toISO string) (map[string]float64, error) {
	requestURL := fmt.Sprintf("https://api.frankfurter.dev/v1/%s..%s?from=USD&to=%s", fromISO, toISO, url.QueryEscape(ticker))
	var response struct {
		Rates map[string]map[string]float64 `json:"rates"`
	}
	if err := f.getJSON(ctx, requestURL, nil, &response); err != nil {
		return nil, err
	}
	result := make(map[string]float64)
	for dateISO, rateMap := range response.Rates {
		rate := rateMap[ticker]
		if rate == 0 {
			continue
		}
		if ticker == "EUR" {
			result[dateISO] = rate
			continue
		}
		result[dateISO] = invertRate(rate)
	}
	return result, nil
}

func (f *MarketFetcher) exchangeRateHostFetch(ctx context.Context, ticker string, fromISO string, toISO string) (map[string]float64, error) {
	requestURL := fmt.Sprintf("https://api.exchangerate.host/timeseries?base=USD&symbols=%s&start_date=%s&end_date=%s", url.QueryEscape(ticker), fromISO, toISO)
	var response struct {
		Rates map[string]map[string]float64 `json:"rates"`
	}
	if err := f.getJSON(ctx, requestURL, nil, &response); err != nil {
		return nil, err
	}
	result := make(map[string]float64)
	for dateISO, rateMap := range response.Rates {
		rate := rateMap[ticker]
		if rate == 0 {
			continue
		}
		if ticker == "EUR" {
			result[dateISO] = rate
			continue
		}
		result[dateISO] = invertRate(rate)
	}
	return result, nil
}

func (f *MarketFetcher) moexFetch(ctx context.Context, market string, ticker string, board string, fromISO string, toISO string, rubUSDRates map[string]float64, normalize func(float64, float64) float64) (map[string]float64, error) {
	rows, err := f.moexHistoryRows(ctx, fmt.Sprintf("https://iss.moex.com/iss/history/engines/stock/markets/%s/securities/%s.json?from=%s&till=%s&limit=100&boardid=%s", market, url.PathEscape(ticker), fromISO, toISO, url.QueryEscape(board)))
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	result := make(map[string]float64)
	for _, row := range rows {
		rubUSD := rubUSDRates[row.DateISO]
		if rubUSD == 0 {
			continue
		}
		value := normalize(row.Close, rubUSD)
		if value > 0 {
			result[row.DateISO] = value
		}
	}
	return result, nil
}

func (f *MarketFetcher) moexHistoryRows(ctx context.Context, requestURL string) ([]moexRow, error) {
	var result []moexRow
	for start := 0; ; start += 100 {
		pagedURL := requestURL + "&start=" + strconv.Itoa(start)
		var response struct {
			History struct {
				Columns []string `json:"columns"`
				Data    [][]any  `json:"data"`
			} `json:"history"`
		}
		if err := f.getJSON(ctx, pagedURL, nil, &response); err != nil {
			if httpStatusOf(err) == http.StatusNotFound {
				return nil, nil
			}
			return nil, err
		}
		if len(response.History.Data) == 0 {
			break
		}
		dateIndex := indexOf(response.History.Columns, "TRADEDATE")
		closeIndex := indexOf(response.History.Columns, "CLOSE")
		if dateIndex == -1 || closeIndex == -1 {
			return nil, nil
		}
		for _, row := range response.History.Data {
			if dateIndex >= len(row) || closeIndex >= len(row) {
				continue
			}
			dateISO, ok := row[dateIndex].(string)
			if !ok || strings.TrimSpace(dateISO) == "" {
				continue
			}
			closeValue, ok := anyFloat64(row[closeIndex])
			if !ok {
				continue
			}
			result = append(result, moexRow{DateISO: dateISO, Close: closeValue})
		}
		if len(response.History.Data) < 100 {
			break
		}
	}
	return result, nil
}

func (f *MarketFetcher) getJSON(ctx context.Context, requestURL string, headers map[string]string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("build request %s: %w", requestURL, err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := f.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return httpError{status: response.StatusCode, message: fmt.Sprintf("http %d: %s", response.StatusCode, requestURL)}
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read response %s: %w", requestURL, err)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode response %s: %w", requestURL, err)
	}
	return nil
}

type httpError struct {
	status  int
	message string
}

func (e httpError) Error() string {
	return e.message
}

func httpStatusOf(err error) int {
	var target httpError
	if errors.As(err, &target) {
		return target.status
	}
	return 0
}

func mergeTickerResults(target map[string]map[string]float64, ticker string, source map[string]float64) {
	for dateISO, value := range source {
		if target[dateISO] == nil {
			target[dateISO] = make(map[string]float64)
		}
		target[dateISO][ticker] = value
	}
}

func invertMap(source map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(source))
	for dateISO, value := range source {
		inverted := invertRate(value)
		if inverted > 0 {
			result[dateISO] = inverted
		}
	}
	return result
}

func invertRate(value float64) float64 {
	if value == 0 {
		return 0
	}
	return 1 / value
}

func rubToUSD(value float64, rubUSD float64) float64 {
	if value == 0 || rubUSD == 0 {
		return 0
	}
	return value * rubUSD
}

func bondRubToUSD(value float64, rubUSD float64) float64 {
	if value == 0 || rubUSD == 0 {
		return 0
	}
	return value * 10 * rubUSD
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func toUnixSec(dateISO string, endOfDay bool) int64 {
	if endOfDay {
		return parseISODate(dateISO).Add(24*time.Hour - time.Second).Unix()
	}
	return parseISODate(dateISO).Unix()
}

func toUnixMs(dateISO string, endOfDay bool) int64 {
	return toUnixSec(dateISO, endOfDay) * 1000
}

func parseISODate(value string) time.Time {
	parsed, _ := time.Parse("2006-01-02", value)
	return parsed.UTC()
}

func anyFloat64(value any) (float64, bool) {
	switch current := value.(type) {
	case float64:
		return current, true
	case json.Number:
		parsed, err := current.Float64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(current), 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func anyInt64(value any) (int64, bool) {
	switch current := value.(type) {
	case int64:
		return current, true
	case float64:
		return int64(current), true
	case json.Number:
		parsed, err := current.Int64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func indexOf(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}
