package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"megaapp-back/internal/platform/sqlite"
)

type User struct {
	ID             int64
	Username       string
	HashedPassword string
	IsAdmin        bool
}

type Session struct {
	ID         string
	SecretHash []byte
	UserID     int64
	CreatedAt  time.Time
	ExpiresAt  time.Time
	RenewedAt  time.Time
	RevokedAt  *time.Time
}

type Repository struct {
	db    *sql.DB
	write sqlite.WriteDB
}

func NewRepository(read *sql.DB, write sqlite.WriteDB) *Repository {
	return &Repository{db: read, write: write}
}

func (r *Repository) CreateUser(ctx context.Context, username string, hashedPassword string) (int64, error) {
	result, err := r.write.ExecContext(ctx, `INSERT INTO users (username, hashedPassword, isAdmin) VALUES (?, ?, 0)`, username, hashedPassword)
	if err != nil {
		return 0, fmt.Errorf("create user: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("create user last insert id: %w", err)
	}

	return id, nil
}

func (r *Repository) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id, username, hashedPassword, isAdmin FROM users WHERE username = ?`, username)
	return scanUser(row)
}

func (r *Repository) GetUserByID(ctx context.Context, userID int64) (*User, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id, username, hashedPassword, isAdmin FROM users WHERE id = ?`, userID)
	return scanUser(row)
}

func (r *Repository) ListAdminUserIDs(ctx context.Context) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM users WHERE isAdmin = 1`)
	if err != nil {
		return nil, fmt.Errorf("list admin user ids: %w", err)
	}
	defer rows.Close()

	var userIDs []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("scan admin user id: %w", err)
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin user ids: %w", err)
	}
	return userIDs, nil
}

func (r *Repository) CreateSession(ctx context.Context, session Session) error {
	_, err := r.write.ExecContext(ctx, `
		INSERT INTO auth_sessions (id, secretHash, userId, createdAt, expiresAt, renewedAt)
		VALUES (?, ?, ?, ?, ?, ?)
	`, session.ID, session.SecretHash, session.UserID, session.CreatedAt.Unix(), session.ExpiresAt.Unix(), session.RenewedAt.Unix())
	if err != nil {
		return fmt.Errorf("create auth session: %w", err)
	}
	return nil
}

func (r *Repository) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, secretHash, userId, createdAt, expiresAt, renewedAt, revokedAt
		FROM auth_sessions
		WHERE id = ?
	`, sessionID)
	return scanSession(row)
}

func (r *Repository) RenewSession(ctx context.Context, sessionID string, expiresAt time.Time, renewedAt time.Time) error {
	result, err := r.write.ExecContext(ctx, `
		UPDATE auth_sessions
		SET expiresAt = ?, renewedAt = ?
		WHERE id = ? AND revokedAt IS NULL
	`, expiresAt.Unix(), renewedAt.Unix(), sessionID)
	if err != nil {
		return fmt.Errorf("renew auth session: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("renew auth session rows affected: %w", err)
	}
	if changed != 1 {
		return ErrInvalidSession
	}
	return nil
}

func (r *Repository) RevokeSession(ctx context.Context, sessionID string, revokedAt time.Time) error {
	_, err := r.write.ExecContext(ctx, `
		UPDATE auth_sessions
		SET revokedAt = COALESCE(revokedAt, ?)
		WHERE id = ?
	`, revokedAt.Unix(), sessionID)
	if err != nil {
		return fmt.Errorf("revoke auth session: %w", err)
	}
	return nil
}

func (r *Repository) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := r.write.ExecContext(ctx, `DELETE FROM auth_sessions WHERE expiresAt < ?`, now.Unix())
	if err != nil {
		return fmt.Errorf("delete expired auth sessions: %w", err)
	}
	return nil
}

func scanUser(row *sql.Row) (*User, error) {
	var user User
	var isAdmin sql.NullBool
	if err := row.Scan(&user.ID, &user.Username, &user.HashedPassword, &isAdmin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get user by username: %w", err)
	}
	user.IsAdmin = isAdmin.Valid && isAdmin.Bool

	return &user, nil
}

func scanSession(row *sql.Row) (*Session, error) {
	var session Session
	var createdAt, expiresAt, renewedAt int64
	var revokedAt sql.NullInt64
	if err := row.Scan(&session.ID, &session.SecretHash, &session.UserID, &createdAt, &expiresAt, &renewedAt, &revokedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get auth session: %w", err)
	}
	session.CreatedAt = time.Unix(createdAt, 0)
	session.ExpiresAt = time.Unix(expiresAt, 0)
	session.RenewedAt = time.Unix(renewedAt, 0)
	if revokedAt.Valid {
		value := time.Unix(revokedAt.Int64, 0)
		session.RevokedAt = &value
	}
	return &session, nil
}
