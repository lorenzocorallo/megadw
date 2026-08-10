package mega

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lorenzocorallo/megadw/internal/store"
)

// AccountLifecycle owns the bounded account-session persistence boundary. It
// has no goroutines and never constructs the upstream full Mega client.
type AccountLifecycle struct {
	Client  *Client
	DB      *store.DB
	Secrets *store.SecretStore
	Now     func() time.Time
}

func NewAccountLifecycle(client *Client, database *store.DB, secrets *store.SecretStore) *AccountLifecycle {
	return &AccountLifecycle{Client: client, DB: database, Secrets: secrets, Now: func() time.Time { return time.Now().UTC() }}
}

// ClientFor validates a stored session and returns a session-bound project
// client. A rejected session causes at most one password login. Successful
// reauthentication replaces the encrypted session in one database
// transaction; the plaintext session never leaves this method.
func (l *AccountLifecycle) ClientFor(ctx context.Context, accountID string) (*Client, error) {
	if l == nil || l.Client == nil || l.DB == nil || l.Secrets == nil {
		return nil, fmt.Errorf("account lifecycle is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	account, err := l.DB.GetMegaAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	credentialCiphertext, sessionCiphertext, err := l.DB.MegaAccountSecrets(ctx, accountID)
	if err != nil {
		return nil, err
	}

	var storedSession []byte
	if len(sessionCiphertext) > 0 {
		storedSession, err = l.Secrets.Decrypt(sessionCiphertext)
		if err != nil {
			// A damaged stored session is not a credential failure. If an
			// encrypted password exists, EnsureSession will perform its one
			// controlled password fallback.
			storedSession = nil
		}
	}
	var password []byte
	if len(credentialCiphertext) > 0 {
		password, _ = l.Secrets.Decrypt(credentialCiphertext)
	}
	result, ensureErr := l.Client.EnsureSession(ctx, account.Email, storedSession, password)
	clearBytes(storedSession)
	clearBytes(password)
	if ensureErr != nil {
		if errors.Is(ensureErr, ErrReauthRequired) {
			// Clearing the rejected session prevents repeated use of known
			// invalid material. The update also records the stable state shown
			// by the account status endpoint.
			_ = l.DB.ReplaceMegaAccountSession(ctx, accountID, nil, "requires_auth", l.now())
			return nil, ErrReauthRequired
		}
		return nil, ensureErr
	}

	if result.Reauthenticated {
		encrypted, encryptErr := l.Secrets.Encrypt([]byte(result.SessionID))
		if encryptErr != nil {
			return nil, fmt.Errorf("protect account session")
		}
		if err := l.DB.ReplaceMegaAccountSession(ctx, accountID, encrypted, "active", l.now()); err != nil {
			return nil, fmt.Errorf("persist account session")
		}
	} else if err := l.DB.MarkMegaAccountChecked(ctx, accountID, "active", l.now()); err != nil {
		return nil, err
	}
	return l.Client.WithSession(result.SessionID), nil
}

// Test validates the account through the same authentication-only lifecycle
// used by downloads and returns the single bounded uq health result.
func (l *AccountLifecycle) Test(ctx context.Context, accountID string) (AccountHealth, error) {
	if l == nil || l.Client == nil || l.DB == nil || l.Secrets == nil {
		return AccountHealth{}, fmt.Errorf("account lifecycle is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	account, err := l.DB.GetMegaAccount(ctx, accountID)
	if err != nil {
		return AccountHealth{}, err
	}
	credentialCiphertext, sessionCiphertext, err := l.DB.MegaAccountSecrets(ctx, accountID)
	if err != nil {
		return AccountHealth{}, err
	}
	var storedSession, password []byte
	if len(sessionCiphertext) > 0 {
		storedSession, _ = l.Secrets.Decrypt(sessionCiphertext)
	}
	if len(credentialCiphertext) > 0 {
		password, _ = l.Secrets.Decrypt(credentialCiphertext)
	}
	result, ensureErr := l.Client.EnsureSession(ctx, account.Email, storedSession, password)
	clearBytes(storedSession)
	clearBytes(password)
	if ensureErr != nil {
		if errors.Is(ensureErr, ErrReauthRequired) {
			_ = l.DB.ReplaceMegaAccountSession(ctx, accountID, nil, "requires_auth", l.now())
			return AccountHealth{}, ErrReauthRequired
		}
		_ = l.DB.MarkMegaAccountChecked(ctx, accountID, "error", l.now())
		return AccountHealth{}, ensureErr
	}
	if result.Reauthenticated {
		encrypted, encryptErr := l.Secrets.Encrypt([]byte(result.SessionID))
		if encryptErr != nil {
			return AccountHealth{}, fmt.Errorf("protect account session")
		}
		if err := l.DB.ReplaceMegaAccountSession(ctx, accountID, encrypted, "active", l.now()); err != nil {
			return AccountHealth{}, fmt.Errorf("persist account session")
		}
	} else if err := l.DB.MarkMegaAccountChecked(ctx, accountID, "active", l.now()); err != nil {
		return AccountHealth{}, err
	}
	return result.Health, nil
}

func (l *AccountLifecycle) now() time.Time {
	if l != nil && l.Now != nil {
		return l.Now().UTC()
	}
	return time.Now().UTC()
}

func clearBytes(value []byte) {
	clear(value)
}
