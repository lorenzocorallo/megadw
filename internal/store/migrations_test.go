package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenUpgradesVersionOneDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "phase-c.sqlite3")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(initialMigrationSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(1, ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open version-1 database: %v", err)
	}
	defer upgraded.Close()
	var count int
	if err := upgraded.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version IN (1, 2)`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("applied migration count = %d, want 2", count)
	}
	if _, err := upgraded.Exec(`UPDATE download_jobs SET quota_retry_index = 0 WHERE 1 = 0`); err != nil {
		t.Fatalf("migration 2 quota columns unavailable: %v", err)
	}
}
