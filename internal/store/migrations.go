package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const initialMigrationVersion = 1

// initialMigrationSQL is kept in the binary because megad is intentionally a
// single self-contained executable. The checked-in SQL file is the readable
// source counterpart used by operators and migration tooling.
const initialMigrationSQL = `
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value_json TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL COLLATE NOCASE UNIQUE,
    password_hash BLOB NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
    token_digest BLOB PRIMARY KEY CHECK(length(token_digest) = 32),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_user_id_idx ON sessions(user_id);
CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions(expires_at);
CREATE TABLE IF NOT EXISTS mega_accounts (
    id TEXT PRIMARY KEY,
    label TEXT NOT NULL,
    email TEXT NOT NULL COLLATE NOCASE,
    credential_ciphertext BLOB,
    session_ciphertext BLOB,
    status TEXT NOT NULL DEFAULT 'unknown',
    default_for_downloads INTEGER NOT NULL DEFAULT 0 CHECK(default_for_downloads IN (0, 1)),
    last_checked_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS proxy_profiles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    host TEXT NOT NULL,
    port INTEGER NOT NULL,
    username TEXT,
    password_ciphertext BLOB,
    timeout_seconds INTEGER NOT NULL DEFAULT 15,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS download_jobs (
    id TEXT PRIMARY KEY,
    source_kind TEXT NOT NULL,
    source_handle TEXT NOT NULL,
    source_key_ciphertext BLOB NOT NULL,
    source_selected_path TEXT NOT NULL DEFAULT '',
    source_selected_node TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL,
    total_bytes INTEGER NOT NULL CHECK(total_bytes >= 0),
    account_id TEXT,
    proxy_id TEXT,
    destination_subdirectory TEXT NOT NULL DEFAULT '',
    complete_root TEXT NOT NULL,
    incomplete_root TEXT NOT NULL,
    state TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY(account_id) REFERENCES mega_accounts(id) ON DELETE SET NULL,
    FOREIGN KEY(proxy_id) REFERENCES proxy_profiles(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS download_jobs_state_idx ON download_jobs(state);
CREATE INDEX IF NOT EXISTS download_jobs_created_at_idx ON download_jobs(created_at);
CREATE TABLE IF NOT EXISTS download_files (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES download_jobs(id) ON DELETE CASCADE,
    remote_node_id TEXT NOT NULL,
    remote_path TEXT NOT NULL,
    final_relative_path TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK(size_bytes >= 0),
    segment_size_bytes INTEGER NOT NULL,
    segment_count INTEGER NOT NULL CHECK(segment_count >= 0),
    completed_segments_bitmap BLOB NOT NULL DEFAULT X'',
    bytes_committed INTEGER NOT NULL DEFAULT 0 CHECK(bytes_committed >= 0),
    file_key_ciphertext BLOB NOT NULL,
    payload_url_ciphertext BLOB,
    payload_context TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL,
    last_error_code TEXT NOT NULL DEFAULT '',
    last_error_message TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    completed_at TEXT
);
CREATE INDEX IF NOT EXISTS download_files_job_id_idx ON download_files(job_id);
CREATE INDEX IF NOT EXISTS download_files_state_idx ON download_files(state);
CREATE TABLE IF NOT EXISTS download_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL REFERENCES download_jobs(id) ON DELETE CASCADE,
    file_id TEXT,
    kind TEXT NOT NULL,
    message TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY(file_id) REFERENCES download_files(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS download_events_job_id_idx ON download_events(job_id, id DESC);
`

func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
        version INTEGER PRIMARY KEY,
        applied_at TEXT NOT NULL
    )`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	var applied bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)`, initialMigrationVersion).Scan(&applied); err != nil {
		return fmt.Errorf("check migration %d: %w", initialMigrationVersion, err)
	}
	if applied {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", initialMigrationVersion, err)
	}
	if _, err := tx.ExecContext(ctx, initialMigrationSQL); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("apply migration %d: %w", initialMigrationVersion, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, initialMigrationVersion, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record migration %d: %w", initialMigrationVersion, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", initialMigrationVersion, err)
	}
	return nil
}
