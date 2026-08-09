package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/lorenzocorallo/megadw/internal/store"
)

const (
	SessionCookieName = "megad_session"
	SessionTTL        = 24 * time.Hour
)

type Manager struct {
	DB            *store.DB
	CookieName    string
	TTL           time.Duration
	SecureCookies bool
	Now           func() time.Time
}

type Principal struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

func NewManager(db *store.DB) *Manager {
	return &Manager{DB: db, CookieName: SessionCookieName, TTL: SessionTTL, Now: time.Now}
}

func (m *Manager) now() time.Time {
	if m != nil && m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) Setup(ctx context.Context, username, password string) (Principal, string, error) {
	if err := ValidateUsername(username); err != nil {
		return Principal{}, "", err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return Principal{}, "", err
	}
	user, err := m.DB.CreateAdmin(ctx, username, hash, m.now())
	if err != nil {
		return Principal{}, "", err
	}
	token, err := m.createSession(ctx, user.ID)
	if err != nil {
		return Principal{}, "", err
	}
	return Principal{ID: user.ID, Username: user.Username}, token, nil
}

func (m *Manager) Login(ctx context.Context, username, password string) (Principal, string, error) {
	user, err := m.DB.UserByUsername(ctx, username)
	if err != nil {
		return Principal{}, "", err
	}
	if !VerifyPassword(password, user.PasswordHash) {
		return Principal{}, "", fmt.Errorf("invalid username or password")
	}
	token, err := m.createSession(ctx, user.ID)
	if err != nil {
		return Principal{}, "", err
	}
	return Principal{ID: user.ID, Username: user.Username}, token, nil
}

func (m *Manager) createSession(ctx context.Context, userID string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	digest := sha256.Sum256(raw[:])
	now := m.now().UTC()
	ttl := m.TTL
	if ttl <= 0 {
		ttl = SessionTTL
	}
	if err := m.DB.InsertSession(ctx, store.SessionRecord{UserID: userID, Digest: digest[:], CreatedAt: now, ExpiresAt: now.Add(ttl)}); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func (m *Manager) Principal(ctx context.Context, request *http.Request) (Principal, bool) {
	if m == nil || m.DB == nil || request == nil {
		return Principal{}, false
	}
	cookieName := m.CookieName
	if cookieName == "" {
		cookieName = SessionCookieName
	}
	cookie, err := request.Cookie(cookieName)
	if err != nil || cookie.Value == "" {
		return Principal{}, false
	}
	digest := TokenDigest(cookie.Value)
	user, err := m.DB.UserForSession(ctx, digest[:], m.now())
	if err != nil {
		return Principal{}, false
	}
	return Principal{ID: user.ID, Username: user.Username}, true
}

func (m *Manager) Logout(ctx context.Context, request *http.Request) error {
	if request == nil {
		return nil
	}
	cookieName := m.CookieName
	if cookieName == "" {
		cookieName = SessionCookieName
	}
	cookie, err := request.Cookie(cookieName)
	if err == nil && cookie.Value != "" {
		digest := TokenDigest(cookie.Value)
		if err := m.DB.DeleteSession(ctx, digest[:]); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) SetCookie(writer http.ResponseWriter, request *http.Request, token string) {
	secure := m.SecureCookies || request != nil && request.TLS != nil
	name := m.CookieName
	if name == "" {
		name = SessionCookieName
	}
	ttl := m.TTL
	if ttl <= 0 {
		ttl = SessionTTL
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  m.now().Add(ttl),
	})
}

func (m *Manager) ClearCookie(writer http.ResponseWriter, request *http.Request) {
	secure := m.SecureCookies || request != nil && request.TLS != nil
	name := m.CookieName
	if name == "" {
		name = SessionCookieName
	}
	http.SetCookie(writer, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}

func TokenDigest(token string) [32]byte {
	if raw, err := base64.RawURLEncoding.DecodeString(token); err == nil && len(raw) == 32 {
		return sha256.Sum256(raw)
	}
	return sha256.Sum256([]byte(token))
}
