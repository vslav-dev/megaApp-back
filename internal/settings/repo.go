package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"megaapp-back/internal/platform/sqlite"
)

type Repository struct {
	db    *sql.DB
	write sqlite.WriteDB
}

// txRunner is satisfied by both *sql.DB and *sql.Tx, so the read below can run either as a plain
// query or as part of an idempotency transaction without duplicating its SQL.
type txRunner interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func NewRepository(read *sql.DB, write sqlite.WriteDB) *Repository {
	return &Repository{db: read, write: write}
}

// Get returns the raw stored JSON payload for a namespace, or "" if the user has never saved
// anything in it yet — callers merge that onto the namespace's defaults.
func (r *Repository) Get(ctx context.Context, userID int64, namespace string) (string, error) {
	return r.get(ctx, r.db, userID, namespace)
}

func (r *Repository) get(ctx context.Context, tx txRunner, userID int64, namespace string) (string, error) {
	row := tx.QueryRowContext(ctx, `SELECT payload FROM userSettings WHERE usersId = ? AND namespace = ?`, userID, namespace)

	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("get settings namespace: %w", err)
	}

	return payload, nil
}

// MergeFields reads the namespace's current payload inside tx, merges the given top-level fields
// into it (each field replaces the whole value under its key, never merging deeper), and upserts
// the result back. Returns the merged payload plus the write's timestamp (millisecond precision,
// so two PUTs committed moments apart from the same user get distinguishable values — the caller
// forwards it to WS broadcast so recipients can drop an out-of-order message) so the caller can
// round-trip validate the payload before committing. The same method serves both a single-field
// auto-save PUT and a multi-field batched PUT — merging N≥1 fields covering every field of a
// namespace is equivalent to a full replace.
func (r *Repository) MergeFields(ctx context.Context, tx *sql.Tx, userID int64, namespace string, fields map[string]json.RawMessage) (string, int64, error) {
	current, err := r.get(ctx, tx, userID, namespace)
	if err != nil {
		return "", 0, err
	}

	merged := map[string]json.RawMessage{}
	if current != "" {
		if err := json.Unmarshal([]byte(current), &merged); err != nil {
			return "", 0, fmt.Errorf("unmarshal stored settings namespace: %w", err)
		}
	}
	for key, value := range fields {
		merged[key] = value
	}

	mergedJSON, err := json.Marshal(merged)
	if err != nil {
		return "", 0, fmt.Errorf("marshal merged settings namespace: %w", err)
	}

	updatedAtMillis, err := r.upsert(ctx, tx, userID, namespace, string(mergedJSON))
	if err != nil {
		return "", 0, err
	}

	return string(mergedJSON), updatedAtMillis, nil
}

func (r *Repository) upsert(ctx context.Context, tx *sql.Tx, userID int64, namespace string, payload string) (int64, error) {
	now := time.Now().UTC()
	nowText := now.Format(time.RFC3339Nano)

	result, err := tx.ExecContext(ctx, `
		UPDATE userSettings SET payload = ?, updatedAt = ? WHERE usersId = ? AND namespace = ?
	`, payload, nowText, userID, namespace)
	if err != nil {
		return 0, fmt.Errorf("update settings namespace: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("update settings namespace rows affected: %w", err)
	}
	if rowsAffected > 0 {
		return now.UnixMilli(), nil
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO userSettings (usersId, namespace, payload, updatedAt) VALUES (?, ?, ?, ?)
	`, userID, namespace, payload, nowText); err != nil {
		return 0, fmt.Errorf("insert settings namespace: %w", err)
	}

	return now.UnixMilli(), nil
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
