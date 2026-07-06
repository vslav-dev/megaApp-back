package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type UserSettings struct {
	SelectedChapterFood  bool   `json:"selectedChapterFood"`
	SelectedChapterMoney bool   `json:"selectedChapterMoney"`
	SelectedChapterTime  bool   `json:"selectedChapterTime"`
	DarkTheme            bool   `json:"darkTheme"`
	LiteVersion          bool   `json:"liteVersion"`
	Height               *int64 `json:"height"`
	UserName             string `json:"userName"`
	IsUserAdmin          bool   `json:"isUserAdmin"`
}

type StoredSettings struct {
	SelectedChapterFood  bool
	SelectedChapterMoney bool
	SelectedChapterTime  bool
	DarkTheme            bool
	LiteVersion          bool
	Height               *int64
}

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) GetByUserID(ctx context.Context, userID int64) (*StoredSettings, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT darkTheme, selectedChapterFood, selectedChapterMoney, COALESCE(selectedChapterTime, 0), liteVersion, height
		FROM settings
		WHERE usersId = ?
	`, userID)

	var darkTheme bool
	var selectedChapterFood bool
	var selectedChapterMoney bool
	var selectedChapterTime bool
	var liteVersion bool
	var height sql.NullInt64

	if err := row.Scan(&darkTheme, &selectedChapterFood, &selectedChapterMoney, &selectedChapterTime, &liteVersion, &height); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get settings by user id: %w", err)
	}

	return &StoredSettings{
		DarkTheme:            darkTheme,
		SelectedChapterFood:  selectedChapterFood,
		SelectedChapterMoney: selectedChapterMoney,
		SelectedChapterTime:  selectedChapterTime,
		LiteVersion:          liteVersion,
		Height:               nullableInt64Ptr(height),
	}, nil
}

func (r *Repository) Upsert(ctx context.Context, userID int64, settings StoredSettings) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE settings
		SET darkTheme = ?, selectedChapterFood = ?, selectedChapterMoney = ?, selectedChapterTime = ?, liteVersion = ?, height = ?
		WHERE usersId = ?
	`, settings.DarkTheme, settings.SelectedChapterFood, settings.SelectedChapterMoney, settings.SelectedChapterTime, settings.LiteVersion, settings.Height, userID)
	if err != nil {
		return fmt.Errorf("update settings: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update settings rows affected: %w", err)
	}
	if rowsAffected > 0 {
		return nil
	}

	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO settings (usersId, darkTheme, selectedChapterFood, selectedChapterMoney, selectedChapterTime, liteVersion, height)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, userID, settings.DarkTheme, settings.SelectedChapterFood, settings.SelectedChapterMoney, settings.SelectedChapterTime, settings.LiteVersion, settings.Height); err != nil {
		return fmt.Errorf("insert settings: %w", err)
	}

	return nil
}

func (r *Repository) GetMetricsSettingsByUserID(ctx context.Context, userID int64) (string, error) {
	row := r.db.QueryRowContext(ctx, `SELECT metricsSettings FROM settings WHERE usersId = ?`, userID)

	var value sql.NullString
	if err := row.Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("get metrics settings by user id: %w", err)
	}

	return value.String, nil
}

func (r *Repository) UpsertMetricsSettings(ctx context.Context, userID int64, value string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE settings
		SET metricsSettings = ?
		WHERE usersId = ?
	`, value, userID)
	if err != nil {
		return fmt.Errorf("update metrics settings: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update metrics settings rows affected: %w", err)
	}
	if rowsAffected > 0 {
		return nil
	}

	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO settings (usersId, metricsSettings)
		VALUES (?, ?)
	`, userID, value); err != nil {
		return fmt.Errorf("insert metrics settings: %w", err)
	}

	return nil
}

func (r *Repository) GetUserAdminAndName(ctx context.Context, userID int64) (bool, string, error) {
	row := r.db.QueryRowContext(ctx, `SELECT username, COALESCE(isAdmin, 0) FROM users WHERE id = ?`, userID)

	var username string
	var isAdmin bool
	if err := row.Scan(&username, &isAdmin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("get user admin and name: %w", err)
	}

	return isAdmin, username, nil
}

func nullableInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}

	result := value.Int64
	return &result
}
