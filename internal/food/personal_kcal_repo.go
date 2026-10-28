package food

import (
	"context"
	"fmt"
)

type PersonalKcalHistoryRow struct {
	FoodCatalogueID int64
	YearMonth       string
	KcalsPer100g    float64
}

type PersonalNormHistoryRow struct {
	YearMonth string
	NormKcals float64
	KcalPerKg float64
}

func (r *Repository) GetPersonalKcalHistory(ctx context.Context, userID int64) ([]PersonalKcalHistoryRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT foodCatalogueId, yearMonth, kcalsPer100g
		FROM foodPersonalKcalHistory
		WHERE usersId = ?
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("get personal kcal history: %w", err)
	}
	defer rows.Close()

	var result []PersonalKcalHistoryRow
	for rows.Next() {
		var row PersonalKcalHistoryRow
		if err := rows.Scan(&row.FoodCatalogueID, &row.YearMonth, &row.KcalsPer100g); err != nil {
			return nil, fmt.Errorf("scan personal kcal history row: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate personal kcal history rows: %w", err)
	}
	return result, nil
}

func (r *Repository) GetPersonalNormHistory(ctx context.Context, userID int64) ([]PersonalNormHistoryRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT yearMonth, normKcals, kcalPerKg
		FROM foodPersonalNormHistory
		WHERE usersId = ?
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("get personal norm history: %w", err)
	}
	defer rows.Close()

	var result []PersonalNormHistoryRow
	for rows.Next() {
		var row PersonalNormHistoryRow
		if err := rows.Scan(&row.YearMonth, &row.NormKcals, &row.KcalPerKg); err != nil {
			return nil, fmt.Errorf("scan personal norm history row: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate personal norm history rows: %w", err)
	}
	return result, nil
}

func (r *Repository) InsertPersonalKcalHistory(ctx context.Context, userID int64, catalogueID int64, yearMonth string, kcalsPer100g float64, createdAt string) error {
	_, err := r.write.ExecContext(ctx, `
		INSERT INTO foodPersonalKcalHistory (usersId, foodCatalogueId, yearMonth, kcalsPer100g, createdAt)
		VALUES (?, ?, ?, ?, ?)
	`, userID, catalogueID, yearMonth, kcalsPer100g, createdAt)
	if err != nil {
		return fmt.Errorf("insert personal kcal history: %w", err)
	}
	return nil
}

func (r *Repository) InsertPersonalNormHistory(ctx context.Context, userID int64, yearMonth string, normKcals float64, kcalPerKg float64, createdAt string) error {
	_, err := r.write.ExecContext(ctx, `
		INSERT INTO foodPersonalNormHistory (usersId, yearMonth, normKcals, kcalPerKg, createdAt)
		VALUES (?, ?, ?, ?, ?)
	`, userID, yearMonth, normKcals, kcalPerKg, createdAt)
	if err != nil {
		return fmt.Errorf("insert personal norm history: %w", err)
	}
	return nil
}

func (r *Repository) DeletePersonalHistoryForUser(ctx context.Context, userID int64) error {
	if _, err := r.write.ExecContext(ctx, `DELETE FROM foodPersonalKcalHistory WHERE usersId = ?`, userID); err != nil {
		return fmt.Errorf("delete personal kcal history for user: %w", err)
	}
	if _, err := r.write.ExecContext(ctx, `DELETE FROM foodPersonalNormHistory WHERE usersId = ?`, userID); err != nil {
		return fmt.Errorf("delete personal norm history for user: %w", err)
	}
	return nil
}

func (r *Repository) DeletePersonalHistoryForAllUsers(ctx context.Context) error {
	if _, err := r.write.ExecContext(ctx, `DELETE FROM foodPersonalKcalHistory`); err != nil {
		return fmt.Errorf("delete personal kcal history for all users: %w", err)
	}
	if _, err := r.write.ExecContext(ctx, `DELETE FROM foodPersonalNormHistory`); err != nil {
		return fmt.Errorf("delete personal norm history for all users: %w", err)
	}
	return nil
}
