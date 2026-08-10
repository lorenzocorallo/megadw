package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const applicationSecretSize = 32

// SecretStore encrypts values that must survive a restart but must never be
// queryable as plaintext from SQLite. The key itself lives in a mode-0600
// file outside the database.
type SecretStore struct {
	key [applicationSecretSize]byte
}

// NewSecretStore creates an in-memory AES-256-GCM store from a key. The key is
// copied so callers can safely reuse their input buffer.
func NewSecretStore(key []byte) (*SecretStore, error) {
	if len(key) != applicationSecretSize {
		return nil, fmt.Errorf("application secret must be %d bytes", applicationSecretSize)
	}
	store := &SecretStore{}
	copy(store.key[:], key)
	return store, nil
}

// OpenSecretStore loads the application key or creates it atomically on first
// launch. Existing symlinks and non-regular files are rejected.
func OpenSecretStore(path string) (*SecretStore, error) {
	if path == "" {
		return nil, fmt.Errorf("secret key path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create secret key directory: %w", err)
	}

	info, err := os.Lstat(path)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("secret key path is not a regular file")
		}
		key, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("read application secret: %w", readErr)
		}
		if len(key) != applicationSecretSize {
			return nil, fmt.Errorf("application secret must be %d bytes", applicationSecretSize)
		}
		if chmodErr := os.Chmod(path, 0o600); chmodErr != nil {
			return nil, fmt.Errorf("secure application secret permissions: %w", chmodErr)
		}
		return NewSecretStore(key)
	case !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("inspect application secret: %w", err)
	}

	var key [applicationSecretSize]byte
	if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
		return nil, fmt.Errorf("generate application secret: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return OpenSecretStore(path)
		}
		return nil, fmt.Errorf("create application secret: %w", err)
	}
	if _, err := file.Write(key[:]); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write application secret: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("sync application secret: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close application secret: %w", err)
	}
	// Sync the directory entry after the key contents. Without this second
	// barrier, a power loss can preserve the database while losing the newly
	// created key name, making every encrypted source/account/proxy secret
	// permanently unreadable on restart.
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("open application secret directory: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return nil, fmt.Errorf("sync application secret directory: %w", syncErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close application secret directory: %w", closeErr)
	}
	return NewSecretStore(key[:])
}

// Encrypt returns nonce || AES-GCM ciphertext. A fresh nonce is generated for
// every value, so equal secrets do not produce equal database blobs.
func (s *SecretStore) Encrypt(plaintext []byte) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("secret store is nil")
	}
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate encryption nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt authenticates and decrypts a nonce || AES-GCM ciphertext blob.
func (s *SecretStore) Decrypt(ciphertext []byte) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("secret store is nil")
	}
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	if len(ciphertext) < gcm.NonceSize()+gcm.Overhead() {
		return nil, fmt.Errorf("encrypted value is truncated")
	}
	nonce, payload := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, payload, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt encrypted value: %w", err)
	}
	return plaintext, nil
}
