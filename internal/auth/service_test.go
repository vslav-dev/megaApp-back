package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"megaapp-back/internal/platform/sqlite"

	_ "modernc.org/sqlite"
)

func TestServiceRegisterLoginAuthenticateAndRevoke(t *testing.T) {
	db := openAuthTestDB(t)
	service := NewService(NewRepository(db, sqlite.WriteDB{DB: db}), SessionConfig{TTL: time.Hour, RenewWindow: 10 * time.Minute})

	userID, err := service.Register(context.Background(), "alice", "password123")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := service.Register(context.Background(), "alice", "password123"); err != ErrUsernameTaken {
		t.Fatalf("Register() duplicate error = %v, want ErrUsernameTaken", err)
	}

	login, err := service.Login(context.Background(), "alice", "password123")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if login.Cookie == "" {
		t.Fatal("Login() returned empty cookie")
	}

	identity, err := service.AuthenticateCookie(context.Background(), login.Cookie)
	if err != nil {
		t.Fatalf("AuthenticateCookie() error = %v", err)
	}
	if identity.UserID != userID || identity.Username != "alice" {
		t.Fatalf("identity = %+v", identity)
	}

	if err := service.Revoke(context.Background(), identity.SessionID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if _, err := service.AuthenticateCookie(context.Background(), login.Cookie); err != ErrInvalidSession {
		t.Fatalf("AuthenticateCookie() revoked error = %v, want ErrInvalidSession", err)
	}
}

func openAuthTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT,
			hashedPassword TEXT,
			isAdmin BOOLEAN
		);
		CREATE TABLE auth_sessions (
			id TEXT PRIMARY KEY,
			secretHash BLOB NOT NULL,
			userId INTEGER NOT NULL,
			createdAt INTEGER NOT NULL,
			expiresAt INTEGER NOT NULL,
			renewedAt INTEGER NOT NULL,
			revokedAt INTEGER DEFAULT NULL
		);
	`); err != nil {
		_ = db.Close()
		t.Fatalf("Exec() error = %v", err)
	}

	t.Cleanup(func() { _ = db.Close() })
	return db
}
