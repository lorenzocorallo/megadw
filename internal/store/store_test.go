package store_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorenzocorallo/megadw/internal/settings"
	"github.com/lorenzocorallo/megadw/internal/store"
)

func TestOpenConfiguresWALAndMigrates(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "state", "megad.sqlite3")
	db, err := store.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var journalMode, foreignKeys, busyTimeout, synchronous string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA synchronous`).Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(journalMode, "wal") || foreignKeys != "1" || busyTimeout != "5000" || synchronous != "1" {
		t.Fatalf("pragmas = journal=%q foreign_keys=%q busy_timeout=%q synchronous=%q", journalMode, foreignKeys, busyTimeout, synchronous)
	}
	var migrationCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 1 {
		t.Fatalf("migration count = %d", migrationCount)
	}
}

func TestMemoryDatabaseUsesOneConnection(t *testing.T) {
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO settings(key, value_json, updated_at) VALUES ('test', '{}', ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'test'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d", count)
	}
}

func TestSecretStoreEncryptsAndRejectsCorruption(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "secret.key")
	first, err := store.OpenSecretStore(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("secret mode = %o", info.Mode().Perm())
	}
	ciphertext, err := first.Encrypt([]byte("private source key"))
	if err != nil {
		t.Fatal(err)
	}
	if string(ciphertext) == "private source key" || strings.Contains(string(ciphertext), "private source key") {
		t.Fatal("ciphertext contains plaintext")
	}
	second, err := store.OpenSecretStore(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := second.Decrypt(ciphertext)
	if err != nil || string(plaintext) != "private source key" {
		t.Fatalf("decrypt = %q, error = %v", plaintext, err)
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := second.Decrypt(ciphertext); err == nil {
		t.Fatal("corrupted ciphertext decrypted successfully")
	}
}

func TestSettingsRestartAndAtomicValidation(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "megad.sqlite3")
	open := func() (*store.DB, *settings.Service) {
		db, err := store.Open(context.Background(), databasePath)
		if err != nil {
			t.Fatal(err)
		}
		service, err := settings.NewService(db)
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		return db, service
	}
	db, service := open()
	value, err := service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	value.UI.Theme = "dark"
	value.Paths.CompleteRoot = filepath.Join(t.TempDir(), "complete")
	if err := service.Update(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	invalid := value
	invalid.Paths.CompleteRoot = "../escape"
	if err := service.Update(context.Background(), invalid); err == nil {
		t.Fatal("invalid settings were accepted")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, service = open()
	defer db.Close()
	reloaded, err := service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.UI.Theme != "dark" || reloaded.Paths.CompleteRoot != value.Paths.CompleteRoot {
		t.Fatalf("reloaded settings = %#v", reloaded)
	}
}
