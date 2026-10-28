package httpx

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The test server runs with METRICS_ENABLED=false: the app starts without Flatline settings,
// shuts down cleanly and has no metrics history route.
func TestAppStartsWithMetricsDisabled(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.MetricsEnabled = false
	cfg.FlatlineBaseURL = ""
	prepareAppTestFiles(t, cfg)

	app, err := NewApp(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	app.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/metrics/history", nil))
	if recorder.Code != http.StatusNotFound && recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/metrics/history status = %d, want 404", recorder.Code)
	}

	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}
