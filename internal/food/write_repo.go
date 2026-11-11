package food

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type WeightByDateRow struct {
	ID     int64
	Weight float64
}

func (r *Repository) CreateDiaryEntry(ctx context.Context, tx *sql.Tx, userID int64, dateISO string, foodCatalogueID int64, foodWeight int64, history string) (int64, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO foodDiary (dateISO, foodCatalogueId, foodWeight, history, usersId, ver, del)
		VALUES (?, ?, ?, ?, ?, 1, 0)
	`, dateISO, foodCatalogueID, foodWeight, history, userID)
	if err != nil {
		return 0, fmt.Errorf("create diary entry: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("create diary entry last insert id: %w", err)
	}

	return id, nil
}

func (r *Repository) CreateDiaryEntriesBatch(ctx context.Context, tx *sql.Tx, userID int64, entries []DiaryEntry) ([]DiaryEntry, error) {
	result := make([]DiaryEntry, 0, len(entries))
	for _, entry := range entries {
		historyJSON, err := toHistoryJSON(entry.History)
		if err != nil {
			return nil, err
		}
		execResult, err := tx.ExecContext(ctx, `
			INSERT INTO foodDiary (dateISO, foodCatalogueId, foodWeight, history, usersId, ver, del)
			VALUES (?, ?, ?, ?, ?, 1, 0)
		`, entry.DateISO, entry.FoodCatalogueID, entry.FoodWeight, historyJSON, userID)
		if err != nil {
			return nil, fmt.Errorf("insert diary entry in batch: %w", err)
		}
		id, err := execResult.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("batch diary entry last insert id: %w", err)
		}
		entry.ID = id
		result = append(result, entry)
	}

	return result, nil
}

type DiaryEntryForEditRow struct {
	FoodCatalogueID int64
	FoodWeight      int64
	History         string
	Version         int64
}

func (r *Repository) GetDiaryEntryForEdit(ctx context.Context, tx *sql.Tx, diaryID int64, userID int64) (*DiaryEntryForEditRow, error) {
	row := tx.QueryRowContext(ctx, `SELECT foodCatalogueId, foodWeight, history, ver FROM foodDiary WHERE id = ? AND usersId = ?`, diaryID, userID)

	var result DiaryEntryForEditRow
	if err := row.Scan(&result.FoodCatalogueID, &result.FoodWeight, &result.History, &result.Version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get diary entry for edit: %w", err)
	}

	return &result, nil
}

func (r *Repository) UpdateDiaryEntry(ctx context.Context, tx *sql.Tx, diaryID int64, userID int64, foodWeight int64, history string, newVersion int64) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE foodDiary
		SET foodWeight = ?, history = ?, ver = ?
		WHERE id = ? AND usersId = ?
	`, foodWeight, history, newVersion, diaryID, userID); err != nil {
		return fmt.Errorf("update diary entry: %w", err)
	}

	return nil
}

func (r *Repository) DeleteDiaryEntry(ctx context.Context, tx *sql.Tx, diaryID int64, userID int64) (bool, error) {
	result, err := tx.ExecContext(ctx, `DELETE FROM foodDiary WHERE id = ? AND usersId = ?`, diaryID, userID)
	if err != nil {
		return false, fmt.Errorf("delete diary entry: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete diary entry rows affected: %w", err)
	}

	return rowsAffected > 0, nil
}

func (r *Repository) DeleteDiaryEntriesByDate(ctx context.Context, tx *sql.Tx, dateISO string, userID int64) (int64, error) {
	result, err := tx.ExecContext(ctx, `DELETE FROM foodDiary WHERE dateISO = ? AND usersId = ?`, dateISO, userID)
	if err != nil {
		return 0, fmt.Errorf("delete diary entries by date: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete diary entries by date rows affected: %w", err)
	}

	return rowsAffected, nil
}

func (r *Repository) GetWeightByDate(ctx context.Context, tx *sql.Tx, dateISO string, userID int64) (*WeightByDateRow, error) {
	row := tx.QueryRowContext(ctx, `SELECT id, weight FROM foodBodyWeight WHERE dateISO = ? AND usersId = ?`, dateISO, userID)

	var result WeightByDateRow
	if err := row.Scan(&result.ID, &result.Weight); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get weight by date: %w", err)
	}

	return &result, nil
}

func (r *Repository) CreateWeight(ctx context.Context, tx *sql.Tx, dateISO string, weight float64, userID int64) (int64, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO foodBodyWeight (dateISO, weight, usersId) VALUES (?, ?, ?)`, dateISO, weight, userID)
	if err != nil {
		return 0, fmt.Errorf("create weight: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("create weight last insert id: %w", err)
	}
	return id, nil
}

func (r *Repository) UpdateWeight(ctx context.Context, tx *sql.Tx, dateISO string, weight float64, userID int64) (bool, error) {
	result, err := tx.ExecContext(ctx, `UPDATE foodBodyWeight SET weight = ? WHERE dateISO = ? AND usersId = ?`, weight, dateISO, userID)
	if err != nil {
		return false, fmt.Errorf("update weight: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("update weight rows affected: %w", err)
	}
	return rowsAffected > 0, nil
}
