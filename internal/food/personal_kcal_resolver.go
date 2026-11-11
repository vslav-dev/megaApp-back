package food

import (
	"maps"
	"math"
	"time"
)

const defaultPersonalNormKcals = 2200

// decayedAverage mirrors plans/research/norm-comparison/algorithms.js decayedAverage: geometric
// recency weighting over the lookbackMonths months strictly before monthIdx (weight at `back`
// months ago = decayRate^(back-1)), skipping months with no raw value (frozen/missing).
func decayedAverage(monthIdx int, lookbackMonths int, decayRate float64, fallback float64, valueAt func(monthIdx int) (float64, bool)) float64 {
	var weightSum, valueSum float64
	for back := 1; back <= lookbackMonths; back++ {
		j := monthIdx - back
		if j < 0 {
			continue
		}
		value, ok := valueAt(j)
		if !ok {
			continue
		}
		weight := math.Pow(decayRate, float64(back-1))
		weightSum += weight
		valueSum += weight * value
	}
	if weightSum > 0 {
		return valueSum / weightSum
	}
	return fallback
}

// applyMonthlyCap bounds how far the applied (displayed) value may move from the previous
// month's applied value, symmetric in log-space — a step up and a step down of the same
// relative size cost the same "distance" (§3.4 of the GA 2.0 plan).
func applyMonthlyCap(prevApplied float64, rawSeed float64, maxMonthlyChangePercent float64) float64 {
	maxLogStep := math.Log(1 + maxMonthlyChangePercent/100)
	logStep := math.Log(rawSeed / prevApplied)
	if logStep > maxLogStep {
		logStep = maxLogStep
	} else if logStep < -maxLogStep {
		logStep = -maxLogStep
	}
	return prevApplied * math.Exp(logStep)
}

// sequentialYearMonths builds the chronological "YYYY-MM" sequence from firstYearMonth to
// lastYearMonth inclusive, used as the calendar backbone for replaying decay-average + cap.
func sequentialYearMonths(firstYearMonth string, lastYearMonth string) []string {
	cursor, err := time.Parse("2006-01", firstYearMonth)
	if err != nil {
		return nil
	}
	last, err := time.Parse("2006-01", lastYearMonth)
	if err != nil {
		return nil
	}
	var months []string
	for !cursor.After(last) {
		months = append(months, cursor.Format("2006-01"))
		cursor = cursor.AddDate(0, 1, 0)
	}
	return months
}

// PersonalKcalResolver answers "what was the applied (decay-averaged, capped) value of this
// product/norm/X as of this calendar month", replaying §3.3-3.4 over the append-only raw
// history loaded once per request. It never mutates history — only the job persists new rows.
type PersonalKcalResolver struct {
	allMonths            []string
	cfg                  PersonalKcalConfig
	catalogueKcals       map[int64]float64
	rawKcal              map[int64]map[string]float64
	rawNorm              map[string]float64
	rawX                 map[string]float64
	appliedKcalByProduct map[int64]map[string]float64
	appliedNorm          map[string]float64
	appliedX             map[string]float64
}

func NewPersonalKcalResolver(allMonths []string, catalogueKcals map[int64]float64, kcalRows []PersonalKcalHistoryRow, normRows []PersonalNormHistoryRow, cfg PersonalKcalConfig) *PersonalKcalResolver {
	rawKcal := make(map[int64]map[string]float64)
	for _, row := range kcalRows {
		if rawKcal[row.FoodCatalogueID] == nil {
			rawKcal[row.FoodCatalogueID] = make(map[string]float64)
		}
		rawKcal[row.FoodCatalogueID][row.YearMonth] = row.KcalsPer100g
	}
	rawNorm := make(map[string]float64)
	rawX := make(map[string]float64)
	for _, row := range normRows {
		rawNorm[row.YearMonth] = row.NormKcals
		rawX[row.YearMonth] = row.KcalPerKg
	}

	resolver := &PersonalKcalResolver{
		allMonths:            allMonths,
		cfg:                  cfg,
		catalogueKcals:       catalogueKcals,
		rawKcal:              rawKcal,
		rawNorm:              rawNorm,
		rawX:                 rawX,
		appliedKcalByProduct: make(map[int64]map[string]float64),
	}
	resolver.appliedNorm = resolveAppliedScalarSeries(allMonths, rawNorm, defaultPersonalNormKcals, cfg.LookbackMonths, cfg.DecayRate)
	resolver.appliedX = resolveAppliedScalarSeries(allMonths, rawX, kcalsIn1KG, cfg.LookbackMonths, cfg.DecayRate)
	return resolver
}

func (r *PersonalKcalResolver) AppliedKcal(catalogueID int64, yearMonth string) float64 {
	series, ok := r.appliedKcalByProduct[catalogueID]
	if !ok {
		series = resolveAppliedKcalSeries(r.allMonths, r.rawKcal[catalogueID], r.catalogueKcals[catalogueID], r.cfg)
		r.appliedKcalByProduct[catalogueID] = series
	}
	if value, ok := series[yearMonth]; ok {
		return value
	}
	return r.catalogueKcals[catalogueID]
}

func (r *PersonalKcalResolver) AppliedNorm(yearMonth string) float64 {
	if value, ok := r.appliedNorm[yearMonth]; ok {
		return value
	}
	return defaultPersonalNormKcals
}

// AppliedKcalsNow returns the complete per-catalogue-id map for one specific month — every
// catalogue id is present (bootstrapped to the catalogue default for untouched products),
// mirroring how GetCoefficients always defaulted missing ids to 1.0.
func (r *PersonalKcalResolver) AppliedKcalsNow(yearMonth string) map[int64]float64 {
	result := make(map[int64]float64, len(r.catalogueKcals))
	maps.Copy(result, r.catalogueKcals)
	for id := range r.rawKcal {
		result[id] = r.AppliedKcal(id, yearMonth)
	}
	return result
}

// resolveAppliedKcalSeries replays §3.3-3.4 for a single product across every month from its
// first touch onward. Months before the first touch are intentionally absent — the caller
// falls back to the catalogue default, since no personal history exists yet for those months.
func resolveAppliedKcalSeries(allMonths []string, rawForProduct map[string]float64, catalogueDefault float64, cfg PersonalKcalConfig) map[string]float64 {
	applied := make(map[string]float64)
	firstTouchIdx := -1
	for idx, yearMonth := range allMonths {
		if _, ok := rawForProduct[yearMonth]; ok {
			firstTouchIdx = idx
			break
		}
	}
	if firstTouchIdx == -1 {
		return applied
	}

	var prevApplied float64
	hasPrev := false
	for idx := firstTouchIdx; idx < len(allMonths); idx++ {
		fallback := catalogueDefault
		if hasPrev {
			fallback = prevApplied
		}
		rawSeed := decayedAverage(idx, cfg.LookbackMonths, cfg.DecayRate, fallback, func(j int) (float64, bool) {
			value, ok := rawForProduct[allMonths[j]]
			return value, ok
		})
		value := rawSeed
		if hasPrev {
			value = applyMonthlyCap(prevApplied, rawSeed, cfg.MaxMonthlyChangePercent)
		}
		applied[allMonths[idx]] = value
		prevApplied = value
		hasPrev = true
	}
	return applied
}

// resolveAppliedScalarSeries replays the same decay-average for norm/X, which carry no hard
// cap (§3.7) — every month gets a value, starting from initialDefault before any history exists.
func resolveAppliedScalarSeries(allMonths []string, raw map[string]float64, initialDefault float64, lookbackMonths int, decayRate float64) map[string]float64 {
	applied := make(map[string]float64, len(allMonths))
	current := initialDefault
	for idx, yearMonth := range allMonths {
		current = decayedAverage(idx, lookbackMonths, decayRate, current, func(j int) (float64, bool) {
			value, ok := raw[allMonths[j]]
			return value, ok
		})
		applied[yearMonth] = current
	}
	return applied
}
