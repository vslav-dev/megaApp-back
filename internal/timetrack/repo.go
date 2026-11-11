package timetrack

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"megaapp-back/internal/platform/sqlite"
)

var ErrOverlap = errors.New("time entry overlaps an existing entry on the same track")

type Repository struct {
	db    *sql.DB
	write sqlite.WriteDB
}

func NewRepository(read *sql.DB, write sqlite.WriteDB) *Repository {
	return &Repository{db: read, write: write}
}

// ---------- ActivityKind ----------

func (r *Repository) ListActivityKinds(ctx context.Context, userID int64) ([]ActivityKind, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, groupBindingsJson, isArchived, createdAt, updatedAt
		FROM timeActivityKind
		WHERE userId = ?
		ORDER BY name ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list activity kinds: %w", err)
	}
	defer rows.Close()

	result := make([]ActivityKind, 0)
	for rows.Next() {
		item, err := scanActivityKind(rows)
		if err != nil {
			return nil, fmt.Errorf("scan activity kind: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate activity kinds: %w", err)
	}
	return result, nil
}

func (r *Repository) GetActivityKindByID(ctx context.Context, userID int64, id int64) (*ActivityKind, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, groupBindingsJson, isArchived, createdAt, updatedAt
		FROM timeActivityKind
		WHERE id = ? AND userId = ?
	`, id, userID)

	item, err := scanActivityKind(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get activity kind: %w", err)
	}
	return &item, nil
}

func (r *Repository) CreateActivityKind(ctx context.Context, userID int64, input ActivityKindInput, now string) (int64, error) {
	bindingsJSON, err := marshalGroupBindings(input.GroupBindings)
	if err != nil {
		return 0, err
	}
	result, err := r.write.ExecContext(ctx, `
		INSERT INTO timeActivityKind (userId, name, groupBindingsJson, isArchived, createdAt, updatedAt)
		VALUES (?, ?, ?, ?, ?, ?)
	`, userID, input.Name, bindingsJSON, boolToInt64(input.IsArchived), now, now)
	if err != nil {
		return 0, fmt.Errorf("create activity kind: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("activity kind last insert id: %w", err)
	}
	return id, nil
}

func (r *Repository) UpdateActivityKind(ctx context.Context, userID int64, id int64, input ActivityKindInput, now string) error {
	bindingsJSON, err := marshalGroupBindings(input.GroupBindings)
	if err != nil {
		return err
	}
	result, err := r.write.ExecContext(ctx, `
		UPDATE timeActivityKind
		SET name = ?, groupBindingsJson = ?, isArchived = ?, updatedAt = ?
		WHERE id = ? AND userId = ?
	`, input.Name, bindingsJSON, boolToInt64(input.IsArchived), now, id, userID)
	if err != nil {
		return fmt.Errorf("update activity kind: %w", err)
	}
	changedRows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update activity kind rows affected: %w", err)
	}
	if changedRows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ---------- CategoryGroup ----------

func (r *Repository) ListCategoryGroups(ctx context.Context, userID int64) ([]CategoryGroup, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, kind, isArchived, createdAt, updatedAt
		FROM timeCategoryGroup
		WHERE userId = ?
		ORDER BY name ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list category groups: %w", err)
	}
	defer rows.Close()

	result := make([]CategoryGroup, 0)
	for rows.Next() {
		item, err := scanCategoryGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("scan category group: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate category groups: %w", err)
	}
	return result, nil
}

func (r *Repository) GetCategoryGroupByID(ctx context.Context, userID int64, id int64) (*CategoryGroup, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, kind, isArchived, createdAt, updatedAt
		FROM timeCategoryGroup
		WHERE id = ? AND userId = ?
	`, id, userID)

	item, err := scanCategoryGroup(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get category group: %w", err)
	}
	return &item, nil
}

func (r *Repository) CreateCategoryGroup(ctx context.Context, userID int64, input CategoryGroupInput, now string) (int64, error) {
	result, err := r.write.ExecContext(ctx, `
		INSERT INTO timeCategoryGroup (userId, name, kind, isArchived, createdAt, updatedAt)
		VALUES (?, ?, ?, ?, ?, ?)
	`, userID, input.Name, valueOrNil(input.Kind), boolToInt64(input.IsArchived), now, now)
	if err != nil {
		return 0, fmt.Errorf("create category group: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("category group last insert id: %w", err)
	}
	return id, nil
}

func (r *Repository) UpdateCategoryGroup(ctx context.Context, userID int64, id int64, input CategoryGroupInput, now string) error {
	result, err := r.write.ExecContext(ctx, `
		UPDATE timeCategoryGroup
		SET name = ?, kind = ?, isArchived = ?, updatedAt = ?
		WHERE id = ? AND userId = ?
	`, input.Name, valueOrNil(input.Kind), boolToInt64(input.IsArchived), now, id, userID)
	if err != nil {
		return fmt.Errorf("update category group: %w", err)
	}
	changedRows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update category group rows affected: %w", err)
	}
	if changedRows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *Repository) DeleteCategoryGroup(ctx context.Context, userID int64, id int64) error {
	result, err := r.write.ExecContext(ctx, `
		DELETE FROM timeCategoryGroup
		WHERE id = ? AND userId = ?
	`, id, userID)
	if err != nil {
		return fmt.Errorf("delete category group: %w", err)
	}
	changedRows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete category group rows affected: %w", err)
	}
	if changedRows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// CountOptionsInGroup is used by the service to block group delete/archive
// while it still owns options — mirrors money's CountChildCategories/
// CountTransactionsByCategory pre-check pattern (app-level, not a caught FK error).
func (r *Repository) CountOptionsInGroup(ctx context.Context, groupID int64) (int64, error) {
	var count int64
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM timeCategoryOption WHERE groupId = ?
	`, groupID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count options in group: %w", err)
	}
	return count, nil
}

// CountNonArchivedAreaGroups is used by the service to enforce "at most one
// non-archived kind='area' group per user".
func (r *Repository) CountNonArchivedAreaGroups(ctx context.Context, userID int64, excludeID *int64) (int64, error) {
	var count int64
	var err error
	if excludeID == nil {
		err = r.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM timeCategoryGroup
			WHERE userId = ? AND kind = ? AND isArchived = 0
		`, userID, CategoryGroupKindArea).Scan(&count)
	} else {
		err = r.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM timeCategoryGroup
			WHERE userId = ? AND kind = ? AND isArchived = 0 AND id != ?
		`, userID, CategoryGroupKindArea, *excludeID).Scan(&count)
	}
	if err != nil {
		return 0, fmt.Errorf("count non-archived area groups: %w", err)
	}
	return count, nil
}

// ---------- CategoryOption ----------

func (r *Repository) ListCategoryOptions(ctx context.Context, userID int64) ([]CategoryOption, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, groupId, name, color, isArchived, createdAt, updatedAt
		FROM timeCategoryOption
		WHERE userId = ?
		ORDER BY name ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list category options: %w", err)
	}
	defer rows.Close()

	result := make([]CategoryOption, 0)
	for rows.Next() {
		item, err := scanCategoryOption(rows)
		if err != nil {
			return nil, fmt.Errorf("scan category option: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate category options: %w", err)
	}
	return result, nil
}

func (r *Repository) GetCategoryOptionByID(ctx context.Context, userID int64, id int64) (*CategoryOption, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, groupId, name, color, isArchived, createdAt, updatedAt
		FROM timeCategoryOption
		WHERE id = ? AND userId = ?
	`, id, userID)

	item, err := scanCategoryOption(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get category option: %w", err)
	}
	return &item, nil
}

func (r *Repository) CreateCategoryOption(ctx context.Context, userID int64, input CategoryOptionInput, now string) (int64, error) {
	result, err := r.write.ExecContext(ctx, `
		INSERT INTO timeCategoryOption (userId, groupId, name, color, isArchived, createdAt, updatedAt)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, userID, input.GroupID, input.Name, valueOrNil(input.Color), boolToInt64(input.IsArchived), now, now)
	if err != nil {
		return 0, fmt.Errorf("create category option: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("category option last insert id: %w", err)
	}
	return id, nil
}

func (r *Repository) UpdateCategoryOption(ctx context.Context, userID int64, id int64, input CategoryOptionInput, now string) error {
	result, err := r.write.ExecContext(ctx, `
		UPDATE timeCategoryOption
		SET groupId = ?, name = ?, color = ?, isArchived = ?, updatedAt = ?
		WHERE id = ? AND userId = ?
	`, input.GroupID, input.Name, valueOrNil(input.Color), boolToInt64(input.IsArchived), now, id, userID)
	if err != nil {
		return fmt.Errorf("update category option: %w", err)
	}
	changedRows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update category option rows affected: %w", err)
	}
	if changedRows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *Repository) DeleteCategoryOption(ctx context.Context, userID int64, id int64) error {
	result, err := r.write.ExecContext(ctx, `
		DELETE FROM timeCategoryOption
		WHERE id = ? AND userId = ?
	`, id, userID)
	if err != nil {
		return fmt.Errorf("delete category option: %w", err)
	}
	changedRows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete category option rows affected: %w", err)
	}
	if changedRows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// CountEntryOptionsForOption is used by the service to block option
// delete/archive while historical entries still reference it.
func (r *Repository) CountEntryOptionsForOption(ctx context.Context, optionID int64) (int64, error) {
	var count int64
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM timeEntryOption WHERE optionId = ?
	`, optionID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count entry options for option: %w", err)
	}
	return count, nil
}

// ---------- Catalog ----------

func (r *Repository) GetCatalog(ctx context.Context, userID int64) (Catalog, error) {
	kinds, err := r.ListActivityKinds(ctx, userID)
	if err != nil {
		return Catalog{}, err
	}
	groups, err := r.ListCategoryGroups(ctx, userID)
	if err != nil {
		return Catalog{}, err
	}
	options, err := r.ListCategoryOptions(ctx, userID)
	if err != nil {
		return Catalog{}, err
	}
	return Catalog{ActivityKinds: kinds, CategoryGroups: groups, CategoryOptions: options}, nil
}

// ---------- Entry ----------

func (r *Repository) ListEntries(ctx context.Context, userID int64, start *string) ([]Entry, error) {
	var rows *sql.Rows
	var err error
	if start == nil {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, activityKindId, track, startAt, endAt, createdAt, updatedAt
			FROM timeEntry
			WHERE userId = ?
			ORDER BY startAt ASC
		`, userID)
	} else {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, activityKindId, track, startAt, endAt, createdAt, updatedAt
			FROM timeEntry
			WHERE userId = ? AND startAt >= ?
			ORDER BY startAt ASC
		`, userID, *start)
	}
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()

	result := make([]Entry, 0)
	for rows.Next() {
		item, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan entry: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entries: %w", err)
	}

	optionsByEntry, err := r.listEntryOptionsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range result {
		options := optionsByEntry[result[i].ID]
		if options == nil {
			options = make([]EntryOption, 0)
		}
		result[i].Options = options
	}

	return result, nil
}

func (r *Repository) GetEntryByID(ctx context.Context, userID int64, entryID int64) (*Entry, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, activityKindId, track, startAt, endAt, createdAt, updatedAt
		FROM timeEntry
		WHERE id = ? AND userId = ?
	`, entryID, userID)

	item, err := scanEntry(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get entry: %w", err)
	}

	options, err := r.listEntryOptions(ctx, entryID)
	if err != nil {
		return nil, err
	}
	item.Options = options

	return &item, nil
}

// listEntryOptionsForUser groups options by entryId in one query — same
// batching pattern as listActivityCategoryIDs, avoids N+1 on ListEntries.
func (r *Repository) listEntryOptionsForUser(ctx context.Context, userID int64) (map[int64][]EntryOption, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT eo.entryId, eo.groupId, eo.optionId
		FROM timeEntryOption eo
		JOIN timeEntry e ON e.id = eo.entryId
		WHERE e.userId = ?
		ORDER BY eo.entryId ASC, eo.groupId ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list entry options for user: %w", err)
	}
	defer rows.Close()

	result := make(map[int64][]EntryOption)
	for rows.Next() {
		var entryID int64
		var option EntryOption
		if err := rows.Scan(&entryID, &option.GroupID, &option.OptionID); err != nil {
			return nil, fmt.Errorf("scan entry option: %w", err)
		}
		result[entryID] = append(result[entryID], option)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entry options: %w", err)
	}
	return result, nil
}

func (r *Repository) listEntryOptions(ctx context.Context, entryID int64) ([]EntryOption, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT groupId, optionId
		FROM timeEntryOption
		WHERE entryId = ?
		ORDER BY groupId ASC
	`, entryID)
	if err != nil {
		return nil, fmt.Errorf("list entry options: %w", err)
	}
	defer rows.Close()

	result := make([]EntryOption, 0)
	for rows.Next() {
		var option EntryOption
		if err := rows.Scan(&option.GroupID, &option.OptionID); err != nil {
			return nil, fmt.Errorf("scan entry option: %w", err)
		}
		result = append(result, option)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entry options: %w", err)
	}
	return result, nil
}

// CreateEntry checks for an overlap and inserts the entry + its options inside the caller's
// write transaction: the single write connection serializes it against every other write, so
// the check and the insert can't interleave with a concurrent request.
func (r *Repository) CreateEntry(ctx context.Context, tx *sql.Tx, userID int64, input EntryInput, now string) (int64, error) {
	overlaps, err := hasOverlap(ctx, tx, userID, input.Track, input.StartAt, input.EndAt, nil)
	if err != nil {
		return 0, err
	}
	if overlaps {
		return 0, ErrOverlap
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO timeEntry (userId, activityKindId, track, startAt, endAt, createdAt, updatedAt)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, userID, input.ActivityKindID, input.Track, input.StartAt, input.EndAt, now, now)
	if err != nil {
		return 0, fmt.Errorf("create entry: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("entry last insert id: %w", err)
	}

	if err := insertEntryOptions(ctx, tx, id, input.Options); err != nil {
		return 0, err
	}
	return id, nil
}

// UpdateEntryTime checks for an overlap (excluding itself) and writes only
// track/startAt/endAt atomically — semantic selection (activityKindId,
// options) is left untouched, see implementation plan ("Update time").
func (r *Repository) UpdateEntryTime(ctx context.Context, tx *sql.Tx, userID int64, entryID int64, input EntryTimeInput, now string) error {
	overlaps, err := hasOverlap(ctx, tx, userID, input.Track, input.StartAt, input.EndAt, &entryID)
	if err != nil {
		return err
	}
	if overlaps {
		return ErrOverlap
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE timeEntry
		SET track = ?, startAt = ?, endAt = ?, updatedAt = ?
		WHERE id = ? AND userId = ?
	`, input.Track, input.StartAt, input.EndAt, now, entryID, userID)
	if err != nil {
		return fmt.Errorf("update entry time: %w", err)
	}
	changedRows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update entry time rows affected: %w", err)
	}
	if changedRows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateEntrySelection replaces activityKindId and the full set of options —
// track/startAt/endAt are left untouched, see implementation plan
// ("Update selection"). No overlap check: the interval doesn't change.
func (r *Repository) UpdateEntrySelection(ctx context.Context, tx *sql.Tx, userID int64, entryID int64, input EntrySelectionInput, now string) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE timeEntry
		SET activityKindId = ?, updatedAt = ?
		WHERE id = ? AND userId = ?
	`, input.ActivityKindID, now, entryID, userID)
	if err != nil {
		return fmt.Errorf("update entry selection: %w", err)
	}
	changedRows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update entry selection rows affected: %w", err)
	}
	if changedRows == 0 {
		return sql.ErrNoRows
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM timeEntryOption WHERE entryId = ?`, entryID); err != nil {
		return fmt.Errorf("clear entry options: %w", err)
	}
	return insertEntryOptions(ctx, tx, entryID, input.Options)
}

func insertEntryOptions(ctx context.Context, tx *sql.Tx, entryID int64, options []EntryOption) error {
	for _, option := range options {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO timeEntryOption (entryId, groupId, optionId)
			VALUES (?, ?, ?)
		`, entryID, option.GroupID, option.OptionID); err != nil {
			return fmt.Errorf("insert entry option: %w", err)
		}
	}
	return nil
}

func (r *Repository) DeleteEntry(ctx context.Context, tx *sql.Tx, userID int64, entryID int64) error {
	result, err := tx.ExecContext(ctx, `
		DELETE FROM timeEntry
		WHERE id = ? AND userId = ?
	`, entryID, userID)
	if err != nil {
		return fmt.Errorf("delete entry: %w", err)
	}
	changedRows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete entry rows affected: %w", err)
	}
	if changedRows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// hasOverlap reports whether an entry of the same userId+track intersects
// [startAt, endAt) (endAt exclusive, per concept). excludeID skips a row
// (the entry being updated) from the check.
func hasOverlap(ctx context.Context, tx *sql.Tx, userID int64, track Track, startAt string, endAt string, excludeID *int64) (bool, error) {
	var count int64
	var err error
	if excludeID == nil {
		err = tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM timeEntry
			WHERE userId = ? AND track = ? AND startAt < ? AND endAt > ?
		`, userID, track, endAt, startAt).Scan(&count)
	} else {
		err = tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM timeEntry
			WHERE userId = ? AND track = ? AND startAt < ? AND endAt > ? AND id != ?
		`, userID, track, endAt, startAt, *excludeID).Scan(&count)
	}
	if err != nil {
		return false, fmt.Errorf("check entry overlap: %w", err)
	}
	return count > 0, nil
}

func scanActivityKind(scanner interface{ Scan(dest ...any) error }) (ActivityKind, error) {
	var item ActivityKind
	var bindingsJSON string
	var isArchived int64
	if err := scanner.Scan(&item.ID, &item.Name, &bindingsJSON, &isArchived, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return ActivityKind{}, err
	}
	item.IsArchived = isArchived != 0
	bindings, err := unmarshalGroupBindings(bindingsJSON)
	if err != nil {
		return ActivityKind{}, err
	}
	item.GroupBindings = bindings
	return item, nil
}

func scanCategoryGroup(scanner interface{ Scan(dest ...any) error }) (CategoryGroup, error) {
	var item CategoryGroup
	var kind sql.NullString
	var isArchived int64
	if err := scanner.Scan(&item.ID, &item.Name, &kind, &isArchived, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return CategoryGroup{}, err
	}
	item.Kind = nullableString(kind)
	item.IsArchived = isArchived != 0
	return item, nil
}

func scanCategoryOption(scanner interface{ Scan(dest ...any) error }) (CategoryOption, error) {
	var item CategoryOption
	var color sql.NullString
	var isArchived int64
	if err := scanner.Scan(&item.ID, &item.GroupID, &item.Name, &color, &isArchived, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return CategoryOption{}, err
	}
	item.Color = nullableString(color)
	item.IsArchived = isArchived != 0
	return item, nil
}

func scanEntry(scanner interface{ Scan(dest ...any) error }) (Entry, error) {
	var item Entry
	if err := scanner.Scan(&item.ID, &item.ActivityKindID, &item.Track, &item.StartAt, &item.EndAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return Entry{}, err
	}
	return item, nil
}

func marshalGroupBindings(bindings []GroupBinding) (string, error) {
	if bindings == nil {
		bindings = []GroupBinding{}
	}
	encoded, err := json.Marshal(bindings)
	if err != nil {
		return "", fmt.Errorf("marshal group bindings: %w", err)
	}
	return string(encoded), nil
}

func unmarshalGroupBindings(value string) ([]GroupBinding, error) {
	result := make([]GroupBinding, 0)
	if value == "" {
		return result, nil
	}
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return nil, fmt.Errorf("unmarshal group bindings: %w", err)
	}
	return result, nil
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func valueOrNil(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
