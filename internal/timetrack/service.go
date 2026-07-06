package timetrack

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"megaapp-back/internal/httpx/legacy"
	platformclock "megaapp-back/internal/platform/clock"
)

const defaultRecentActivitiesLimit = 10

type Service struct {
	repo  *Repository
	clock platformclock.Clock

	// mu serializes create/update Entry: the overlap check and the write
	// must never interleave across two concurrent requests, see
	// implementation plan ("Синхронизация с сервером"/«Сервис — инварианты»).
	mu sync.Mutex
}

func NewService(repo *Repository) *Service {
	return NewServiceWithClock(repo, platformclock.NewRealClock())
}

func NewServiceWithClock(repo *Repository, appClock platformclock.Clock) *Service {
	if appClock == nil {
		appClock = platformclock.NewRealClock()
	}
	return &Service{repo: repo, clock: appClock}
}

func (s *Service) GetActivities(ctx context.Context, userID int64, isArchivedFilter *bool) ([]Activity, error) {
	activities, err := s.repo.ListActivities(ctx, userID)
	if err != nil {
		return nil, err
	}
	if isArchivedFilter == nil {
		return activities, nil
	}
	filtered := make([]Activity, 0, len(activities))
	for _, activity := range activities {
		if activity.IsArchived == *isArchivedFilter {
			filtered = append(filtered, activity)
		}
	}
	return filtered, nil
}

func (s *Service) CreateActivity(ctx context.Context, userID int64, input ActivityInput) (int64, error) {
	normalized, err := validateActivityInput(input)
	if err != nil {
		return 0, err
	}
	if err := s.ensureCategoriesExist(ctx, userID, normalized.CategoryIDs); err != nil {
		return 0, err
	}
	return s.repo.CreateActivity(ctx, userID, normalized, s.clock.Now().UTC().Format(time.RFC3339))
}

func (s *Service) UpdateActivity(ctx context.Context, userID int64, activityID int64, input ActivityInput) error {
	existing, err := s.repo.GetActivityByID(ctx, userID, activityID)
	if err != nil {
		return err
	}
	if existing == nil {
		return legacy.NewError(legacy.ErrorKindNotFound, "Activity not found")
	}

	normalized, err := validateActivityInput(input)
	if err != nil {
		return err
	}
	if err := s.ensureCategoriesExist(ctx, userID, normalized.CategoryIDs); err != nil {
		return err
	}
	return s.repo.UpdateActivity(ctx, userID, activityID, normalized)
}

func (s *Service) GetCategories(ctx context.Context, userID int64) ([]Category, error) {
	return s.repo.ListCategories(ctx, userID)
}

func (s *Service) CreateCategory(ctx context.Context, userID int64, input CategoryInput) (int64, error) {
	normalized, err := validateCategoryInput(input)
	if err != nil {
		return 0, err
	}
	return s.repo.CreateCategory(ctx, userID, normalized, s.clock.Now().UTC().Format(time.RFC3339))
}

func (s *Service) UpdateCategory(ctx context.Context, userID int64, categoryID int64, input CategoryInput) error {
	existing, err := s.repo.GetCategoryByID(ctx, userID, categoryID)
	if err != nil {
		return err
	}
	if existing == nil {
		return legacy.NewError(legacy.ErrorKindNotFound, "Category not found")
	}

	normalized, err := validateCategoryInput(input)
	if err != nil {
		return err
	}
	return s.repo.UpdateCategory(ctx, userID, categoryID, normalized)
}

func (s *Service) DeleteCategory(ctx context.Context, userID int64, categoryID int64) error {
	existing, err := s.repo.GetCategoryByID(ctx, userID, categoryID)
	if err != nil {
		return err
	}
	if existing == nil {
		return legacy.NewError(legacy.ErrorKindNotFound, "Category not found")
	}
	// Deleting a Category is always safe — no Entry references it directly,
	// only the timeActivityCategory join table does, and that cascades.
	return s.repo.DeleteCategory(ctx, userID, categoryID)
}

func (s *Service) GetEntries(ctx context.Context, userID int64, start *string) ([]Entry, error) {
	return s.repo.ListEntries(ctx, userID, start)
}

func (s *Service) CreateEntry(ctx context.Context, userID int64, input EntryInput) (int64, error) {
	normalized, err := validateEntryInput(input)
	if err != nil {
		return 0, err
	}
	activity, err := s.repo.GetActivityByID(ctx, userID, normalized.ActivityID)
	if err != nil {
		return 0, err
	}
	if activity == nil {
		return 0, legacy.NewError(legacy.ErrorKindValidation, "Activity not found")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id, err := s.repo.CreateEntry(ctx, userID, normalized, s.clock.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, translateEntryWriteError(err)
	}
	return id, nil
}

func (s *Service) UpdateEntry(ctx context.Context, userID int64, entryID int64, input EntryInput) error {
	normalized, err := validateEntryInput(input)
	if err != nil {
		return err
	}
	activity, err := s.repo.GetActivityByID(ctx, userID, normalized.ActivityID)
	if err != nil {
		return err
	}
	if activity == nil {
		return legacy.NewError(legacy.ErrorKindValidation, "Activity not found")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, err := s.repo.GetEntryByID(ctx, userID, entryID)
	if err != nil {
		return err
	}
	if existing == nil {
		return legacy.NewError(legacy.ErrorKindNotFound, "Entry not found")
	}

	if err := s.repo.UpdateEntry(ctx, userID, entryID, normalized, s.clock.Now().UTC().Format(time.RFC3339)); err != nil {
		return translateEntryWriteError(err)
	}
	return nil
}

func (s *Service) DeleteEntry(ctx context.Context, userID int64, entryID int64) error {
	existing, err := s.repo.GetEntryByID(ctx, userID, entryID)
	if err != nil {
		return err
	}
	if existing == nil {
		return legacy.NewError(legacy.ErrorKindNotFound, "Entry not found")
	}
	return s.repo.DeleteEntry(ctx, userID, entryID)
}

func (s *Service) GetRecentActivityIDs(ctx context.Context, userID int64, track *Track, limit int) ([]int64, error) {
	if track != nil && !isValidTrack(*track) {
		return nil, legacy.NewError(legacy.ErrorKindValidation, "track must be one of: primary, secondary")
	}
	if limit <= 0 {
		limit = defaultRecentActivitiesLimit
	}
	return s.repo.ListRecentActivityIDs(ctx, userID, track, limit)
}

func (s *Service) ensureCategoriesExist(ctx context.Context, userID int64, categoryIDs []int64) error {
	for _, categoryID := range categoryIDs {
		category, err := s.repo.GetCategoryByID(ctx, userID, categoryID)
		if err != nil {
			return err
		}
		if category == nil {
			return legacy.NewError(legacy.ErrorKindValidation, fmt.Sprintf("Category %d not found", categoryID))
		}
	}
	return nil
}

func translateEntryWriteError(err error) error {
	if errors.Is(err, ErrOverlap) {
		return legacy.NewError(legacy.ErrorKindConflict, "Entry overlaps an existing entry on the same track")
	}
	return err
}

func validateActivityInput(input ActivityInput) (ActivityInput, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return ActivityInput{}, missingFieldsError("name")
	}
	return ActivityInput{
		Name:        name,
		IsArchived:  input.IsArchived,
		CategoryIDs: normalizeCategoryIDs(input.CategoryIDs),
	}, nil
}

func validateCategoryInput(input CategoryInput) (CategoryInput, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return CategoryInput{}, missingFieldsError("name")
	}
	if input.Impact != nil && !isValidImpact(*input.Impact) {
		return CategoryInput{}, legacy.NewError(legacy.ErrorKindValidation, "impact must be one of: useful, neutral, wasteful")
	}
	return CategoryInput{
		Name:   name,
		Kind:   trimmedOrNil(input.Kind),
		Color:  trimmedOrNil(input.Color),
		Impact: input.Impact,
	}, nil
}

func validateEntryInput(input EntryInput) (EntryInput, error) {
	var missing []string
	if input.ActivityID <= 0 {
		missing = append(missing, "activityId")
	}
	if input.StartAt == "" {
		missing = append(missing, "startAt")
	}
	if input.EndAt == "" {
		missing = append(missing, "endAt")
	}
	if len(missing) > 0 {
		return EntryInput{}, missingFieldsError(missing...)
	}
	if !isValidTrack(input.Track) {
		return EntryInput{}, legacy.NewError(legacy.ErrorKindValidation, "track must be one of: primary, secondary")
	}
	if input.EndAt <= input.StartAt {
		return EntryInput{}, legacy.NewError(legacy.ErrorKindValidation, "endAt must be after startAt")
	}
	return input, nil
}

func isValidTrack(track Track) bool {
	switch track {
	case TrackPrimary, TrackSecondary:
		return true
	default:
		return false
	}
}

func isValidImpact(impact string) bool {
	switch Impact(impact) {
	case ImpactUseful, ImpactNeutral, ImpactWasteful:
		return true
	default:
		return false
	}
}

func normalizeCategoryIDs(categoryIDs []int64) []int64 {
	if len(categoryIDs) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(categoryIDs))
	result := make([]int64, 0, len(categoryIDs))
	for _, id := range categoryIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func trimmedOrNil(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func missingFieldsError(fields ...string) error {
	return legacy.NewError(legacy.ErrorKindValidation, fmt.Sprintf("Missing required fields: %s", strings.Join(fields, ", ")))
}
