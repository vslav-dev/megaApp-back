package ws

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"megaapp-back/internal/auth"
	"megaapp-back/internal/platform/sqlite"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	_ "modernc.org/sqlite"
)

func TestConnectSendsSyncStatusAndSupportsHeartbeat(t *testing.T) {
	authService := openWSTestAuthService(t)
	hub := NewHub(50*time.Millisecond, NewSyncState())
	defer func() { _ = hub.Close() }()
	router := chi.NewRouter()
	RegisterRoutes(router, NewHandler(authService, hub))
	server := httptest.NewServer(router)
	defer server.Close()

	userID, err := authService.Register(context.Background(), "alice", "password123")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	hub.syncState.Set(userID, 123)
	session, err := authService.CreateSession(context.Background(), userID)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	conn := dialWS(t, server.URL+"/api/ws?clientId=tab-a", session.Cookie)
	defer func() { _ = conn.Close() }()

	var syncStatus map[string]any
	if err := conn.ReadJSON(&syncStatus); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
	if syncStatus["type"] != "SYNC_STATUS" {
		t.Fatalf("type = %v, want SYNC_STATUS", syncStatus["type"])
	}
	payload, ok := syncStatus["payload"].(map[string]any)
	if !ok || payload["userDataLastModifiedTs"] != float64(123) {
		t.Fatalf("payload = %#v", syncStatus["payload"])
	}

	var ping map[string]any
	if err := conn.ReadJSON(&ping); err != nil || ping["type"] != "PING" {
		t.Fatalf("ping = %#v, error = %v", ping, err)
	}
	if err := conn.WriteJSON(map[string]string{"type": "PONG"}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
}

func TestConnectRejectsMissingSessionAndForeignOrigin(t *testing.T) {
	authService := openWSTestAuthService(t)
	hub := NewHub(time.Second, NewSyncState())
	defer func() { _ = hub.Close() }()
	router := chi.NewRouter()
	RegisterRoutes(router, NewHandler(authService, hub))
	server := httptest.NewServer(router)
	defer server.Close()
	userID, err := authService.Register(context.Background(), "alice", "password123")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	session, err := authService.CreateSession(context.Background(), userID)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	wsURL := "ws" + server.URL[len("http"):] + "/api/ws"
	_, response, err := websocket.DefaultDialer.Dial(wsURL, http.Header{
		"Cookie": []string{auth.SessionCookieName + "=" + session.Cookie},
		"Origin": []string{"https://evil.example"},
	})
	if err == nil {
		t.Fatal("Dial() error = nil, want error")
	}
	if response == nil || response.StatusCode != http.StatusForbidden {
		statusCode := 0
		if response != nil {
			statusCode = response.StatusCode
		}
		t.Fatalf("status = %d, want 403", statusCode)
	}
}

func TestCloseSessionDisconnectsAllTabs(t *testing.T) {
	authService := openWSTestAuthService(t)
	hub := NewHub(time.Second, NewSyncState())
	defer func() { _ = hub.Close() }()
	router := chi.NewRouter()
	RegisterRoutes(router, NewHandler(authService, hub))
	server := httptest.NewServer(router)
	defer server.Close()

	userID, err := authService.Register(context.Background(), "alice", "password123")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	session, err := authService.CreateSession(context.Background(), userID)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	connA := dialWS(t, server.URL+"/api/ws?clientId=tab-a", session.Cookie)
	connB := dialWS(t, server.URL+"/api/ws?clientId=tab-b", session.Cookie)
	defer func() { _ = connA.Close() }()
	defer func() { _ = connB.Close() }()
	drainInitialMessage(t, connA)
	drainInitialMessage(t, connB)

	hub.CloseSession(session.Identity.SessionID, 4001, "Session revoked")
	for _, conn := range []*websocket.Conn{connA, connB} {
		_, _, err := conn.ReadMessage()
		if websocket.IsCloseError(err, 4001) {
			continue
		}
		t.Fatalf("ReadMessage() error = %v, want close 4001", err)
	}
}

func openWSTestAuthService(t *testing.T) *auth.Service {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT, hashedPassword TEXT, isAdmin BOOLEAN);
		CREATE TABLE auth_sessions (id TEXT PRIMARY KEY, secretHash BLOB NOT NULL, userId INTEGER NOT NULL, createdAt INTEGER NOT NULL, expiresAt INTEGER NOT NULL, renewedAt INTEGER NOT NULL, revokedAt INTEGER DEFAULT NULL);
	`); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	return auth.NewService(auth.NewRepository(db, sqlite.WriteDB{DB: db}), auth.SessionConfig{})
}

func dialWS(t *testing.T, httpURL string, sessionCookie string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + httpURL[len("http"):]
	parsed, err := url.Parse(httpURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	origin := "http://" + parsed.Host
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{
		"Cookie": []string{auth.SessionCookieName + "=" + sessionCookie},
		"Origin": []string{origin},
	})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	return conn
}

func drainInitialMessage(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var message map[string]any
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
	if message["type"] != "SYNC_STATUS" {
		bytes, _ := json.Marshal(message)
		t.Fatalf("initial message = %s", bytes)
	}
}
