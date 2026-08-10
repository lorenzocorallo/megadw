package mega

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/pbkdf2"
)

const (
	accountVersionLegacy = 1
	accountVersionModern = 2
	accountKeyIterations = 100000
	accountAuthTimeout   = 30 * time.Second
	hashcashTimeout      = 5 * time.Minute
)

// AccountHealth contains only bounded, non-secret account information used by
// the Settings status surface. It is deliberately not a filesystem model.
type AccountHealth struct {
	StorageBytes uint64 `json:"storageBytes"`
	UsedBytes    uint64 `json:"usedBytes"`
}

// AccountSession is the in-memory result of an authentication-only lifecycle.
// SessionID is never serialized by this type; callers persist it only after
// encrypting it through the application secret store.
type AccountSession struct {
	SessionID       string `json:"-"`
	Health          AccountHealth
	Restored        bool
	Reauthenticated bool
}

// LoginAccount keeps the historical convenience API for embedders. Production
// paths use LoginAccountContext so every request is owned by its caller's
// context and has a bounded lifetime.
func (c *Client) LoginAccount(email, password string) (string, error) {
	return c.LoginAccountContext(context.Background(), email, password)
}

// LoginAccountWithContext is an explicit-name alias for callers that prefer
// the conventional Go context suffix.
func (c *Client) LoginAccountWithContext(ctx context.Context, email, password string) (string, error) {
	return c.LoginAccountContext(ctx, email, password)
}

// LoginAccountContext performs only the MEGA prelogin and login commands. It
// intentionally stops before any full-client post-login initialization.
func (c *Client) LoginAccountContext(ctx context.Context, email, password string) (string, error) {
	if c == nil || c.httpClient == nil {
		return "", ErrAuthenticationFailed
	}
	ctx, cancel := boundedAccountContext(ctx)
	defer cancel()
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || password == "" {
		return "", ErrAuthenticationFailed
	}

	version, salt, err := c.prelogin(ctx, email)
	if err != nil {
		if errors.Is(err, ErrMFAUnsupported) {
			return "", err
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrAuthenticationFailed
	}

	var passkey []byte
	var userHandle string
	if version == accountVersionLegacy {
		passkey = legacyPasswordKey(password)
		userHandle, err = legacyStringHash(email, passkey)
	} else {
		derived := pbkdf2.Key([]byte(password), salt, accountKeyIterations, 2*aes.BlockSize, sha512.New)
		passkey = append([]byte(nil), derived[:aes.BlockSize]...)
		userHandle = base64.RawURLEncoding.EncodeToString(derived[aes.BlockSize:])
	}
	if err != nil || userHandle == "" {
		return "", ErrAuthenticationFailed
	}
	login := map[string]any{"a": "us", "user": email, "uh": userHandle}
	if version == accountVersionModern {
		sessionKey := make([]byte, aes.BlockSize)
		if _, err := rand.Read(sessionKey); err != nil {
			return "", ErrAuthenticationFailed
		}
		login["sek"] = base64.RawURLEncoding.EncodeToString(sessionKey)
	}
	result, err := c.authCommand(ctx, []map[string]any{login})
	if err != nil {
		if errors.Is(err, ErrMFAUnsupported) {
			return "", err
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrAuthenticationFailed
	}
	response, err := firstObject(result)
	if err != nil {
		return "", ErrAuthenticationFailed
	}
	encodedMasterKey := stringValue(response["k"])
	encodedPrivateKey := stringValue(response["privk"])
	encodedSession := stringValue(response["csid"])
	if encodedMasterKey == "" || encodedPrivateKey == "" || encodedSession == "" {
		return "", ErrAuthenticationFailed
	}
	masterKey, err := decodeURLBase64(encodedMasterKey)
	if err != nil || len(masterKey) != aes.BlockSize {
		return "", ErrAuthenticationFailed
	}
	block, err := aes.NewCipher(passkey)
	if err != nil {
		return "", ErrAuthenticationFailed
	}
	block.Decrypt(masterKey, masterKey)
	sessionID, err := decryptAccountSessionID(encodedPrivateKey, encodedSession, masterKey)
	if err != nil || sessionID == "" {
		return "", ErrAuthenticationFailed
	}
	return sessionID, nil
}

// LoginAndValidate completes a fresh login and performs the same bounded
// health command used for restored-session validation.
func (c *Client) LoginAndValidate(ctx context.Context, email, password string) (AccountSession, error) {
	ctx, cancel := boundedAccountContext(ctx)
	defer cancel()
	sessionID, err := c.LoginAccountContext(ctx, email, password)
	if err != nil {
		return AccountSession{}, err
	}
	authenticated := c.WithSession(sessionID)
	health, err := authenticated.ValidateSession(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return AccountSession{}, ctx.Err()
		}
		return AccountSession{}, ErrAuthenticationFailed
	}
	return AccountSession{SessionID: sessionID, Health: health, Reauthenticated: true}, nil
}

// EnsureSession validates a stored session once and falls back to one fresh
// password login only when MEGA explicitly rejects that session. There is no
// retry loop around password authentication.
func (c *Client) EnsureSession(ctx context.Context, email string, storedSession, password []byte) (AccountSession, error) {
	if c == nil || c.httpClient == nil {
		return AccountSession{}, ErrAuthenticationFailed
	}
	ctx, cancel := boundedAccountContext(ctx)
	defer cancel()
	if len(storedSession) > 0 {
		sessionID := string(storedSession)
		health, err := c.WithSession(sessionID).ValidateSession(ctx)
		if err == nil {
			return AccountSession{SessionID: sessionID, Health: health, Restored: true}, nil
		}
		if ctx.Err() != nil {
			return AccountSession{}, ctx.Err()
		}
		if !errors.Is(err, ErrSessionRejected) {
			return AccountSession{}, err
		}
	}
	if len(password) == 0 {
		return AccountSession{}, ErrReauthRequired
	}
	return c.LoginAndValidate(ctx, email, string(password))
}

// ValidateSession uses uq, a bounded authenticated quota/status operation. It
// never asks MEGA for the private filesystem and never starts event polling.
func (c *Client) ValidateSession(ctx context.Context) (AccountHealth, error) {
	if c == nil || c.httpClient == nil || c.session == "" {
		return AccountHealth{}, ErrSessionRejected
	}
	ctx, cancel := boundedAccountContext(ctx)
	defer cancel()
	result, err := c.command(ctx, []map[string]any{{"a": "uq", "xfer": 1, "strg": 1}}, "")
	if err != nil {
		return AccountHealth{}, err
	}
	object, err := firstObject(result)
	if err != nil {
		return AccountHealth{}, err
	}
	var health AccountHealth
	if value, ok := uintValue(object["mstrg"]); ok {
		health.StorageBytes = value
	}
	if value, ok := uintValue(object["cstrg"]); ok {
		health.UsedBytes = value
	}
	return health, nil
}

func (c *Client) prelogin(ctx context.Context, email string) (int, []byte, error) {
	result, err := c.authCommand(ctx, []map[string]any{{"a": "us0", "user": email}})
	if err != nil {
		return 0, nil, err
	}
	object, err := firstObject(result)
	if err != nil {
		return 0, nil, ErrAuthenticationFailed
	}
	versionValue, ok := intValue(object["v"])
	if !ok || versionValue == 0 {
		return 0, nil, ErrAuthenticationFailed
	}
	if versionValue != accountVersionLegacy && versionValue != accountVersionModern {
		return 0, nil, ErrAuthenticationFailed
	}
	if versionValue == accountVersionLegacy {
		return versionValue, nil, nil
	}
	saltText := stringValue(object["s"])
	if saltText == "" {
		return 0, nil, ErrAuthenticationFailed
	}
	salt, err := decodeURLBase64(saltText)
	if err != nil || len(salt) == 0 {
		return 0, nil, ErrAuthenticationFailed
	}
	return versionValue, salt, nil
}

func boundedAccountContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(accountAuthTimeout)
	if existing, hasDeadline := ctx.Deadline(); hasDeadline && !existing.After(deadline) {
		return ctx, func() {}
	}
	return context.WithDeadline(ctx, deadline)
}

func (c *Client) authCommand(ctx context.Context, payload []map[string]any) ([]any, error) {
	return c.doCommand(ctx, payload, "", true)
}

func legacyPasswordKey(password string) []byte {
	padded := zeroPad([]byte(password), 4)
	result := []byte{0x93, 0xc4, 0x67, 0xe3, 0x7d, 0xb0, 0xc7, 0xa4, 0xd1, 0xbe, 0x3f, 0x81, 0x01, 0x52, 0xcb, 0x56}
	ciphers := make([]cipher.Block, 0, (len(padded)+15)/16)
	for offset := 0; offset < len(padded); offset += aes.BlockSize {
		key := make([]byte, aes.BlockSize)
		copy(key, padded[offset:min(offset+aes.BlockSize, len(padded))])
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil
		}
		ciphers = append(ciphers, block)
	}
	for iteration := 0; iteration < 65536; iteration++ {
		for _, block := range ciphers {
			block.Encrypt(result, result)
		}
	}
	return result
}

func legacyStringHash(value string, key []byte) (string, error) {
	padded := zeroPad([]byte(value), 4)
	var words [4]uint32
	for offset := 0; offset < len(padded); offset += 4 {
		words[(offset/4)&3] ^= binary.BigEndian.Uint32(padded[offset : offset+4])
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	data := make([]byte, aes.BlockSize)
	for index, word := range words {
		binary.BigEndian.PutUint32(data[index*4:index*4+4], word)
	}
	for iteration := 0; iteration < 16384; iteration++ {
		block.Encrypt(data, data)
	}
	return base64.RawURLEncoding.EncodeToString([]byte{data[0], data[1], data[2], data[3], data[8], data[9], data[10], data[11]}), nil
}

func zeroPad(value []byte, size int) []byte {
	if size <= 0 || len(value) == 0 {
		return append([]byte(nil), value...)
	}
	if remainder := len(value) % size; remainder != 0 {
		value = append(value, make([]byte, size-remainder)...)
	}
	return value
}

func decryptAccountSessionID(encodedPrivate, encodedSession string, masterKey []byte) (string, error) {
	privateCiphertext, err := decodeURLBase64(encodedPrivate)
	if err != nil || len(privateCiphertext) == 0 || len(privateCiphertext)%aes.BlockSize != 0 {
		return "", ErrAuthenticationFailed
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return "", ErrAuthenticationFailed
	}
	privateKey := make([]byte, len(privateCiphertext))
	for offset := 0; offset < len(privateCiphertext); offset += aes.BlockSize {
		block.Decrypt(privateKey[offset:offset+aes.BlockSize], privateCiphertext[offset:offset+aes.BlockSize])
	}
	p, q, d, _, err := readRSAKey(privateKey)
	if err != nil {
		return "", ErrAuthenticationFailed
	}
	ciphertext, err := decodeURLBase64(encodedSession)
	if err != nil {
		return "", ErrAuthenticationFailed
	}
	message, _, err := readMPI(ciphertext)
	if err != nil {
		return "", ErrAuthenticationFailed
	}
	n := new(big.Int).Mul(p, q)
	plain := new(big.Int).Exp(message, d, n).Bytes()
	if len(plain) < 43 {
		return "", ErrAuthenticationFailed
	}
	return base64.RawURLEncoding.EncodeToString(plain[:43]), nil
}

func readRSAKey(value []byte) (p, q, d *big.Int, rest []byte, err error) {
	p, rest, err = readMPI(value)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	q, rest, err = readMPI(rest)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	d, rest, err = readMPI(rest)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if p.Sign() <= 0 || q.Sign() <= 0 || d.Sign() <= 0 {
		return nil, nil, nil, nil, ErrAuthenticationFailed
	}
	return p, q, d, rest, nil
}

func readMPI(value []byte) (*big.Int, []byte, error) {
	if len(value) < 2 {
		return nil, nil, ErrAuthenticationFailed
	}
	bits := int(binary.BigEndian.Uint16(value[:2]))
	length := (bits + 7) / 8
	if bits <= 0 || length <= 0 || len(value) < 2+length {
		return nil, nil, ErrAuthenticationFailed
	}
	return new(big.Int).SetBytes(value[2 : 2+length]), value[2+length:], nil
}

func intValue(value any) (int, bool) {
	var number int64
	switch value := value.(type) {
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return 0, false
		}
		number = parsed
	case float64:
		number = int64(value)
	case int64:
		number = value
	case int:
		number = int64(value)
	default:
		return 0, false
	}
	return int(number), true
}

func uintValue(value any) (uint64, bool) {
	switch value := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseUint(string(value), 10, 64)
		return parsed, err == nil
	case float64:
		if value < 0 || value > math.MaxUint64 || value != math.Trunc(value) {
			return 0, false
		}
		return uint64(value), true
	case uint64:
		return value, true
	case int64:
		if value < 0 {
			return 0, false
		}
		return uint64(value), true
	default:
		return 0, false
	}
}
