package food

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"math"
	"math/rand"
	"slices"
	"time"
)

type PersonalKcalConfig struct {
	LookbackMonths          int
	DecayRate               float64
	CoverageThreshold       float64
	MaxMonthlyChangePercent float64
	AnchorLambda            float64
	EvidenceHalfKcal        float64
	CoefLogStep             float64
	NormStep                float64
	XStep                   float64
	Population              int
	MaxGenerations          int
	MaxStale                int
}

func DefaultPersonalKcalConfig() PersonalKcalConfig {
	return PersonalKcalConfig{
		LookbackMonths:          3,
		DecayRate:               0.6,
		CoverageThreshold:       0.5,
		MaxMonthlyChangePercent: 10,
		AnchorLambda:            3,
		EvidenceHalfKcal:        333,
		CoefLogStep:             0.03,
		NormStep:                33,
		XStep:                   33,
		Population:              33,
		MaxGenerations:          333,
		MaxStale:                33,
	}
}

func (s *Service) SetPersonalKcalConfig(cfg PersonalKcalConfig) {
	s.personalKcalConfig = cfg
}

type PersonalKcalJobUserResult struct {
	UserID         int64  `json:"userId"`
	Success        bool   `json:"success"`
	MonthsComputed int    `json:"monthsComputed,omitempty"`
	Error          string `json:"error,omitempty"`
}

type PersonalKcalJobResult struct {
	ProcessedCount int                         `json:"processedCount"`
	SuccessCount   int                         `json:"successCount"`
	FailedCount    int                         `json:"failedCount"`
	Users          []PersonalKcalJobUserResult `json:"users"`
}

var ErrPersonalKcalJobAlreadyRunning = errors.New("personal kcal job is already running")

type personalKcalMonthBucket struct {
	yearMonth string
	dates     []string
}

type personalKcalDiaryEntry struct {
	catalogueID int64
	grams       float64
}

type personalKcalGenome struct {
	kcal100 map[int64]float64
	norm    float64
	x       float64
}

func (s *Service) RunPersonalKcalJob(ctx context.Context) (*PersonalKcalJobResult, error) {
	if !s.beginPersonalKcalJobRun() {
		return nil, ErrPersonalKcalJobAlreadyRunning
	}
	defer s.endPersonalKcalJobRun()

	userIDs, err := s.repo.GetAllUserIDs(ctx)
	if err != nil {
		return nil, err
	}

	result := &PersonalKcalJobResult{
		ProcessedCount: len(userIDs),
		Users:          make([]PersonalKcalJobUserResult, 0, len(userIDs)),
	}
	for _, userID := range userIDs {
		userResult := PersonalKcalJobUserResult{UserID: userID}
		monthsComputed, err := s.processUserPersonalKcal(ctx, userID)
		if err != nil {
			userResult.Error = err.Error()
			result.FailedCount++
			result.Users = append(result.Users, userResult)
			continue
		}
		userResult.Success = true
		userResult.MonthsComputed = monthsComputed
		result.SuccessCount++
		result.Users = append(result.Users, userResult)
		if monthsComputed > 0 {
			s.InvalidateStats(userID)
		}
	}
	return result, nil
}

func (s *Service) beginPersonalKcalJobRun() bool {
	s.personalKcalRunMu.Lock()
	defer s.personalKcalRunMu.Unlock()
	if s.personalKcalJobActive {
		return false
	}
	s.personalKcalJobActive = true
	return true
}

func (s *Service) endPersonalKcalJobRun() {
	s.personalKcalRunMu.Lock()
	defer s.personalKcalRunMu.Unlock()
	s.personalKcalJobActive = false
}

// processUserPersonalKcal ports runGraph2MonthlyGA (plans/research/norm-comparison/algorithms.js)
// to Go. It walks every calendar month from the user's first diary/weight date up to the last
// fully completed month, replaying §3.3-3.4 to advance the running "applied" state, and runs the
// GA (§3.5) only for months that have no stored fit yet — idempotent by construction.
func (s *Service) processUserPersonalKcal(ctx context.Context, userID int64) (int, error) {
	firstDate, err := s.repo.GetUserFirstDate(ctx, userID)
	if err != nil {
		return 0, err
	}
	if firstDate == "" {
		return 0, nil
	}

	boundaryEnd := lastCompletedMonthEndDate(s.clock.Now().UTC())
	if firstDate > boundaryEnd {
		return 0, nil
	}

	cfg := s.personalKcalConfig

	diaryRows, err := s.repo.GetAllDiaryEntries(ctx, userID)
	if err != nil {
		return 0, err
	}
	weightRows, err := s.repo.GetAllWeightEntries(ctx, userID)
	if err != nil {
		return 0, err
	}
	catalogueRows, err := s.repo.GetCatalogue(ctx)
	if err != nil {
		return 0, err
	}
	kcalHistoryRows, err := s.repo.GetPersonalKcalHistory(ctx, userID)
	if err != nil {
		return 0, err
	}
	normHistoryRows, err := s.repo.GetPersonalNormHistory(ctx, userID)
	if err != nil {
		return 0, err
	}

	catalogueKcals := make(map[int64]float64, len(catalogueRows))
	for _, row := range catalogueRows {
		catalogueKcals[row.ID] = float64(row.Kcals)
	}

	rawKcal := make(map[int64]map[string]float64)
	for _, row := range kcalHistoryRows {
		if rawKcal[row.FoodCatalogueID] == nil {
			rawKcal[row.FoodCatalogueID] = make(map[string]float64)
		}
		rawKcal[row.FoodCatalogueID][row.YearMonth] = row.KcalsPer100g
	}
	rawNorm := make(map[string]float64)
	rawX := make(map[string]float64)
	for _, row := range normHistoryRows {
		rawNorm[row.YearMonth] = row.NormKcals
		rawX[row.YearMonth] = row.KcalPerKg
	}

	allDates := getDatesList(firstDate, boundaryEnd)
	weights := prepareWeights(weightRows, allDates)
	smoothWeight := calculateCenteredAverage(weights, 5, false, 0)

	diaryByDay := make(map[string][]personalKcalDiaryEntry, len(allDates))
	for _, row := range diaryRows {
		if row.DateISO < firstDate || row.DateISO > boundaryEnd {
			continue
		}
		diaryByDay[row.DateISO] = append(diaryByDay[row.DateISO], personalKcalDiaryEntry{catalogueID: row.FoodCatalogueID, grams: float64(row.FoodWeight)})
	}

	months := splitDatesIntoCalendarMonths(allDates)

	appliedKcal := make(map[int64]float64)
	appliedNorm := float64(defaultPersonalNormKcals)
	appliedX := float64(kcalsIn1KG)
	monthsComputed := 0
	rng := rand.New(rand.NewSource(s.clock.Now().UnixNano() + userID))
	createdAt := s.clock.Now().UTC().Format(time.RFC3339)

	for idx, month := range months {
		gramsByCat := make(map[int64]float64)
		loggedCount := 0
		for _, date := range month.dates {
			entries := diaryByDay[date]
			if len(entries) == 0 {
				continue
			}
			loggedCount++
			for _, entry := range entries {
				gramsByCat[entry.catalogueID] += entry.grams
			}
		}
		coverage := float64(loggedCount) / float64(len(month.dates))
		touchedIDs := sortedPersonalKcalCatalogueIDs(gramsByCat)

		seedKcal := make(map[int64]float64, len(touchedIDs))
		kcalByCat := make(map[int64]float64, len(touchedIDs))
		for _, id := range touchedIDs {
			productRaw := rawKcal[id]
			prevApplied, hasPrev := appliedKcal[id]
			bootstrap := prevApplied
			if !hasPrev {
				bootstrap = catalogueKcals[id]
			}
			kcalByCat[id] = gramsByCat[id] * bootstrap / 100
			rawSeed := decayedAverage(idx, cfg.LookbackMonths, cfg.DecayRate, bootstrap, func(j int) (float64, bool) {
				value, ok := productRaw[months[j].yearMonth]
				return value, ok
			})
			if hasPrev {
				seedKcal[id] = applyMonthlyCap(prevApplied, rawSeed, cfg.MaxMonthlyChangePercent)
			} else {
				seedKcal[id] = rawSeed
			}
			appliedKcal[id] = seedKcal[id]
		}
		appliedNorm = decayedAverage(idx, cfg.LookbackMonths, cfg.DecayRate, appliedNorm, func(j int) (float64, bool) {
			value, ok := rawNorm[months[j].yearMonth]
			return value, ok
		})
		appliedX = decayedAverage(idx, cfg.LookbackMonths, cfg.DecayRate, appliedX, func(j int) (float64, bool) {
			value, ok := rawX[months[j].yearMonth]
			return value, ok
		})

		if _, computed := rawNorm[month.yearMonth]; computed {
			continue
		}
		if coverage < cfg.CoverageThreshold || len(touchedIDs) == 0 {
			continue
		}

		seedGenome := personalKcalGenome{kcal100: cloneFloatByIDMap(seedKcal), norm: appliedNorm, x: appliedX}
		best := runPersonalKcalMonthGA(seedGenome, month, diaryByDay, smoothWeight, seedKcal, kcalByCat, touchedIDs, cfg, rng)

		for _, id := range touchedIDs {
			if err := s.repo.InsertPersonalKcalHistory(ctx, userID, id, month.yearMonth, best.kcal100[id], createdAt); err != nil {
				return monthsComputed, err
			}
			if rawKcal[id] == nil {
				rawKcal[id] = make(map[string]float64)
			}
			rawKcal[id][month.yearMonth] = best.kcal100[id]
		}
		if err := s.repo.InsertPersonalNormHistory(ctx, userID, month.yearMonth, best.norm, best.x, createdAt); err != nil {
			return monthsComputed, err
		}
		rawNorm[month.yearMonth] = best.norm
		rawX[month.yearMonth] = best.x
		monthsComputed++
	}

	return monthsComputed, nil
}

func runPersonalKcalMonthGA(seed personalKcalGenome, month personalKcalMonthBucket, diaryByDay map[string][]personalKcalDiaryEntry, smoothWeight map[string]float64, seedKcal map[int64]float64, kcalByCat map[int64]float64, touchedIDs []int64, cfg PersonalKcalConfig, rng *rand.Rand) personalKcalGenome {
	fitnessOf := func(genome personalKcalGenome) float64 {
		return personalKcalFitness(genome, month, diaryByDay, smoothWeight, seedKcal, kcalByCat, touchedIDs, cfg)
	}

	population := make([]personalKcalGenome, cfg.Population)
	population[0] = seed
	for i := 1; i < cfg.Population; i++ {
		population[i] = mutatePersonalKcalGenome(population[0], touchedIDs, kcalByCat, cfg, rng)
	}

	const childrenPerParent = 4
	parents := int(math.Round(float64(cfg.Population) / 4))
	parents = max(parents, 1)

	type scoredGenome struct {
		genome personalKcalGenome
		score  float64
	}

	best := population[0]
	bestScore := fitnessOf(population[0])
	stale := 0
	for gen := 0; gen < cfg.MaxGenerations && stale < cfg.MaxStale; gen++ {
		scored := make([]scoredGenome, len(population))
		for i, genome := range population {
			scored[i] = scoredGenome{genome: genome, score: fitnessOf(genome)}
		}
		slices.SortFunc(scored, func(a, b scoredGenome) int { return cmp.Compare(a.score, b.score) })

		if scored[0].score < bestScore-1e-9 {
			bestScore = scored[0].score
			best = scored[0].genome
			stale = 0
		} else {
			stale++
		}

		limit := min(parents, len(scored))
		next := make([]personalKcalGenome, 0, limit*childrenPerParent)
		for p := range limit {
			next = append(next, scored[p].genome)
			for c := 1; c < childrenPerParent; c++ {
				next = append(next, mutatePersonalKcalGenome(scored[p].genome, touchedIDs, kcalByCat, cfg, rng))
			}
		}
		population = next
	}

	return best
}

// personalKcalFitness mirrors algorithms.js fitness(): a data term tracking how well the
// genome's day-by-day surplus explains the smoothed weight trajectory this month, plus a soft
// anchor pulling each product's kcal100 back toward its seed, weighted by how little evidence
// (calorie exposure) this month provides for that product.
func personalKcalFitness(genome personalKcalGenome, month personalKcalMonthBucket, diaryByDay map[string][]personalKcalDiaryEntry, smoothWeight map[string]float64, seedKcal map[int64]float64, kcalByCat map[int64]float64, touchedIDs []int64, cfg PersonalKcalConfig) float64 {
	var cumSurplus, dataTerm float64
	w0 := smoothWeight[month.dates[0]]
	for _, date := range month.dates {
		entries := diaryByDay[date]
		if len(entries) > 0 {
			var intake float64
			for _, entry := range entries {
				intake += genome.kcal100[entry.catalogueID] * entry.grams / 100
			}
			cumSurplus += intake - genome.norm
		}
		predicted := cumSurplus / genome.x
		actual := smoothWeight[date] - w0
		diff := predicted - actual
		dataTerm += diff * diff
	}

	var anchorTerm float64
	for _, id := range touchedIDs {
		pull := cfg.AnchorLambda * (1 - personalKcalConfidence(kcalByCat[id], cfg.EvidenceHalfKcal))
		logDeviation := math.Log(genome.kcal100[id] / seedKcal[id])
		anchorTerm += pull * logDeviation * logDeviation
	}

	return dataTerm + anchorTerm
}

func mutatePersonalKcalGenome(parent personalKcalGenome, touchedIDs []int64, kcalByCat map[int64]float64, cfg PersonalKcalConfig, rng *rand.Rand) personalKcalGenome {
	kcal100 := make(map[int64]float64, len(touchedIDs))
	for _, id := range touchedIDs {
		logStep := cfg.CoefLogStep * personalKcalConfidence(kcalByCat[id], cfg.EvidenceHalfKcal)
		kcal100[id] = parent.kcal100[id] * math.Exp((rng.Float64()*2-1)*logStep)
	}
	return personalKcalGenome{
		kcal100: kcal100,
		norm:    parent.norm + (rng.Float64()*2-1)*cfg.NormStep,
		x:       parent.x + (rng.Float64()*2-1)*cfg.XStep,
	}
}

func personalKcalConfidence(kcal float64, evidenceHalfKcal float64) float64 {
	return kcal / (kcal + evidenceHalfKcal)
}

func lastCompletedMonthEndDate(now time.Time) string {
	firstOfCurrentMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return firstOfCurrentMonth.AddDate(0, 0, -1).Format("2006-01-02")
}

func splitDatesIntoCalendarMonths(allDates []string) []personalKcalMonthBucket {
	var months []personalKcalMonthBucket
	for _, date := range allDates {
		yearMonth := date[:7]
		if len(months) == 0 || months[len(months)-1].yearMonth != yearMonth {
			months = append(months, personalKcalMonthBucket{yearMonth: yearMonth})
		}
		months[len(months)-1].dates = append(months[len(months)-1].dates, date)
	}
	return months
}

func sortedPersonalKcalCatalogueIDs(input map[int64]float64) []int64 {
	result := make([]int64, 0, len(input))
	for id := range input {
		result = append(result, id)
	}
	slices.Sort(result)
	return result
}

func cloneFloatByIDMap(input map[int64]float64) map[int64]float64 {
	result := make(map[int64]float64, len(input))
	maps.Copy(result, input)
	return result
}
