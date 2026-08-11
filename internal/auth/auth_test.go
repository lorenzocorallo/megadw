package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/lorenzocorallo/megadw/internal/auth"
	"github.com/lorenzocorallo/megadw/internal/store"
)

func TestPasswordAndSessionRoundTrip(t *testing.T) {
	hash, err := auth.HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if auth.VerifyPassword("wrong password", hash) || !auth.VerifyPassword("correct horse battery", hash) {
		t.Fatal("password verification result is incorrect")
	}
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "megadw.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := auth.NewManager(db)
	principal, token, err := manager.Setup(context.Background(), "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if principal.Username != "admin" || token == "" {
		t.Fatalf("setup = %#v token=%q", principal, token)
	}
	digest := auth.TokenDigest(token)
	var stored []byte
	if err := db.QueryRow(`SELECT token_digest FROM sessions`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) == token || string(stored) == string([]byte(token)) {
		t.Fatal("raw session token was persisted")
	}
	if string(stored) != string(digest[:]) {
		t.Fatalf("session digest does not match SHA-256 token digest: stored=%x want=%x token=%q", stored, digest, token)
	}
	request := httptest.NewRequest("GET", "http://example.test/", nil)
	request.AddCookie(httpCookie(auth.SessionCookieName, token))
	got, ok := manager.Principal(context.Background(), request)
	if !ok || got.ID != principal.ID {
		t.Fatalf("principal = %#v, authenticated = %v", got, ok)
	}
}

func TestCreatingSessionPrunesExpiredSessions(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "megadw.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := auth.NewManager(db)
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	manager.Now = func() time.Time { return now }
	if _, _, err := manager.Setup(context.Background(), "admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(auth.SessionTTL + time.Minute)
	if _, _, err := manager.Login(context.Background(), "admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("stored sessions = %d, want only the new unexpired session", count)
	}
}

func httpCookie(name, value string) *http.Cookie {
	return &http.Cookie{Name: name, Value: value}
}
