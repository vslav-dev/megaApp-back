package settings

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"megaapp-back/internal/auth"
	"megaapp-back/internal/platform/idempotency"
	"megaapp-back/internal/platform/sqlite"
	"megaapp-back/internal/ws"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
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

		CREATE TABLE auth_sessions (id TEXT PRIMARY KEY, secretHash BLOB NOT NULL, userId INTEGER NOT NULL, createdAt TEXT NOT NULL, expiresAt TEXT NOT NULL, renewedAt TEXT NOT NULL, revokedAt TEXT);

		CREATE TABLE userSettings (
			usersId INTEGER NOT NULL,
			namespace TEXT NOT NULL,
			payload TEXT NOT NULL,
			updatedAt TEXT NOT NULL,
			PRIMARY KEY (usersId, namespace)
		);

		CREATE TABLE syncOperations (id TEXT PRIMARY KEY, userId INTEGER NOT NULL, createdAt TEXT NOT NULL, resultJSON TEXT NOT NULL);
	`); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	authRepo := auth.NewRepository(db, sqlite.WriteDB{DB: db})
	authService := auth.NewService(authRepo, auth.SessionConfig{})
	userID, err := authService.Register(t.Context(), "alice", "password123")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := db.Exec(`UPDATE users SET isAdmin = ? WHERE id = ?`, true, userID); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	settingsService := NewService(NewRepository(db, sqlite.WriteDB{DB: db}), idempotency.NewStore(sqlite.WriteDB{DB: db}))
	hub := ws.NewHub(30*time.Second, nil)
	defer func() { _ = hub.Close() }()
	settingsHandler := NewHandler(settingsService, NewWSRealtimePublisher(hub))

	session, err := authService.CreateSession(t.Context(), userID)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	router := chi.NewRouter()
	RegisterRoutes(router, authService, settingsHandler)
	server := httptest.NewServer(router)
	defer server.Close()

	doRequest := func(method, path string, body []byte) *http.Response {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		} else {
			reader = bytes.NewReader([]byte{})
		}
		request, err := http.NewRequest(method, server.URL+path, reader)
		if err != nil {
			t.Fatalf("http.NewRequest() error = %v", err)
		}
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.Cookie})
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		return response
	}

	getResponse := doRequest(http.MethodGet, "/api/settings/core", nil)
	if getResponse.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", getResponse.StatusCode)
	}
	var coreResponse map[string]json.RawMessage
	if err := json.NewDecoder(getResponse.Body).Decode(&coreResponse); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	_ = getResponse.Body.Close()
	var userName string
	_ = json.Unmarshal(coreResponse["userName"], &userName)
	if userName != "alice" {
		t.Fatalf("userName = %q, want alice", userName)
	}
	var isAdmin bool
	_ = json.Unmarshal(coreResponse["isUserAdmin"], &isAdmin)
	if !isAdmin {
		t.Fatal("isUserAdmin = false, want true")
	}

	putBody, err := json.Marshal(map[string]any{"selectedChapterFood": true, "operationId": "op-1"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	putResponse := doRequest(http.MethodPut, "/api/settings/core", putBody)
	if putResponse.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", putResponse.StatusCode)
	}
	_ = putResponse.Body.Close()

	retryBody, err := json.Marshal(map[string]any{"selectedChapterMoney": true, "operationId": "op-1"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	retryResponse := doRequest(http.MethodPut, "/api/settings/core", retryBody)
	if retryResponse.StatusCode != http.StatusOK {
		t.Fatalf("retry PUT status = %d, want 200", retryResponse.StatusCode)
	}
	_ = retryResponse.Body.Close()

	afterRetryResponse := doRequest(http.MethodGet, "/api/settings/core", nil)
	var afterRetrySettings CoreSettings
	if err := json.NewDecoder(afterRetryResponse.Body).Decode(&afterRetrySettings); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	_ = afterRetryResponse.Body.Close()
	if afterRetrySettings.SelectedChapterMoney {
		t.Fatal("SelectedChapterMoney = true, want false — retry with the same operationId must be replayed, not reapplied")
	}
	if !afterRetrySettings.SelectedChapterFood {
		t.Fatal("SelectedChapterFood = false, want true")
	}

	noOperationIDBody, err := json.Marshal(map[string]any{"selectedChapterFood": true})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	noOperationIDResponse := doRequest(http.MethodPut, "/api/settings/core", noOperationIDBody)
	if noOperationIDResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT without operationId status = %d, want 400", noOperationIDResponse.StatusCode)
	}
	_ = noOperationIDResponse.Body.Close()

	invalidBody, err := json.Marshal(map[string]any{"isUserAdmin": true, "operationId": "op-2"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	invalidResponse := doRequest(http.MethodPut, "/api/settings/core", invalidBody)
	if invalidResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid PUT status = %d, want 400", invalidResponse.StatusCode)
	}
	_ = invalidResponse.Body.Close()

	unknownNamespaceResponse := doRequest(http.MethodGet, "/api/settings/not-a-namespace", nil)
	if unknownNamespaceResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("GET unknown namespace status = %d, want 404", unknownNamespaceResponse.StatusCode)
	}
	_ = unknownNamespaceResponse.Body.Close()

	metricsGetResponse := doRequest(http.MethodGet, "/api/settings/metrics", nil)
	if metricsGetResponse.StatusCode != http.StatusOK {
		t.Fatalf("metrics GET status = %d, want 200", metricsGetResponse.StatusCode)
	}
	var defaultMetrics MetricsSettings
	if err := json.NewDecoder(metricsGetResponse.Body).Decode(&defaultMetrics); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	_ = metricsGetResponse.Body.Close()
	if defaultMetrics.CardSize.WidthPx != 304 {
		t.Fatalf("default CardSize.WidthPx = %v, want 304", defaultMetrics.CardSize.WidthPx)
	}

	metricsPutBody, err := json.Marshal(map[string]any{"syncCrosshairEnabled": true, "operationId": "op-3"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	metricsPutResponse := doRequest(http.MethodPut, "/api/settings/metrics", metricsPutBody)
	if metricsPutResponse.StatusCode != http.StatusOK {
		t.Fatalf("metrics PUT status = %d, want 200", metricsPutResponse.StatusCode)
	}
	_ = metricsPutResponse.Body.Close()

	metricsGetAfterPutResponse := doRequest(http.MethodGet, "/api/settings/metrics", nil)
	var storedMetrics MetricsSettings
	if err := json.NewDecoder(metricsGetAfterPutResponse.Body).Decode(&storedMetrics); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	_ = metricsGetAfterPutResponse.Body.Close()
	if !storedMetrics.SyncCrosshairEnabled {
		t.Fatal("SyncCrosshairEnabled = false, want true")
	}

	invalidMetricsBody, err := json.Marshal(map[string]any{"cardSize": "not-an-object", "operationId": "op-4"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	invalidMetricsResponse := doRequest(http.MethodPut, "/api/settings/metrics", invalidMetricsBody)
	if invalidMetricsResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid metrics PUT status = %d, want 400", invalidMetricsResponse.StatusCode)
	}
	_ = invalidMetricsResponse.Body.Close()
}

func TestPutBroadcastsToOtherClientsButNotTheInitiator(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`
		CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT, hashedPassword TEXT, isAdmin BOOLEAN);
		CREATE TABLE auth_sessions (id TEXT PRIMARY KEY, secretHash BLOB NOT NULL, userId INTEGER NOT NULL, createdAt TEXT NOT NULL, expiresAt TEXT NOT NULL, renewedAt TEXT NOT NULL, revokedAt TEXT);
		CREATE TABLE userSettings (usersId INTEGER NOT NULL, namespace TEXT NOT NULL, payload TEXT NOT NULL, updatedAt TEXT NOT NULL, PRIMARY KEY (usersId, namespace));
		CREATE TABLE syncOperations (id TEXT PRIMARY KEY, userId INTEGER NOT NULL, createdAt TEXT NOT NULL, resultJSON TEXT NOT NULL);
	`); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	authRepo := auth.NewRepository(db, sqlite.WriteDB{DB: db})
	authService := auth.NewService(authRepo, auth.SessionConfig{})
	userID, err := authService.Register(t.Context(), "alice", "password123")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	session, err := authService.CreateSession(t.Context(), userID)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	settingsService := NewService(NewRepository(db, sqlite.WriteDB{DB: db}), idempotency.NewStore(sqlite.WriteDB{DB: db}))
	hub := ws.NewHub(30*time.Second, nil)
	defer func() { _ = hub.Close() }()
	settingsHandler := NewHandler(settingsService, NewWSRealtimePublisher(hub))
	wsHandler := ws.NewHandler(authService, hub)

	router := chi.NewRouter()
	RegisterRoutes(router, authService, settingsHandler)
	ws.RegisterRoutes(router, wsHandler)
	server := httptest.NewServer(router)
	defer server.Close()

	connA := dialSettingsWS(t, server.URL+"/api/ws?clientId=tab-a", session.Cookie)
	defer func() { _ = connA.Close() }()
	connB := dialSettingsWS(t, server.URL+"/api/ws?clientId=tab-b", session.Cookie)
	defer func() { _ = connB.Close() }()
	drainSettingsWSMessage(t, connA) // initial SYNC_STATUS
	drainSettingsWSMessage(t, connB)

	body, err := json.Marshal(map[string]any{"selectedChapterFood": true, "operationId": "op-1"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/settings/core", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.Cookie})
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Client-ID", "tab-a")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", response.StatusCode)
	}
	_ = response.Body.Close()

	// connB (a different clientId) should receive the broadcast.
	_ = connB.SetReadDeadline(time.Now().Add(time.Second))
	var receiverMessage map[string]any
	if err := connB.ReadJSON(&receiverMessage); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
	if receiverMessage["type"] != "SETTINGS_UPDATED" {
		t.Fatalf("ws type = %v, want SETTINGS_UPDATED", receiverMessage["type"])
	}
	payload, _ := receiverMessage["payload"].(map[string]any)
	if payload["namespace"] != "core" {
		t.Fatalf("payload.namespace = %v, want core", payload["namespace"])
	}
	updatedAt, ok := payload["updatedAt"].(float64)
	if !ok || updatedAt <= 0 {
		t.Fatalf("payload.updatedAt = %v, want a positive timestamp", payload["updatedAt"])
	}

	// connA (the PUT's own clientId) must not receive its own echo.
	_ = connA.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	var senderMessage map[string]any
	if err := connA.ReadJSON(&senderMessage); err == nil {
		t.Fatalf("sender received unexpected ws message: %+v", senderMessage)
	}
}

func dialSettingsWS(t *testing.T, httpURL string, sessionCookie string) *websocket.Conn {
	t.Helper()

	wsURL := "ws" + httpURL[len("http"):]
	parsed, err := url.Parse(httpURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	headers := http.Header{
		"Cookie": {auth.SessionCookieName + "=" + sessionCookie},
		"Origin": {"http://" + parsed.Host},
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	return conn
}

func drainSettingsWSMessage(t *testing.T, conn *websocket.Conn) {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var message map[string]any
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
}
