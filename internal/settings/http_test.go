package settings

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"megaapp-back/internal/auth"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

func TestSettingsEndpoints(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT,
			hashedPassword TEXT,
			isAdmin BOOLEAN
		);

		CREATE TABLE settings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			usersId INTEGER,
			darkTheme BOOLEAN,
			selectedChapterFood BOOLEAN,
			selectedChapterMoney BOOLEAN,
			selectedChapterTime BOOLEAN,
			liteVersion BOOLEAN,
			height INTEGER,
			sex TEXT DEFAULT NULL,
			birthDate TEXT DEFAULT NULL,
			activityLevel TEXT DEFAULT NULL,
			goal TEXT DEFAULT NULL,
			metricsSettings TEXT
		);
	`); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	authRepo := auth.NewRepository(db)
	tokenManager := auth.NewTokenManager("test-secret", time.Hour, 24*time.Hour)
	authService := auth.NewService(authRepo, tokenManager)
	userID, err := authService.Register(t.Context(), "alice", "password123")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := db.Exec(`UPDATE users SET isAdmin = ? WHERE id = ?`, true, userID); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	settingsService := NewService(NewRepository(db))
	settingsHandler := NewHandler(settingsService)

	tokens, err := tokenManager.Issue(auth.TokenClaims{UserID: userID, Username: "alice"})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	router := chi.NewRouter()
	RegisterRoutes(router, authService, settingsHandler)
	server := httptest.NewServer(router)
	defer server.Close()

	getRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/settings/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	getRequest.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	getResponse, err := http.DefaultClient.Do(getRequest)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if getResponse.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", getResponse.StatusCode)
	}

	var settingsResponse UserSettings
	if err := json.NewDecoder(getResponse.Body).Decode(&settingsResponse); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	_ = getResponse.Body.Close()
	if settingsResponse.UserName != "alice" {
		t.Fatalf("UserName = %q, want alice", settingsResponse.UserName)
	}
	if !settingsResponse.IsUserAdmin {
		t.Fatal("IsUserAdmin = false, want true")
	}

	putBody, err := json.Marshal(map[string]any{"darkTheme": true})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	putRequest, err := http.NewRequest(http.MethodPut, server.URL+"/api/settings/", bytes.NewReader(putBody))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	putRequest.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	putRequest.Header.Set("Content-Type", "application/json")
	putResponse, err := http.DefaultClient.Do(putRequest)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if putResponse.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", putResponse.StatusCode)
	}
	_ = putResponse.Body.Close()

	invalidBody, err := json.Marshal(map[string]any{"userName": "bob"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	invalidRequest, err := http.NewRequest(http.MethodPut, server.URL+"/api/settings/", bytes.NewReader(invalidBody))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	invalidRequest.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	invalidRequest.Header.Set("Content-Type", "application/json")
	invalidResponse, err := http.DefaultClient.Do(invalidRequest)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if invalidResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid PUT status = %d, want 400", invalidResponse.StatusCode)
	}
	_ = invalidResponse.Body.Close()

	metricsGetRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/metrics-settings/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	metricsGetRequest.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	metricsGetResponse, err := http.DefaultClient.Do(metricsGetRequest)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if metricsGetResponse.StatusCode != http.StatusOK {
		t.Fatalf("metrics-settings GET status = %d, want 200", metricsGetResponse.StatusCode)
	}
	var defaultMetricsBody json.RawMessage
	if err := json.NewDecoder(metricsGetResponse.Body).Decode(&defaultMetricsBody); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	_ = metricsGetResponse.Body.Close()
	if string(defaultMetricsBody) != "{}" {
		t.Fatalf("metrics-settings GET body = %s, want {}", defaultMetricsBody)
	}

	metricsPutBody, err := json.Marshal(map[string]any{"granularity": "hour"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	metricsPutRequest, err := http.NewRequest(http.MethodPut, server.URL+"/api/metrics-settings/", bytes.NewReader(metricsPutBody))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	metricsPutRequest.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	metricsPutRequest.Header.Set("Content-Type", "application/json")
	metricsPutResponse, err := http.DefaultClient.Do(metricsPutRequest)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if metricsPutResponse.StatusCode != http.StatusOK {
		t.Fatalf("metrics-settings PUT status = %d, want 200", metricsPutResponse.StatusCode)
	}
	_ = metricsPutResponse.Body.Close()

	metricsGetAfterPutRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/metrics-settings/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	metricsGetAfterPutRequest.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	metricsGetAfterPutResponse, err := http.DefaultClient.Do(metricsGetAfterPutRequest)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	var storedMetricsBody json.RawMessage
	if err := json.NewDecoder(metricsGetAfterPutResponse.Body).Decode(&storedMetricsBody); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	_ = metricsGetAfterPutResponse.Body.Close()
	if string(storedMetricsBody) != `{"granularity":"hour"}` {
		t.Fatalf("metrics-settings GET after PUT body = %s, want %s", storedMetricsBody, `{"granularity":"hour"}`)
	}

	invalidMetricsRequest, err := http.NewRequest(http.MethodPut, server.URL+"/api/metrics-settings/", bytes.NewReader([]byte("not json")))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	invalidMetricsRequest.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	invalidMetricsRequest.Header.Set("Content-Type", "application/json")
	invalidMetricsResponse, err := http.DefaultClient.Do(invalidMetricsRequest)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if invalidMetricsResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid metrics-settings PUT status = %d, want 400", invalidMetricsResponse.StatusCode)
	}
	_ = invalidMetricsResponse.Body.Close()
}
