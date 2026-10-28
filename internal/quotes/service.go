package quotes

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	platformclock "megaapp-back/internal/platform/clock"
)

type Service struct {
	repo                *Repository
	cfg                 Config
	clock               platformclock.Clock
	currencyBatch       func([]string) BatchFetcher
	cryptoBatch         func([]OpenAsset) BatchFetcher
	stockBatchWithRates func([]OpenAsset, map[string]float64) BatchFetcher
	bondBatchWithRates  func([]OpenAsset, map[string]float64) BatchFetcher
}

func NewService(repo *Repository, cfg Config, clk platformclock.Clock, marketFetcher *MarketFetcher) *Service {
	if clk == nil {
		clk = platformclock.NewRealClock()
	}
	return &Service{
		repo:                repo,
		cfg:                 cfg,
		clock:               clk,
		currencyBatch:       marketFetcher.NewCurrencyBatch,
		cryptoBatch:         marketFetcher.NewCryptoBatch,
		stockBatchWithRates: marketFetcher.NewStockBatch,
		bondBatchWithRates:  marketFetcher.NewBondBatch,
	}
}

func (s *Service) Run(ctx context.Context) (RunResult, error) {
	fromISO, toISO := s.buildDateRange()
	rateRows, err := s.repo.ListRateHistoryRange(ctx, fromISO, toISO)
	if err != nil {
		return RunResult{}, err
	}
	ratesMap := make(map[string]map[string]float64)
	for _, row := range rateRows {
		rates, ok := parseStoredRates(row.RatesJSON)
		if !ok {
			ratesMap[row.DateISO] = map[string]float64{}
			continue
		}
		ratesMap[row.DateISO] = rates
	}

	var failures []TickerFailure
	var degraded []TickerFailure
	retry := RetryConfig{Attempts: s.cfg.RetryAttempts, Delay: s.cfg.RetryDelay}

	currencyTickers, err := s.repo.ListCurrencyTickers(ctx)
	if err != nil {
		return RunResult{}, err
	}
	if len(currencyTickers) > 0 {
		currencyRates, currencyOutcome, err := s.currencyBatch(currencyTickers).Fetch(ctx, fromISO, toISO, retry)
		if err != nil {
			return RunResult{}, err
		}
		mergeRatesMap(ratesMap, currencyRates)
		failures = append(failures, currencyOutcome.Failures...)
		degraded = append(degraded, currencyOutcome.Degraded...)
	}

	openAssets, err := s.repo.ListOpenAssets(ctx)
	if err != nil {
		return RunResult{}, err
	}
	cryptoAssets, stockAssets, bondAssets := splitAssetsByType(openAssets)

	if len(cryptoAssets) > 0 {
		cryptoRates, cryptoOutcome, err := s.cryptoBatch(cryptoAssets).Fetch(ctx, fromISO, toISO, retry)
		if err != nil {
			return RunResult{}, err
		}
		mergeRatesMap(ratesMap, cryptoRates)
		failures = append(failures, cryptoOutcome.Failures...)
		degraded = append(degraded, cryptoOutcome.Degraded...)
	}

	rubUSDRates := collectTickerRates(ratesMap, "RUB")
	if len(stockAssets) > 0 {
		stockRates, stockOutcome, err := s.stockBatchWithRates(stockAssets, rubUSDRates).Fetch(ctx, fromISO, toISO, retry)
		if err != nil {
			return RunResult{}, err
		}
		mergeRatesMap(ratesMap, stockRates)
		failures = append(failures, stockOutcome.Failures...)
		degraded = append(degraded, stockOutcome.Degraded...)
	}
	if len(bondAssets) > 0 {
		bondRates, bondOutcome, err := s.bondBatchWithRates(bondAssets, rubUSDRates).Fetch(ctx, fromISO, toISO, retry)
		if err != nil {
			return RunResult{}, err
		}
		mergeRatesMap(ratesMap, bondRates)
		failures = append(failures, bondOutcome.Failures...)
		degraded = append(degraded, bondOutcome.Degraded...)
	}

	dates := make([]string, 0, len(ratesMap))
	for dateISO, rates := range ratesMap {
		if len(rates) == 0 {
			continue
		}
		dates = append(dates, dateISO)
	}
	sort.Strings(dates)

	upsertedCount := 0
	for _, dateISO := range dates {
		encoded, err := json.Marshal(ratesMap[dateISO])
		if err != nil {
			return RunResult{}, fmt.Errorf("encode rates for %s: %w", dateISO, err)
		}
		if err := s.repo.UpsertRateHistoryEntry(ctx, dateISO, string(encoded)); err != nil {
			return RunResult{}, err
		}
		upsertedCount++
	}

	return RunResult{UpsertedCount: upsertedCount, FromISO: fromISO, ToISO: toISO, Failures: failures, Degraded: degraded}, nil
}

func (s *Service) buildDateRange() (string, string) {
	now := s.clock.Now().UTC()
	toDate := now.AddDate(0, 0, -1)
	fromDate := now.AddDate(0, 0, -s.cfg.FetchDays)
	return fromDate.Format("2006-01-02"), toDate.Format("2006-01-02")
}

func parseStoredRates(value string) (map[string]float64, bool) {
	var result map[string]float64
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return nil, false
	}
	if result == nil {
		return nil, false
	}
	return result, true
}

func mergeRatesMap(target map[string]map[string]float64, source map[string]map[string]float64) {
	for dateISO, rates := range source {
		if target[dateISO] == nil {
			target[dateISO] = make(map[string]float64)
		}
		for ticker, value := range rates {
			if value != 0 {
				target[dateISO][ticker] = value
			}
		}
	}
}

func splitAssetsByType(assets []OpenAsset) ([]OpenAsset, []OpenAsset, []OpenAsset) {
	var cryptoAssets []OpenAsset
	var stockAssets []OpenAsset
	var bondAssets []OpenAsset
	for _, asset := range assets {
		switch strings.ToLower(strings.TrimSpace(asset.Type)) {
		case "crypto":
			cryptoAssets = append(cryptoAssets, asset)
		case "stock":
			stockAssets = append(stockAssets, asset)
		case "bond":
			bondAssets = append(bondAssets, asset)
		}
	}
	return cryptoAssets, stockAssets, bondAssets
}

func collectTickerRates(ratesMap map[string]map[string]float64, ticker string) map[string]float64 {
	result := make(map[string]float64)
	for dateISO, rates := range ratesMap {
		if value := rates[ticker]; value != 0 {
			result[dateISO] = value
		}
	}
	return result
}
