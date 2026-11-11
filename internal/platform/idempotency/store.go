package idempotency

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"megaapp-back/internal/platform/sqlite"
)

// Store lets a write operation be replayed safely: the caller checks Find inside its own
// transaction before doing any business writes, and calls Record in that same transaction
// right before committing. A retry with the same operationID then short-circuits to the
// original result instead of re-applying the operation. Always backed by the write connection —
// idempotency correctness depends on this transaction being part of the single serialized write
// path, never the read pool.
type Store struct {
	db sqlite.WriteDB
}

func NewStore(db sqlite.WriteDB) *Store {
	return &Store{db: db}
}

func (s *Store) BeginTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin sync operation tx: %w", err)
	}
	return tx, nil
}

func (s *Store) Find(ctx context.Context, tx *sql.Tx, userID int64, operationID string) (resultJSON string, found bool, err error) {
	row := tx.QueryRowContext(ctx, `SELECT resultJSON FROM syncOperations WHERE id = ? AND userId = ?`, operationID, userID)
	if err := row.Scan(&resultJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("find sync operation: %w", err)
	}
	return resultJSON, true, nil
}

func (s *Store) Record(ctx context.Context, tx *sql.Tx, userID int64, operationID string, resultJSON string) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO syncOperations (id, userId, createdAt, resultJSON) VALUES (?, ?, ?, ?)
	`, operationID, userID, time.Now().UTC().Format(time.RFC3339), resultJSON); err != nil {
		return fmt.Errorf("record sync operation: %w", err)
	}
	return nil
}
