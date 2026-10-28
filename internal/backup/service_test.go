package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"megaapp-back/internal/platform/sqlite"

	_ "modernc.org/sqlite"
)

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

type fakeUploader struct {
	mu           sync.Mutex
	key          string
	contentType  string
	storageClass string
	archiveBytes []byte
	err          error
}

func (u *fakeUploader) UploadFile(_ context.Context, key string, filePath string, contentType string, storageClass string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.key = key
	u.contentType = contentType
	u.storageClass = storageClass
	if u.err != nil {
		return u.err
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	u.archiveBytes = data
	return nil
}

func (u *fakeUploader) Key() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.key
}

func TestServiceRunCreatesUploadsAndCleansBackup(t *testing.T) {
	db, dbPath := openBackupTestDB(t)
	seedBackupTestDB(t, db)
	backupsDir := filepath.Join(t.TempDir(), "backups")
	uploader := &fakeUploader{}
	service := NewService(sqlite.WriteDB{DB: db}, Config{
		DatabaseName:   "megaapp",
		DatabaseEnv:    "test",
		BackupsDir:     backupsDir,
		StorageEnabled: true,
		StorageClass:   "STANDARD_IA",
	}, fixedClock{now: time.Date(2026, time.July, 20, 10, 30, 0, 0, time.UTC)}, nil, uploader)

	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.UploadedKey != "megaapp-test-2026-07-20T10-30-00Z.zip" {
		t.Fatalf("UploadedKey = %q", result.UploadedKey)
	}
	if !result.CleanedUp {
		t.Fatal("CleanedUp = false, want true")
	}
	if uploader.key != result.UploadedKey || uploader.contentType != "application/zip" || uploader.storageClass != "STANDARD_IA" {
		t.Fatalf("uploader = %+v", uploader)
	}
	if len(uploader.archiveBytes) == 0 {
		t.Fatal("archiveBytes = empty")
	}

	zipReader, err := zip.NewReader(bytes.NewReader(uploader.archiveBytes), int64(len(uploader.archiveBytes)))
	if err != nil {
		t.Fatalf("zip.NewReader() error = %v", err)
	}
	if len(zipReader.File) != 1 {
		t.Fatalf("zip entries = %d, want 1", len(zipReader.File))
	}
	if zipReader.File[0].Name != result.SnapshotFileName {
		t.Fatalf("zip entry name = %q, want %q", zipReader.File[0].Name, result.SnapshotFileName)
	}

	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("backupsDir entries = %d, want 0", len(entries))
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("dbPath stat error = %v", err)
	}
}

func TestServiceRunCleansFilesAfterUploadFailure(t *testing.T) {
	db, _ := openBackupTestDB(t)
	seedBackupTestDB(t, db)
	backupsDir := filepath.Join(t.TempDir(), "backups")
	service := NewService(sqlite.WriteDB{DB: db}, Config{
		DatabaseName:   "megaapp",
		DatabaseEnv:    "test",
		BackupsDir:     backupsDir,
		StorageEnabled: true,
	}, fixedClock{now: time.Date(2026, time.July, 20, 10, 30, 0, 0, time.UTC)}, nil, &fakeUploader{err: errors.New("upload failed")})

	_, err := service.Run(context.Background())
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("backupsDir entries = %d, want 0", len(entries))
	}
}

func TestServiceRejectsDisabledStorage(t *testing.T) {
	db, _ := openBackupTestDB(t)
	service := NewService(sqlite.WriteDB{DB: db}, Config{StorageEnabled: false}, fixedClock{now: time.Now().UTC()}, nil, &fakeUploader{})
	if _, err := service.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil, want error")
	}
}

func openBackupTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode = WAL; CREATE TABLE sample (id INTEGER PRIMARY KEY, value TEXT);`); err != nil {
		_ = db.Close()
		t.Fatalf("Exec() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, dbPath
}

func seedBackupTestDB(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO sample (value) VALUES ('hello'), ('world')`); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
}
