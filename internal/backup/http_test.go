package backup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"megaapp-back/internal/platform/sqlite"

	"github.com/go-chi/chi/v5"
)

func TestDebugRouteRunsBackupJob(t *testing.T) {
	db, _ := openBackupTestDB(t)
	seedBackupTestDB(t, db)
	uploader := &fakeUploader{}
	service := NewService(sqlite.WriteDB{DB: db}, Config{
		DatabaseName:   "megaapp",
		DatabaseEnv:    "test",
		BackupsDir:     filepath.Join(t.TempDir(), "backups"),
		StorageEnabled: true,
	}, fixedClock{now: time.Date(2026, time.July, 20, 10, 30, 0, 0, time.UTC)}, nil, uploader)

	router := chi.NewRouter()
	RegisterDebugRoutes(router, NewDebugHandler(service, nil))
	server := httptest.NewServer(router)
	defer server.Close()

	response, err := http.Post(server.URL+"/api/debug/run-backup-job", "", nil)
	if err != nil {
		t.Fatalf("http.Post() error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", response.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if jobID, _ := payload["jobId"].(string); jobID == "" {
		t.Fatalf("jobId = %v, want non-empty string", payload["jobId"])
	}

	deadline := time.Now().Add(2 * time.Second)
	for uploader.Key() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if uploader.Key() == "" {
		t.Fatal("backup job did not run in background within 2s")
	}
}
