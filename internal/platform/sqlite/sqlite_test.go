package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestOpenConfiguresAndPingsDatabase(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
}

func TestCloseTruncatesWALArtifactsOnCleanShutdown(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "nested", "test.db")

	db, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if _, err := db.SQL().ExecContext(context.Background(), `CREATE TABLE sample (id INTEGER PRIMARY KEY, title TEXT NOT NULL);`); err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}
	if _, err := db.SQL().ExecContext(context.Background(), `INSERT INTO sample(title) VALUES('ok')`); err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if _, err := os.Stat(dbPath + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("wal file state error = %v, want not exists", err)
	}
	if _, err := os.Stat(dbPath + "-shm"); !os.IsNotExist(err) {
		t.Fatalf("shm file state error = %v, want not exists", err)
	}
}

func TestCloseTruncatesWALWithLiveReadPoolConnections(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if _, err := db.Write().ExecContext(context.Background(), `CREATE TABLE sample (id INTEGER PRIMARY KEY, title TEXT NOT NULL);`); err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}
	if _, err := db.Write().ExecContext(context.Background(), `INSERT INTO sample(title) VALUES('ok')`); err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}

	// Force the read pool to actually open readPoolSize physical connections (a sequential
	// QueryRowContext loop could keep reusing one idle connection) so Close() has real,
	// idle-but-open connections to reckon with, not just a theoretical pool.
	var wg sync.WaitGroup
	for i := 0; i < readPoolSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var count int
			_ = db.Read().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sample`).Scan(&count)
		}()
	}
	wg.Wait()

	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if _, err := os.Stat(dbPath + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("wal file state error = %v, want not exists", err)
	}
	if _, err := os.Stat(dbPath + "-shm"); !os.IsNotExist(err) {
		t.Fatalf("shm file state error = %v, want not exists", err)
	}
}

func TestReadPoolNotBlockedByOpenWriteTransaction(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Write().ExecContext(context.Background(), `CREATE TABLE sample (id INTEGER PRIMARY KEY, title TEXT NOT NULL);`); err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}
	if _, err := db.Write().ExecContext(context.Background(), `INSERT INTO sample(title) VALUES('seed')`); err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}

	tx, err := db.Write().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	if _, err := tx.ExecContext(context.Background(), `INSERT INTO sample(title) VALUES('uncommitted')`); err != nil {
		t.Fatalf("tx ExecContext() error = %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	readCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var count int
	if err := db.Read().QueryRowContext(readCtx, `SELECT COUNT(*) FROM sample`).Scan(&count); err != nil {
		t.Fatalf("read pool query blocked by open write tx: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1 (uncommitted row must not be visible)", count)
	}
}

func TestReadPoolConnectionsAllEnforceForeignKeys(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	schema := `
		CREATE TABLE parent (id INTEGER PRIMARY KEY);
		CREATE TABLE child (id INTEGER PRIMARY KEY, parentId INTEGER NOT NULL, FOREIGN KEY (parentId) REFERENCES parent(id));
	`
	if _, err := db.Write().ExecContext(context.Background(), schema); err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}

	// Pin readPoolSize distinct physical connections at once (a plain sequential QueryRowContext
	// loop could keep reusing the same idle connection and never exercise the others), then check
	// every one of them individually applied the DSN's foreign_keys pragma on connection open.
	var wg sync.WaitGroup
	errs := make([]error, readPoolSize)
	for i := 0; i < readPoolSize; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			tx, err := db.Read().BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				errs[index] = err
				return
			}
			defer func() { _ = tx.Rollback() }()

			var enabled int
			if err := tx.QueryRowContext(context.Background(), `PRAGMA foreign_keys`).Scan(&enabled); err != nil {
				errs[index] = err
				return
			}
			if enabled != 1 {
				errs[index] = fmt.Errorf("foreign_keys = %d, want 1", enabled)
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("connection %d: %v", i, err)
		}
	}

	if _, err := db.Write().ExecContext(context.Background(), `INSERT INTO child (id, parentId) VALUES (1, 999)`); err == nil {
		t.Fatal("insert with dangling parentId succeeded, want foreign key violation")
	}
}

func TestApplyMigrationsCreatesSchemaMigrationsAndRunsFiles(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	migrationsDir := filepath.Join(tempDir, "migrations")

	if err := os.MkdirAll(migrationsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	migrationSQL := `CREATE TABLE sample (id INTEGER PRIMARY KEY, title TEXT NOT NULL);`
	if err := os.WriteFile(filepath.Join(migrationsDir, "000001_create_sample.sql"), []byte(migrationSQL), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	db, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := ApplyMigrations(context.Background(), db.SQL(), migrationsDir); err != nil {
		t.Fatalf("ApplyMigrations() error = %v", err)
	}

	var count int
	if err := db.SQL().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, "000001_create_sample").Scan(&count); err != nil {
		t.Fatalf("QueryRowContext() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	if _, err := db.SQL().ExecContext(context.Background(), `INSERT INTO sample(title) VALUES('ok')`); err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}
}
