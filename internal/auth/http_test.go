package auth

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"megaapp-back/internal/httpx/legacy"
	"megaapp-back/internal/platform/sqlite"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

func TestAuthEndpointsAndMiddleware(t *testing.T) {
	db := openAuthHTTPTestDB(t)
	service := NewService(NewRepository(db, sqlite.WriteDB{DB: db}), SessionConfig{})
	handler := NewHandler(service)
	router := chi.NewRouter()
	RegisterRoutes(router, handler)
	server := httptest.NewServer(router)
	defer server.Close()

	registerResponse := doJSONRequest(t, server.URL+"/api/auth/register", map[string]string{"username": "alice", "password": "password123"})
	if registerResponse.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, want 201", registerResponse.StatusCode)
	}
	_ = registerResponse.Body.Close()

	loginResponse := doJSONRequest(t, server.URL+"/api/auth/login", map[string]string{"username": "alice", "password": "password123"})
	if loginResponse.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", loginResponse.StatusCode)
	}
	cookies := loginResponse.Cookies()
	if len(cookies) != 1 || cookies[0].Name != SessionCookieName || !cookies[0].HttpOnly {
		t.Fatalf("login cookies = %+v", cookies)
	}
	_ = loginResponse.Body.Close()

	verifyRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/auth/session", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	verifyRequest.AddCookie(cookies[0])
	verifyResponse, err := http.DefaultClient.Do(verifyRequest)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if verifyResponse.StatusCode != http.StatusOK {
		t.Fatalf("session status = %d, want 200", verifyResponse.StatusCode)
	}
	_ = verifyResponse.Body.Close()

	protectedHandler := Middleware(service)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := IdentityFromContext(r.Context())
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		legacy.WriteJSON(w, http.StatusOK, map[string]any{"id": identity.UserID, "username": identity.Username})
	}))
	protectedRequest := httptest.NewRequest(http.MethodGet, "/protected", nil)
	protectedRequest.AddCookie(cookies[0])
	protectedRecorder := httptest.NewRecorder()
	protectedHandler.ServeHTTP(protectedRecorder, protectedRequest)
	if protectedRecorder.Code != http.StatusOK {
		t.Fatalf("protected status = %d, want 200", protectedRecorder.Code)
	}

	logoutRequest, err := http.NewRequest(http.MethodPost, server.URL+"/api/auth/logout", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	logoutRequest.Header.Set("Content-Type", "application/json")
	logoutRequest.AddCookie(cookies[0])
	logoutResponse, err := http.DefaultClient.Do(logoutRequest)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if logoutResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", logoutResponse.StatusCode)
	}
	_ = logoutResponse.Body.Close()

	protectedRecorder = httptest.NewRecorder()
	protectedHandler.ServeHTTP(protectedRecorder, protectedRequest)
	if protectedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("revoked status = %d, want 401", protectedRecorder.Code)
	}
}

func doJSONRequest(t *testing.T, url string, payload any) *http.Response {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	response, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("http.Post() error = %v", err)
	}
	return response
}

func openAuthHTTPTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT, hashedPassword TEXT, isAdmin BOOLEAN);
		CREATE TABLE auth_sessions (id TEXT PRIMARY KEY, secretHash BLOB NOT NULL, userId INTEGER NOT NULL, createdAt INTEGER NOT NULL, expiresAt INTEGER NOT NULL, renewedAt INTEGER NOT NULL, revokedAt INTEGER DEFAULT NULL);
	`); err != nil {
		_ = db.Close()
		t.Fatalf("Exec() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
