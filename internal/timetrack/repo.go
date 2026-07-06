package timetrack

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrOverlap = errors.New("time entry overlaps an existing entry on the same track")

type Repository struct {
	db *sql.DB
}

type txRunner interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) ListActivities(ctx context.Context, userID int64) ([]Activity, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, isArchived, createdAt
		FROM timeActivity
		WHERE userId = ?
		ORDER BY name ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list activities: %w", err)
	}
	defer rows.Close()

	result := make([]Activity, 0)
	for rows.Next() {
		var item Activity
		var isArchived int64
		if err := rows.Scan(&item.ID, &item.Name, &isArchived, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan activity: %w", err)
		}
		item.IsArchived = isArchived != 0
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate activities: %w", err)
	}

	categoryIDsByActivity, err := r.listActivityCategoryIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range result {
		categoryIDs := categoryIDsByActivity[result[i].ID]
		if categoryIDs == nil {
			categoryIDs = make([]int64, 0)
		}
		result[i].CategoryIDs = categoryIDs
	}

	return result, nil
}

func (r *Repository) GetActivityByID(ctx context.Context, userID int64, activityID int64) (*Activity, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, isArchived, createdAt
		FROM timeActivity
		WHERE id = ? AND userId = ?
	`, activityID, userID)

	var item Activity
	var isArchived int64
	if err := row.Scan(&item.ID, &item.Name, &isArchived, &item.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get activity: %w", err)
	}
	item.IsArchived = isArchived != 0

	categoryIDs, err := r.listCategoryIDsForActivity(ctx, activityID)
	if err != nil {
		return nil, err
	}
	item.CategoryIDs = categoryIDs

	return &item, nil
}

func (r *Repository) listActivityCategoryIDs(ctx context.Context, userID int64) (map[int64][]int64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT ac.activityId, ac.categoryId
		FROM timeActivityCategory ac
		JOIN timeActivity a ON a.id = ac.activityId
		WHERE a.userId = ?
		ORDER BY ac.activityId ASC, ac.categoryId ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list activity category ids: %w", err)
	}
	defer rows.Close()

	result := make(map[int64][]int64)
	for rows.Next() {
		var activityID, categoryID int64
		if err := rows.Scan(&activityID, &categoryID); err != nil {
			return nil, fmt.Errorf("scan activity category id: %w", err)
		}
		result[activityID] = append(result[activityID], categoryID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate activity category ids: %w", err)
	}
	return result, nil
}

func (r *Repository) listCategoryIDsForActivity(ctx context.Context, activityID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT categoryId
		FROM timeActivityCategory
		WHERE activityId = ?
		ORDER BY categoryId ASC
	`, activityID)
	if err != nil {
		return nil, fmt.Errorf("list category ids for activity: %w", err)
	}
	defer rows.Close()

	result := make([]int64, 0)
	for rows.Next() {
		var categoryID int64
		if err := rows.Scan(&categoryID); err != nil {
			return nil, fmt.Errorf("scan category id: %w", err)
		}
		result = append(result, categoryID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate category ids: %w", err)
	}
	return result, nil
}

func (r *Repository) CreateActivity(ctx context.Context, userID int64, input ActivityInput, now string) (id int64, err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin create activity: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO timeActivity (userId, name, isArchived, createdAt)
		VALUES (?, ?, ?, ?)
	`, userID, input.Name, boolToInt64(input.IsArchived), now)
	if err != nil {
		return 0, fmt.Errorf("create activity: %w", err)
	}
	id, err = result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("activity last insert id: %w", err)
	}

	if err = replaceActivityCategories(ctx, tx, id, input.CategoryIDs); err != nil {
		return 0, err
	}

	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit create activity: %w", err)
	}
	return id, nil
}

func (r *Repository) UpdateActivity(ctx context.Context, userID int64, activityID int64, input ActivityInput) (err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update activity: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	result, err := tx.ExecContext(ctx, `
		UPDATE timeActivity
		SET name = ?, isArchived = ?
		WHERE id = ? AND userId = ?
	`, input.Name, boolToInt64(input.IsArchived), activityID, userID)
	if err != nil {
		return fmt.Errorf("update activity: %w", err)
	}
	changedRows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update activity rows affected: %w", err)
	}
	if changedRows == 0 {
		return sql.ErrNoRows
	}

	if err = replaceActivityCategories(ctx, tx, activityID, input.CategoryIDs); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit update activity: %w", err)
	}
	return nil
}

func replaceActivityCategories(ctx context.Context, tx *sql.Tx, activityID int64, categoryIDs []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM timeActivityCategory WHERE activityId = ?`, activityID); err != nil {
		return fmt.Errorf("clear activity categories: %w", err)
	}
	for _, categoryID := range categoryIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO timeActivityCategory (activityId, categoryId)
			VALUES (?, ?)
		`, activityID, categoryID); err != nil {
			return fmt.Errorf("insert activity category: %w", err)
		}
	}
	return nil
}

func (r *Repository) ListCategories(ctx context.Context, userID int64) ([]Category, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, kind, color, impact, createdAt
		FROM timeCategory
		WHERE userId = ?
		ORDER BY name ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	result := make([]Category, 0)
	for rows.Next() {
		item, err := scanCategory(rows)
		if err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate categories: %w", err)
	}
	return result, nil
}

func (r *Repository) GetCategoryByID(ctx context.Context, userID int64, categoryID int64) (*Category, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, kind, color, impact, createdAt
		FROM timeCategory
		WHERE id = ? AND userId = ?
	`, categoryID, userID)

	item, err := scanCategory(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get category: %w", err)
	}
	return &item, nil
}

func (r *Repository) CreateCategory(ctx context.Context, userID int64, input CategoryInput, now string) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO timeCategory (userId, name, kind, color, impact, createdAt)
		VALUES (?, ?, ?, ?, ?, ?)
	`, userID, input.Name, valueOrNil(input.Kind), valueOrNil(input.Color), valueOrNil(input.Impact), now)
	if err != nil {
		return 0, fmt.Errorf("create category: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("category last insert id: %w", err)
	}
	return id, nil
}

func (r *Repository) UpdateCategory(ctx context.Context, userID int64, categoryID int64, input CategoryInput) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE timeCategory
		SET name = ?, kind = ?, color = ?, impact = ?
		WHERE id = ? AND userId = ?
	`, input.Name, valueOrNil(input.Kind), valueOrNil(input.Color), valueOrNil(input.Impact), categoryID, userID)
	if err != nil {
		return fmt.Errorf("update category: %w", err)
	}
	changedRows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update category rows affected: %w", err)
	}
	if changedRows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *Repository) DeleteCategory(ctx context.Context, userID int64, categoryID int64) error {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM timeCategory
		WHERE id = ? AND userId = ?
	`, categoryID, userID)
	if err != nil {
		return fmt.Errorf("delete category: %w", err)
	}
	changedRows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete category rows affected: %w", err)
	}
	if changedRows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *Repository) ListEntries(ctx context.Context, userID int64, start *string) ([]Entry, error) {
	var rows *sql.Rows
	var err error
	if start == nil {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, activityId, track, startAt, endAt, createdAt, updatedAt
			FROM timeEntry
			WHERE userId = ?
			ORDER BY startAt ASC
		`, userID)
	} else {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, activityId, track, startAt, endAt, createdAt, updatedAt
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
	return result, nil
}

func (r *Repository) GetEntryByID(ctx context.Context, userID int64, entryID int64) (*Entry, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, activityId, track, startAt, endAt, createdAt, updatedAt
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
	return &item, nil
}

// CreateEntry checks for an overlap and inserts the new entry atomically —
// the check and the write happen inside the same transaction so the whole
// operation is all-or-nothing (call is expected to be serialized by the
// caller's mutex, see Service).
func (r *Repository) CreateEntry(ctx context.Context, userID int64, input EntryInput, now string) (id int64, err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin create entry: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	overlaps, err := hasOverlap(ctx, tx, userID, input.Track, input.StartAt, input.EndAt, nil)
	if err != nil {
		return 0, err
	}
	if overlaps {
		err = ErrOverlap
		return 0, err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO timeEntry (userId, activityId, track, startAt, endAt, createdAt, updatedAt)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, userID, input.ActivityID, input.Track, input.StartAt, input.EndAt, now, now)
	if err != nil {
		return 0, fmt.Errorf("create entry: %w", err)
	}
	id, err = result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("entry last insert id: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit create entry: %w", err)
	}
	return id, nil
}

// UpdateEntry checks for an overlap (excluding itself) and writes the new
// fields atomically, same reasoning as CreateEntry.
func (r *Repository) UpdateEntry(ctx context.Context, userID int64, entryID int64, input EntryInput, now string) (err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update entry: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	overlaps, err := hasOverlap(ctx, tx, userID, input.Track, input.StartAt, input.EndAt, &entryID)
	if err != nil {
		return err
	}
	if overlaps {
		err = ErrOverlap
		return err
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE timeEntry
		SET activityId = ?, track = ?, startAt = ?, endAt = ?, updatedAt = ?
		WHERE id = ? AND userId = ?
	`, input.ActivityID, input.Track, input.StartAt, input.EndAt, now, entryID, userID)
	if err != nil {
		return fmt.Errorf("update entry: %w", err)
	}
	changedRows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update entry rows affected: %w", err)
	}
	if changedRows == 0 {
		err = sql.ErrNoRows
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit update entry: %w", err)
	}
	return nil
}

func (r *Repository) DeleteEntry(ctx context.Context, userID int64, entryID int64) error {
	result, err := r.db.ExecContext(ctx, `
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
func hasOverlap(ctx context.Context, runner txRunner, userID int64, track Track, startAt string, endAt string, excludeID *int64) (bool, error) {
	var count int64
	var err error
	if excludeID == nil {
		err = runner.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM timeEntry
			WHERE userId = ? AND track = ? AND startAt < ? AND endAt > ?
		`, userID, track, endAt, startAt).Scan(&count)
	} else {
		err = runner.QueryRowContext(ctx, `
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

// ListRecentActivityIDs returns activityId ordered by the most recent
// startAt they were used with — for autocomplete/quick-picker on the
// frontend. No dedicated table, derived on the fly (see concept doc).
func (r *Repository) ListRecentActivityIDs(ctx context.Context, userID int64, track *Track, limit int) ([]int64, error) {
	var rows *sql.Rows
	var err error
	if track == nil {
		rows, err = r.db.QueryContext(ctx, `
			SELECT activityId
			FROM timeEntry
			WHERE userId = ?
			GROUP BY activityId
			ORDER BY MAX(startAt) DESC
			LIMIT ?
		`, userID, limit)
	} else {
		rows, err = r.db.QueryContext(ctx, `
			SELECT activityId
			FROM timeEntry
			WHERE userId = ? AND track = ?
			GROUP BY activityId
			ORDER BY MAX(startAt) DESC
			LIMIT ?
		`, userID, *track, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list recent activity ids: %w", err)
	}
	defer rows.Close()

	result := make([]int64, 0)
	for rows.Next() {
		var activityID int64
		if err := rows.Scan(&activityID); err != nil {
			return nil, fmt.Errorf("scan recent activity id: %w", err)
		}
		result = append(result, activityID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent activity ids: %w", err)
	}
	return result, nil
}

func scanCategory(scanner interface{ Scan(dest ...any) error }) (Category, error) {
	var item Category
	var kind sql.NullString
	var color sql.NullString
	var impact sql.NullString
	if err := scanner.Scan(&item.ID, &item.Name, &kind, &color, &impact, &item.CreatedAt); err != nil {
		return Category{}, err
	}
	item.Kind = nullableString(kind)
	item.Color = nullableString(color)
	item.Impact = nullableString(impact)
	return item, nil
}

func scanEntry(scanner interface{ Scan(dest ...any) error }) (Entry, error) {
	var item Entry
	if err := scanner.Scan(&item.ID, &item.ActivityID, &item.Track, &item.StartAt, &item.EndAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return Entry{}, err
	}
	return item, nil
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
