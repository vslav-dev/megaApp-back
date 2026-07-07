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

type Service struct {
	repo  *Repository
	clock platformclock.Clock

	// mu serializes create/update-time Entry: the overlap check and the
	// write must never interleave across two concurrent requests, see
	// implementation plan ("Синхронизация с сервером"/«Сервис — инварианты»).
	// Update-selection doesn't touch the interval, so it doesn't need it.
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

func (s *Service) now() string {
	return s.clock.Now().UTC().Format(time.RFC3339)
}

// ---------- Catalog ----------

func (s *Service) GetCatalog(ctx context.Context, userID int64) (Catalog, error) {
	return s.repo.GetCatalog(ctx, userID)
}

// ---------- ActivityKind ----------

func (s *Service) CreateActivityKind(ctx context.Context, userID int64, input ActivityKindInput) (int64, error) {
	normalized, err := validateActivityKindInput(input)
	if err != nil {
		return 0, err
	}
	if err := s.ensureGroupsExist(ctx, userID, bindingGroupIDs(normalized.GroupBindings)); err != nil {
		return 0, err
	}
	return s.repo.CreateActivityKind(ctx, userID, normalized, s.now())
}

func (s *Service) UpdateActivityKind(ctx context.Context, userID int64, id int64, input ActivityKindInput) error {
	existing, err := s.repo.GetActivityKindByID(ctx, userID, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return legacy.NewError(legacy.ErrorKindNotFound, "ActivityKind not found")
	}

	normalized, err := validateActivityKindInput(input)
	if err != nil {
		return err
	}
	if err := s.ensureGroupsExist(ctx, userID, bindingGroupIDs(normalized.GroupBindings)); err != nil {
		return err
	}
	return s.repo.UpdateActivityKind(ctx, userID, id, normalized, s.now())
}

// ---------- CategoryGroup ----------

func (s *Service) CreateCategoryGroup(ctx context.Context, userID int64, input CategoryGroupInput) (int64, error) {
	normalized, err := validateCategoryGroupInput(input)
	if err != nil {
		return 0, err
	}
	if err := s.ensureAreaUniqueness(ctx, userID, normalized, nil); err != nil {
		return 0, err
	}
	return s.repo.CreateCategoryGroup(ctx, userID, normalized, s.now())
}

func (s *Service) UpdateCategoryGroup(ctx context.Context, userID int64, id int64, input CategoryGroupInput) error {
	existing, err := s.repo.GetCategoryGroupByID(ctx, userID, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return legacy.NewError(legacy.ErrorKindNotFound, "CategoryGroup not found")
	}

	normalized, err := validateCategoryGroupInput(input)
	if err != nil {
		return err
	}
	if err := s.ensureAreaUniqueness(ctx, userID, normalized, &id); err != nil {
		return err
	}

	// Archiving (transition false -> true) is blocked while the group is
	// still bound to a non-archived ActivityKind — the same rule DeleteCategoryGroup
	// enforces, since archiving is the softer half of the same "can't lose the
	// applicability source" invariant. Re-saving an already-archived group, or
	// editing a still-active group without archiving it, is unaffected.
	if normalized.IsArchived && !existing.IsArchived {
		if err := s.ensureGroupNotBoundToActiveKind(ctx, userID, id); err != nil {
			return err
		}
	}

	return s.repo.UpdateCategoryGroup(ctx, userID, id, normalized, s.now())
}

func (s *Service) DeleteCategoryGroup(ctx context.Context, userID int64, id int64) error {
	existing, err := s.repo.GetCategoryGroupByID(ctx, userID, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return legacy.NewError(legacy.ErrorKindNotFound, "CategoryGroup not found")
	}

	optionsCount, err := s.repo.CountOptionsInGroup(ctx, id)
	if err != nil {
		return err
	}
	if optionsCount > 0 {
		return legacy.NewError(legacy.ErrorKindConflict, "CategoryGroup has options, remove or reassign them first")
	}
	if err := s.ensureGroupNotBoundToActiveKind(ctx, userID, id); err != nil {
		return err
	}

	return s.repo.DeleteCategoryGroup(ctx, userID, id)
}

func (s *Service) ensureAreaUniqueness(ctx context.Context, userID int64, input CategoryGroupInput, excludeID *int64) error {
	if input.Kind == nil || *input.Kind != CategoryGroupKindArea || input.IsArchived {
		return nil
	}
	count, err := s.repo.CountNonArchivedAreaGroups(ctx, userID, excludeID)
	if err != nil {
		return err
	}
	if count > 0 {
		return legacy.NewError(legacy.ErrorKindConflict, "Only one non-archived kind='area' group is allowed per user")
	}
	return nil
}

func (s *Service) ensureGroupNotBoundToActiveKind(ctx context.Context, userID int64, groupID int64) error {
	kinds, err := s.repo.ListActivityKinds(ctx, userID)
	if err != nil {
		return err
	}
	for _, kind := range kinds {
		if kind.IsArchived {
			continue
		}
		for _, binding := range kind.GroupBindings {
			if binding.GroupID == groupID {
				return legacy.NewError(legacy.ErrorKindConflict, fmt.Sprintf("CategoryGroup is bound to active ActivityKind %q, unbind it first", kind.Name))
			}
		}
	}
	return nil
}

// ---------- CategoryOption ----------

func (s *Service) CreateCategoryOption(ctx context.Context, userID int64, input CategoryOptionInput) (int64, error) {
	normalized, err := validateCategoryOptionInput(input)
	if err != nil {
		return 0, err
	}
	if err := s.ensureGroupExists(ctx, userID, normalized.GroupID); err != nil {
		return 0, err
	}
	return s.repo.CreateCategoryOption(ctx, userID, normalized, s.now())
}

func (s *Service) UpdateCategoryOption(ctx context.Context, userID int64, id int64, input CategoryOptionInput) error {
	existing, err := s.repo.GetCategoryOptionByID(ctx, userID, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return legacy.NewError(legacy.ErrorKindNotFound, "CategoryOption not found")
	}

	normalized, err := validateCategoryOptionInput(input)
	if err != nil {
		return err
	}
	if err := s.ensureGroupExists(ctx, userID, normalized.GroupID); err != nil {
		return err
	}
	return s.repo.UpdateCategoryOption(ctx, userID, id, normalized, s.now())
}

func (s *Service) DeleteCategoryOption(ctx context.Context, userID int64, id int64) error {
	existing, err := s.repo.GetCategoryOptionByID(ctx, userID, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return legacy.NewError(legacy.ErrorKindNotFound, "CategoryOption not found")
	}

	usedCount, err := s.repo.CountEntryOptionsForOption(ctx, id)
	if err != nil {
		return err
	}
	if usedCount > 0 {
		return legacy.NewError(legacy.ErrorKindConflict, "CategoryOption is used by existing entries")
	}

	return s.repo.DeleteCategoryOption(ctx, userID, id)
}

func (s *Service) ensureGroupExists(ctx context.Context, userID int64, groupID int64) error {
	group, err := s.repo.GetCategoryGroupByID(ctx, userID, groupID)
	if err != nil {
		return err
	}
	if group == nil {
		return legacy.NewError(legacy.ErrorKindValidation, fmt.Sprintf("CategoryGroup %d not found", groupID))
	}
	return nil
}

func (s *Service) ensureGroupsExist(ctx context.Context, userID int64, groupIDs []int64) error {
	for _, groupID := range groupIDs {
		if err := s.ensureGroupExists(ctx, userID, groupID); err != nil {
			return err
		}
	}
	return nil
}

// ---------- Entry ----------

func (s *Service) GetEntries(ctx context.Context, userID int64, start *string) ([]Entry, error) {
	return s.repo.ListEntries(ctx, userID, start)
}

func (s *Service) CreateEntry(ctx context.Context, userID int64, input EntryInput) (int64, error) {
	normalizedTime, err := validateEntryTime(input.Track, input.StartAt, input.EndAt)
	if err != nil {
		return 0, err
	}
	normalizedOptions, err := s.validateEntrySelection(ctx, userID, input.ActivityKindID, input.Options)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id, err := s.repo.CreateEntry(ctx, userID, EntryInput{
		ActivityKindID: input.ActivityKindID,
		Track:          normalizedTime.Track,
		StartAt:        normalizedTime.StartAt,
		EndAt:          normalizedTime.EndAt,
		Options:        normalizedOptions,
	}, s.now())
	if err != nil {
		return 0, translateEntryWriteError(err)
	}
	return id, nil
}

func (s *Service) UpdateEntryTime(ctx context.Context, userID int64, entryID int64, input EntryTimeInput) error {
	normalizedTime, err := validateEntryTime(input.Track, input.StartAt, input.EndAt)
	if err != nil {
		return err
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

	if err := s.repo.UpdateEntryTime(ctx, userID, entryID, EntryTimeInput{
		Track:   normalizedTime.Track,
		StartAt: normalizedTime.StartAt,
		EndAt:   normalizedTime.EndAt,
	}, s.now()); err != nil {
		return translateEntryWriteError(err)
	}
	return nil
}

func (s *Service) UpdateEntrySelection(ctx context.Context, userID int64, entryID int64, input EntrySelectionInput) error {
	normalizedOptions, err := s.validateEntrySelection(ctx, userID, input.ActivityKindID, input.Options)
	if err != nil {
		return err
	}

	existing, err := s.repo.GetEntryByID(ctx, userID, entryID)
	if err != nil {
		return err
	}
	if existing == nil {
		return legacy.NewError(legacy.ErrorKindNotFound, "Entry not found")
	}

	return s.repo.UpdateEntrySelection(ctx, userID, entryID, EntrySelectionInput{
		ActivityKindID: input.ActivityKindID,
		Options:        normalizedOptions,
	}, s.now())
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

// validateEntrySelection enforces every semantic invariant from the plan:
// kind exists/not archived, groups applicable to the kind, options belong to
// their group, single option per group, required groups filled, nothing
// archived, nothing belonging to another user.
func (s *Service) validateEntrySelection(ctx context.Context, userID int64, activityKindID int64, options []EntryOption) ([]EntryOption, error) {
	kind, err := s.repo.GetActivityKindByID(ctx, userID, activityKindID)
	if err != nil {
		return nil, err
	}
	if kind == nil {
		return nil, legacy.NewError(legacy.ErrorKindValidation, "ActivityKind not found")
	}
	if kind.IsArchived {
		return nil, legacy.NewError(legacy.ErrorKindValidation, "ActivityKind is archived")
	}

	requiredByGroup := make(map[int64]bool, len(kind.GroupBindings))
	for _, binding := range kind.GroupBindings {
		requiredByGroup[binding.GroupID] = binding.Required
	}

	seenGroups := make(map[int64]struct{}, len(options))
	normalized := make([]EntryOption, 0, len(options))
	for _, option := range options {
		if _, ok := requiredByGroup[option.GroupID]; !ok {
			return nil, legacy.NewError(legacy.ErrorKindValidation, fmt.Sprintf("Group %d is not applicable to this ActivityKind", option.GroupID))
		}
		if _, duplicate := seenGroups[option.GroupID]; duplicate {
			return nil, legacy.NewError(legacy.ErrorKindValidation, fmt.Sprintf("Group %d has more than one selected option", option.GroupID))
		}
		seenGroups[option.GroupID] = struct{}{}

		group, err := s.repo.GetCategoryGroupByID(ctx, userID, option.GroupID)
		if err != nil {
			return nil, err
		}
		if group == nil {
			return nil, legacy.NewError(legacy.ErrorKindValidation, fmt.Sprintf("CategoryGroup %d not found", option.GroupID))
		}
		if group.IsArchived {
			return nil, legacy.NewError(legacy.ErrorKindValidation, fmt.Sprintf("CategoryGroup %q is archived", group.Name))
		}

		categoryOption, err := s.repo.GetCategoryOptionByID(ctx, userID, option.OptionID)
		if err != nil {
			return nil, err
		}
		if categoryOption == nil {
			return nil, legacy.NewError(legacy.ErrorKindValidation, fmt.Sprintf("CategoryOption %d not found", option.OptionID))
		}
		if categoryOption.GroupID != option.GroupID {
			return nil, legacy.NewError(legacy.ErrorKindValidation, fmt.Sprintf("CategoryOption %d does not belong to group %d", option.OptionID, option.GroupID))
		}
		if categoryOption.IsArchived {
			return nil, legacy.NewError(legacy.ErrorKindValidation, fmt.Sprintf("CategoryOption %q is archived", categoryOption.Name))
		}

		normalized = append(normalized, option)
	}

	for groupID, required := range requiredByGroup {
		if !required {
			continue
		}
		if _, ok := seenGroups[groupID]; !ok {
			return nil, legacy.NewError(legacy.ErrorKindValidation, fmt.Sprintf("Required group %d is missing a selected option", groupID))
		}
	}

	return normalized, nil
}

func translateEntryWriteError(err error) error {
	if errors.Is(err, ErrOverlap) {
		return legacy.NewError(legacy.ErrorKindConflict, "Entry overlaps an existing entry on the same track")
	}
	return err
}

type normalizedEntryTime struct {
	Track   Track
	StartAt string
	EndAt   string
}

func validateEntryTime(track Track, startAt string, endAt string) (normalizedEntryTime, error) {
	var missing []string
	if startAt == "" {
		missing = append(missing, "startAt")
	}
	if endAt == "" {
		missing = append(missing, "endAt")
	}
	if len(missing) > 0 {
		return normalizedEntryTime{}, missingFieldsError(missing...)
	}
	if !isValidTrack(track) {
		return normalizedEntryTime{}, legacy.NewError(legacy.ErrorKindValidation, "track must be one of: primary, secondary")
	}
	if endAt <= startAt {
		return normalizedEntryTime{}, legacy.NewError(legacy.ErrorKindValidation, "endAt must be after startAt")
	}
	return normalizedEntryTime{Track: track, StartAt: startAt, EndAt: endAt}, nil
}

func validateActivityKindInput(input ActivityKindInput) (ActivityKindInput, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return ActivityKindInput{}, missingFieldsError("name")
	}
	bindings, err := normalizeGroupBindings(input.GroupBindings)
	if err != nil {
		return ActivityKindInput{}, err
	}
	return ActivityKindInput{
		Name:          name,
		GroupBindings: bindings,
		IsArchived:    input.IsArchived,
	}, nil
}

func normalizeGroupBindings(bindings []GroupBinding) ([]GroupBinding, error) {
	seen := make(map[int64]struct{}, len(bindings))
	result := make([]GroupBinding, 0, len(bindings))
	for _, binding := range bindings {
		if binding.GroupID <= 0 {
			continue
		}
		if _, ok := seen[binding.GroupID]; ok {
			return nil, legacy.NewError(legacy.ErrorKindValidation, fmt.Sprintf("Group %d is bound more than once", binding.GroupID))
		}
		seen[binding.GroupID] = struct{}{}
		result = append(result, binding)
	}
	return result, nil
}

func bindingGroupIDs(bindings []GroupBinding) []int64 {
	result := make([]int64, 0, len(bindings))
	for _, binding := range bindings {
		result = append(result, binding.GroupID)
	}
	return result
}

func validateCategoryGroupInput(input CategoryGroupInput) (CategoryGroupInput, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return CategoryGroupInput{}, missingFieldsError("name")
	}
	kind := trimmedOrNil(input.Kind)
	if kind != nil && *kind != CategoryGroupKindArea {
		return CategoryGroupInput{}, legacy.NewError(legacy.ErrorKindValidation, "kind must be null or 'area'")
	}
	return CategoryGroupInput{
		Name:       name,
		Kind:       kind,
		IsArchived: input.IsArchived,
	}, nil
}

func validateCategoryOptionInput(input CategoryOptionInput) (CategoryOptionInput, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return CategoryOptionInput{}, missingFieldsError("name")
	}
	if input.GroupID <= 0 {
		return CategoryOptionInput{}, missingFieldsError("groupId")
	}
	return CategoryOptionInput{
		GroupID:    input.GroupID,
		Name:       name,
		Color:      trimmedOrNil(input.Color),
		IsArchived: input.IsArchived,
	}, nil
}

func isValidTrack(track Track) bool {
	switch track {
	case TrackPrimary, TrackSecondary:
		return true
	default:
		return false
	}
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
