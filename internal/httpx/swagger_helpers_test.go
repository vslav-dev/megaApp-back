package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newSwaggerTestApp(t *testing.T) *App {
	t.Helper()

	cfg := appTestConfig(t.TempDir())
	prepareAppTestFiles(t, cfg)

	app, err := NewApp(context.Background(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	return app
}

func getFromApp(app *App, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	app.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}
