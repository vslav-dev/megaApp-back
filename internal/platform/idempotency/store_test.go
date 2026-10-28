package idempotency

import (
	"context"
	"database/sql"
	"testing"

	"megaapp-back/internal/platform/sqlite"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE syncOperations (id TEXT PRIMARY KEY, userId INTEGER NOT NULL, createdAt TEXT NOT NULL, resultJSON TEXT NOT NULL);
	`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func TestStoreFindMissThenRecordThenHit(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(sqlite.WriteDB{DB: db})
	ctx := context.Background()

	tx, err := store.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	if _, found, err := store.Find(ctx, tx, 1, "op-1"); err != nil || found {
		t.Fatalf("Find() = (found=%v, err=%v), want not found", found, err)
	}
	if err := store.Record(ctx, tx, 1, "op-1", `{"result":true}`); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	tx2, err := store.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer func() { _ = tx2.Rollback() }()
	resultJSON, found, err := store.Find(ctx, tx2, 1, "op-1")
	if err != nil || !found {
		t.Fatalf("Find() = (found=%v, err=%v), want found", found, err)
	}
	if resultJSON != `{"result":true}` {
		t.Fatalf("resultJSON = %q, want {\"result\":true}", resultJSON)
	}
}

func TestStoreFindScopedToUser(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(sqlite.WriteDB{DB: db})
	ctx := context.Background()

	tx, err := store.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	if err := store.Record(ctx, tx, 1, "op-1", `{"result":true}`); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	tx2, err := store.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer func() { _ = tx2.Rollback() }()
	if _, found, err := store.Find(ctx, tx2, 2, "op-1"); err != nil || found {
		t.Fatalf("Find() for other user = (found=%v, err=%v), want not found", found, err)
	}
}
