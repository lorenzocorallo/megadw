package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

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
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "megad.sqlite3"))
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

func httpCookie(name, value string) *http.Cookie {
	return &http.Cookie{Name: name, Value: value}
}
