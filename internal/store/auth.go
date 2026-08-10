package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrAdminExists = errors.New("administrator already exists")
	ErrNotFound    = errors.New("record not found")
	ErrRecordInUse = errors.New("record is selected by a download")
)

type UserRecord struct {
	ID           string
	Username     string
	PasswordHash []byte
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type SessionRecord struct {
	UserID    string
	Digest    []byte
	ExpiresAt time.Time
	CreatedAt time.Time
}

func (d *DB) HasUsers(ctx context.Context) (bool, error) {
	var count int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	return count > 0, nil
}

func (d *DB) CreateAdmin(ctx context.Context, username string, passwordHash []byte, now time.Time) (UserRecord, error) {
	id, err := newID()
	if err != nil {
		return UserRecord{}, err
	}
	if len(passwordHash) == 0 {
		return UserRecord{}, fmt.Errorf("password hash is required")
	}
	when := now.UTC()
	record := UserRecord{
		ID:           id,
		Username:     username,
		PasswordHash: append([]byte(nil), passwordHash...),
		CreatedAt:    when,
		UpdatedAt:    when,
	}
	err = d.WithTx(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
			return fmt.Errorf("count users: %w", err)
		}
		if count != 0 {
			return ErrAdminExists
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO users(id, username, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, record.ID, record.Username, record.PasswordHash, record.CreatedAt.Format(time.RFC3339Nano), record.UpdatedAt.Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("insert administrator: %w", err)
		}
		return nil
	})
	if err != nil {
		if isConstraintError(err) {
			return UserRecord{}, ErrAdminExists
		}
		return UserRecord{}, err
	}
	return record, nil
}

func (d *DB) UserByUsername(ctx context.Context, username string) (UserRecord, error) {
	return scanUser(d.QueryRowContext(ctx, `SELECT id, username, password_hash, created_at, updated_at FROM users WHERE username = ?`, username))
}

func (d *DB) UserByID(ctx context.Context, id string) (UserRecord, error) {
	return scanUser(d.QueryRowContext(ctx, `SELECT id, username, password_hash, created_at, updated_at FROM users WHERE id = ?`, id))
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (UserRecord, error) {
	var record UserRecord
	var created, updated string
	if err := row.Scan(&record.ID, &record.Username, &record.PasswordHash, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserRecord{}, ErrNotFound
		}
		return UserRecord{}, fmt.Errorf("read user: %w", err)
	}
	var err error
	record.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return UserRecord{}, fmt.Errorf("parse user created_at: %w", err)
	}
	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return UserRecord{}, fmt.Errorf("parse user updated_at: %w", err)
	}
	record.PasswordHash = append([]byte(nil), record.PasswordHash...)
	return record, nil
}

func (d *DB) InsertSession(ctx context.Context, session SessionRecord) error {
	if len(session.Digest) != 32 {
		return fmt.Errorf("session digest must be 32 bytes")
	}
	_, err := d.ExecContext(ctx, `INSERT INTO sessions(token_digest, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`, session.Digest, session.UserID, session.ExpiresAt.UTC().Format(time.RFC3339Nano), session.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (d *DB) UserForSession(ctx context.Context, digest []byte, now time.Time) (UserRecord, error) {
	if len(digest) != 32 {
		return UserRecord{}, ErrNotFound
	}
	var record UserRecord
	var created, updated string
	err := d.QueryRowContext(ctx, `SELECT u.id, u.username, u.password_hash, u.created_at, u.updated_at
        FROM sessions s JOIN users u ON u.id = s.user_id
        WHERE s.token_digest = ? AND s.expires_at > ?`, digest, now.UTC().Format(time.RFC3339Nano)).Scan(&record.ID, &record.Username, &record.PasswordHash, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return UserRecord{}, ErrNotFound
	}
	if err != nil {
		return UserRecord{}, fmt.Errorf("read session user: %w", err)
	}
	var parseErr error
	record.CreatedAt, parseErr = time.Parse(time.RFC3339Nano, created)
	if parseErr != nil {
		return UserRecord{}, fmt.Errorf("parse session user created_at: %w", parseErr)
	}
	record.UpdatedAt, parseErr = time.Parse(time.RFC3339Nano, updated)
	if parseErr != nil {
		return UserRecord{}, fmt.Errorf("parse session user updated_at: %w", parseErr)
	}
	record.PasswordHash = append([]byte(nil), record.PasswordHash...)
	return record, nil
}

func (d *DB) DeleteSession(ctx context.Context, digest []byte) error {
	if len(digest) != 32 {
		return nil
	}
	if _, err := d.ExecContext(ctx, `DELETE FROM sessions WHERE token_digest = ?`, digest); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (d *DB) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	if _, err := d.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}

func isConstraintError(err error) bool {
	return err != nil && (containsError(err, "constraint") || containsError(err, "UNIQUE"))
}

func containsError(err error, needle string) bool {
	return strings.Contains(strings.ToLower(fmt.Sprint(err)), strings.ToLower(needle))
}
