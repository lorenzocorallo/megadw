package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	passwordVersion = "argon2id-v1"
	passwordTime    = 3
	passwordMemory  = 64 * 1024
	passwordThreads = 2
	passwordKeyLen  = 32
	passwordSaltLen = 16
)

// HashPassword returns a self-describing Argon2id password record. The salt
// and cost parameters are stored with the hash, never the password itself.
func HashPassword(password string) ([]byte, error) {
	if len(password) < 8 {
		return nil, fmt.Errorf("password must contain at least 8 characters")
	}
	if len(password) > 1024 {
		return nil, fmt.Errorf("password is too long")
	}
	salt := make([]byte, passwordSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, passwordTime, passwordMemory, passwordThreads, passwordKeyLen)
	record := strings.Join([]string{
		passwordVersion,
		strconv.FormatUint(uint64(passwordMemory), 10),
		strconv.FormatUint(uint64(passwordTime), 10),
		strconv.FormatUint(uint64(passwordThreads), 10),
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	}, "$")
	return []byte(record), nil
}

// VerifyPassword verifies a password record using constant-time comparison.
// Malformed records fail closed.
func VerifyPassword(password string, encoded []byte) bool {
	parts := strings.Split(string(encoded), "$")
	if len(parts) != 6 || parts[0] != passwordVersion {
		return false
	}
	memory, err1 := strconv.ParseUint(parts[1], 10, 32)
	timeCost, err2 := strconv.ParseUint(parts[2], 10, 32)
	threads, err3 := strconv.ParseUint(parts[3], 10, 8)
	salt, err4 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err5 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || len(salt) < 8 || len(want) == 0 || memory == 0 || timeCost == 0 || threads == 0 {
		return false
	}
	if memory > 1024*1024 || timeCost > 10 || threads > 32 || len(password) > 1024 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, uint32(timeCost), uint32(memory), uint8(threads), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ValidateUsername applies the local administrator username bounds.
func ValidateUsername(username string) error {
	if strings.TrimSpace(username) != username || username == "" {
		return errors.New("username must not be empty or padded")
	}
	if len(username) > 64 {
		return errors.New("username is too long")
	}
	for _, r := range username {
		if r == 0 || r == '\n' || r == '\r' || r == '\t' {
			return errors.New("username contains an invalid character")
		}
	}
	return nil
}
