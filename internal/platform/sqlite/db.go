package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const readPoolSize = 4

// WriteDB is the single serialized write connection. It's a distinct type from the read pool's
// plain *sql.DB — both would otherwise be structurally identical, and the entire point of
// splitting them is that mixing them up (e.g. a new write method copy-pasted from a neighboring
// read method) should fail to compile instead of silently writing through the read pool.
type WriteDB struct {
	*sql.DB
}

type DB struct {
	write *sql.DB
	read  *sql.DB
}

func Open(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite directory: %w", err)
	}

	dsn := connDSN(path)

	write, err := openPool(dsn, 1)
	if err != nil {
		return nil, fmt.Errorf("open sqlite write pool: %w", err)
	}

	read, err := openPool(dsn, readPoolSize)
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("open sqlite read pool: %w", err)
	}

	wrapper := &DB{write: write, read: read}

	if err := wrapper.configure(ctx); err != nil {
		_ = read.Close()
		_ = write.Close()
		return nil, err
	}

	return wrapper, nil
}

func openPool(dsn string, maxConns int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	return db, nil
}

// connDSN carries pragmas in the connection string, not as a one-off ExecContext call, because
// journal_mode is the only one of these that's sticky in the database file — foreign_keys,
// synchronous and busy_timeout are per-connection and must be reapplied whenever the pool opens
// a new physical connection (which happens lazily, outside our control, once MaxOpenConns > 1).
func connDSN(path string) string {
	values := url.Values{}
	values.Add("_pragma", "foreign_keys(1)")
	values.Add("_pragma", "busy_timeout(5000)")
	values.Add("_pragma", "synchronous(NORMAL)")
	values.Add("_pragma", "journal_mode(WAL)")
	return path + "?" + values.Encode()
}

// SQL returns the write connection, kept for callers (migrations, tests) that need a single
// *sql.DB and don't care about the read/write split.
func (d *DB) SQL() *sql.DB {
	return d.write
}

func (d *DB) Write() WriteDB {
	return WriteDB{d.write}
}

func (d *DB) Read() *sql.DB {
	return d.read
}

func (d *DB) PingContext(ctx context.Context) error {
	if err := d.write.PingContext(ctx); err != nil {
		return err
	}
	return d.read.PingContext(ctx)
}

func (d *DB) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Close the read pool first so no lingering read connection/transaction can hold back the
	// WAL checkpoint below.
	if err := d.read.Close(); err != nil {
		return fmt.Errorf("close sqlite read pool: %w", err)
	}

	if _, err := d.write.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);"); err != nil {
		return fmt.Errorf("checkpoint sqlite wal: %w", err)
	}

	if _, err := d.write.ExecContext(ctx, "PRAGMA optimize;"); err != nil {
		return fmt.Errorf("optimize sqlite: %w", err)
	}

	if err := d.write.Close(); err != nil {
		return fmt.Errorf("close sqlite write pool: %w", err)
	}

	return nil
}

func (d *DB) configure(ctx context.Context) error {
	pragmaTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := d.write.PingContext(pragmaTimeout); err != nil {
		return fmt.Errorf("ping sqlite write pool: %w", err)
	}
	if err := d.read.PingContext(pragmaTimeout); err != nil {
		return fmt.Errorf("ping sqlite read pool: %w", err)
	}

	return nil
}
