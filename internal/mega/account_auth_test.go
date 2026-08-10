package mega

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lorenzocorallo/megadw/internal/network"
	"github.com/lorenzocorallo/megadw/internal/store"
	"golang.org/x/crypto/pbkdf2"
)

func TestAuthenticationOnlyProtocolSupportsLegacyAndModernAccounts(t *testing.T) {
	for _, version := range []int{accountVersionLegacy, accountVersionModern} {
		t.Run("version-"+string(rune('0'+version)), func(t *testing.T) {
			fixture := newAuthFixture(t, version, false)
			defer fixture.Close()
			result, err := fixture.Client().LoginAndValidate(context.Background(), fixture.email, fixture.password)
			if err != nil {
				t.Fatal(err)
			}
			if result.SessionID != fixture.sessionID {
				t.Fatalf("session = %q, want fixture session", result.SessionID)
			}
			if result.Health.StorageBytes != fixture.storageBytes || result.Health.UsedBytes != fixture.usedBytes {
				t.Fatalf("health = %#v", result.Health)
			}
			if fixture.loginCount.Load() != 1 || fixture.healthCount.Load() != 1 {
				t.Fatalf("login/health = %d/%d", fixture.loginCount.Load(), fixture.healthCount.Load())
			}
			fixture.assertNoPrivateLifecycle(t)
		})
	}
}

func TestAuthenticationHandlesHashcashChallenge(t *testing.T) {
	fixture := newAuthFixture(t, accountVersionModern, true)
	defer fixture.Close()
	if _, err := fixture.Client().LoginAndValidate(context.Background(), fixture.email, fixture.password); err != nil {
		t.Fatal(err)
	}
	if fixture.hashcashCount.Load() == 0 {
		t.Fatal("authentication did not answer the hashcash challenge")
	}
	fixture.assertNoPrivateLifecycle(t)
}

func TestAuthenticationExplicitlyRejectsMFAAccounts(t *testing.T) {
	fixture := newAuthFixture(t, accountVersionModern, false)
	defer fixture.Close()
	fixture.mfa = true
	if _, err := fixture.Client().LoginAccountContext(context.Background(), fixture.email, fixture.password); !errors.Is(err, ErrMFAUnsupported) {
		t.Fatalf("error = %v, want ErrMFAUnsupported", err)
	}
	fixture.assertNoPrivateLifecycle(t)
}

func TestAuthenticationSessionLifecyclePersistsRestoresAndReauthenticatesOnce(t *testing.T) {
	fixture := newAuthFixture(t, accountVersionModern, false)
	defer fixture.Close()
	root := t.TempDir()
	database, err := store.Open(context.Background(), root+"/accounts.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secrets, err := store.OpenSecretStore(root + "/secret.key")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := secrets.Encrypt([]byte(fixture.password))
	if err != nil {
		t.Fatal(err)
	}
	oldSession, err := secrets.Encrypt([]byte(fixture.sessionID))
	if err != nil {
		t.Fatal(err)
	}
	account, err := database.InsertMegaAccount(context.Background(), store.MegaAccountInput{ID: "account", Label: "Fixture", Email: fixture.email, CredentialCiphertext: credential, SessionCiphertext: oldSession, Status: "active"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := NewAccountLifecycle(fixture.Client(), database, secrets)
	if _, err := lifecycle.ClientFor(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}
	if fixture.loginCount.Load() != 0 {
		t.Fatalf("valid restored session caused %d password logins", fixture.loginCount.Load())
	}
	_, restored, err := database.MegaAccountSecrets(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(oldSession) {
		t.Fatal("valid restore rewrote the encrypted session")
	}

	fixture.revokeCurrentSession()
	if _, err := lifecycle.ClientFor(context.Background(), account.ID); err != nil {
		t.Fatal("controlled reauthentication failed: ", err)
	}
	if fixture.loginCount.Load() != 1 {
		t.Fatalf("password logins after revocation = %d, want 1", fixture.loginCount.Load())
	}
	_, replaced, err := database.MegaAccountSecrets(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(replaced) == string(oldSession) {
		t.Fatal("reauthentication did not replace session ciphertext")
	}
	if account, err = database.GetMegaAccount(context.Background(), account.ID); err != nil || account.Status != "active" {
		t.Fatalf("account after reauth = %#v, err=%v", account, err)
	}
	for index := 0; index < 3; index++ {
		health, err := lifecycle.Test(context.Background(), account.ID)
		if err != nil || health.StorageBytes != fixture.storageBytes {
			t.Fatalf("test operation %d health=%#v err=%v", index, health, err)
		}
	}
	if fixture.loginCount.Load() != 1 {
		t.Fatalf("repeated account tests caused %d password logins", fixture.loginCount.Load())
	}
	fixture.assertNoPrivateLifecycle(t)
	fixture.Client().CloseIdleConnections()
}

func TestAuthenticationRejectedSessionWithoutCredentialsIsStableReauthState(t *testing.T) {
	fixture := newAuthFixture(t, accountVersionLegacy, false)
	defer fixture.Close()
	root := t.TempDir()
	database, err := store.Open(context.Background(), root+"/accounts.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secrets, err := store.OpenSecretStore(root + "/secret.key")
	if err != nil {
		t.Fatal(err)
	}
	session, err := secrets.Encrypt([]byte(fixture.sessionID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.InsertMegaAccount(context.Background(), store.MegaAccountInput{ID: "session-only", Label: "Fixture", Email: fixture.email, SessionCiphertext: session, Status: "active"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	fixture.revokeCurrentSession()
	lifecycle := NewAccountLifecycle(fixture.Client(), database, secrets)
	_, err = lifecycle.ClientFor(context.Background(), "session-only")
	if !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("error = %v, want ErrReauthRequired", err)
	}
	if fixture.loginCount.Load() != 0 {
		t.Fatal("session-only account attempted password login")
	}
	account, err := database.GetMegaAccount(context.Background(), "session-only")
	if err != nil {
		t.Fatal(err)
	}
	if account.Status != "requires_auth" {
		t.Fatalf("status = %q, want requires_auth", account.Status)
	}
	_, restored, err := database.MegaAccountSecrets(context.Background(), "session-only")
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 0 {
		t.Fatal("rejected session was retained")
	}
}

func TestAuthenticationCancellationIsBounded(t *testing.T) {
	fixture := newAuthFixture(t, accountVersionModern, false)
	defer fixture.Close()
	fixture.delay = 500 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := fixture.Client().LoginAccountContext(ctx, fixture.email, fixture.password)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestAuthenticationAndSessionValidationUseSelectedProxyTransport(t *testing.T) {
	fixture := newAuthFixture(t, accountVersionModern, false)
	defer fixture.Close()
	var proxyRequests atomic.Int64
	direct := http.DefaultTransport.(*http.Transport).Clone()
	direct.Proxy = nil
	defer direct.CloseIdleConnections()
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		proxyRequests.Add(1)
		response, err := direct.RoundTrip(request)
		if err != nil {
			http.Error(writer, "proxy transport failed", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				writer.Header().Add(key, value)
			}
		}
		writer.WriteHeader(response.StatusCode)
		_, _ = io.Copy(writer, response.Body)
	}))
	defer proxy.Close()
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(proxy.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	pool := network.NewTransportPool(network.TransportConfig{ConnectTimeout: time.Second, ResponseHeaderTimeout: time.Second, MaxConnectionsPerHost: 4})
	defer pool.Close()
	httpClient, err := pool.Client(network.ProxyProfile{ID: "fixture-proxy", Type: network.ProxyHTTP, Host: host, Port: port, Timeout: time.Second, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(httpClient, fixture.server.URL).LoginAndValidate(context.Background(), fixture.email, fixture.password); err != nil {
		t.Fatal(err)
	}
	if proxyRequests.Load() < 3 {
		t.Fatalf("proxy requests = %d, want prelogin, login, and health", proxyRequests.Load())
	}
}

func TestAuthenticationRepeatedOperationsHaveNoPrivateTreeOrPollLifecycle(t *testing.T) {
	fixture := newAuthFixture(t, accountVersionLegacy, false)
	defer fixture.Close()
	baseline := runtime.NumGoroutine()
	for index := 0; index < 4; index++ {
		if _, err := fixture.Client().LoginAndValidate(context.Background(), fixture.email, fixture.password); err != nil {
			t.Fatal(err)
		}
	}
	fixture.assertNoPrivateLifecycle(t)
	fixture.Client().CloseIdleConnections()
	runtime.GC()
	if got := runtime.NumGoroutine(); got > baseline+2 {
		t.Fatalf("goroutines after repeated account operations = %d, baseline %d", got, baseline)
	}
}

func TestAuthenticatedPublicLinkCommandDoesNotLoadPrivateTreeOrEvents(t *testing.T) {
	fixture := newAuthFixture(t, accountVersionModern, false)
	defer fixture.Close()
	client := fixture.Client().WithSession(fixture.sessionID)
	if _, err := client.RefreshPayloadURL(context.Background(), PublicLink{Kind: LinkKindFile, Handle: "public-file"}, ""); err != nil {
		t.Fatal(err)
	}
	fixture.assertNoPrivateLifecycle(t)
}

func TestProductionAccountAuthDoesNotReferenceUpstreamFullClientLogin(t *testing.T) {
	for _, path := range []string{"client.go", "account_auth.go", "accounts.go", "../api/api.go", "../download/manager.go"} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(contents)
		for _, forbidden := range []string{"goMega.New", ".Login(email", "postAuthInit", "pollEvents", "getFileSystem", "LoginWithKeys", "MultiFactorLogin"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s contains forbidden full-client authentication reference %q", path, forbidden)
			}
		}
		if strings.Contains(text, "github.com/t3rm1n4l/go-mega") && (strings.Contains(text, ".Login(") || strings.Contains(text, ".MultiFactorLogin(")) {
			t.Fatalf("%s invokes a go-mega login method", path)
		}
	}
}

type authFixture struct {
	t               *testing.T
	server          *httptest.Server
	version         int
	email           string
	password        string
	salt            []byte
	expectedHandle  string
	sessionID       string
	storageBytes    uint64
	usedBytes       uint64
	privateResponse map[string]string

	delay          time.Duration
	revoke         atomic.Bool
	allowReauth    atomic.Bool
	challenge      bool
	challengeToken string
	mfa            bool
	loginCount     atomic.Int64
	healthCount    atomic.Int64
	hashcashCount  atomic.Int64
	treeCount      atomic.Int64
	eventCount     atomic.Int64
	commandKindsMu sync.Mutex
	commandKinds   []string
}

func newAuthFixture(t *testing.T, version int, challenge bool) *authFixture {
	t.Helper()
	fixture := &authFixture{
		t:              t,
		version:        version,
		email:          "fixture@example.test",
		password:       "correct horse battery staple",
		salt:           []byte("fixture-account-salt"),
		storageBytes:   1073741824,
		usedBytes:      123456,
		challenge:      challenge,
		challengeToken: base64.RawURLEncoding.EncodeToString([]byte("fixture-hashcash-token")),
	}
	if version == accountVersionLegacy {
		passkey := fixtureLegacyPasswordKey(fixture.password)
		fixture.expectedHandle = fixtureLegacyStringHash(fixture.email, passkey)
	} else {
		derived := pbkdf2.Key([]byte(fixture.password), fixture.salt, accountKeyIterations, 2*aes.BlockSize, sha512.New)
		fixture.expectedHandle = base64.RawURLEncoding.EncodeToString(derived[aes.BlockSize:])
	}
	fixture.privateResponse = fixture.makeLoginResponse()
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	return fixture
}

func (f *authFixture) Close() { f.server.Close() }

func (f *authFixture) Client() *Client {
	return NewClient(f.server.Client(), f.server.URL)
}

func (f *authFixture) serveHTTP(w http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/cs" || request.Method != http.MethodPost {
		if request.URL.Path == "/sc" {
			f.eventCount.Add(1)
		}
		http.NotFound(w, request)
		return
	}
	if f.delay > 0 {
		timer := time.NewTimer(f.delay)
		select {
		case <-timer.C:
		case <-request.Context().Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		}
	}
	defer request.Body.Close()
	var commands []map[string]any
	if err := json.NewDecoder(request.Body).Decode(&commands); err != nil || len(commands) != 1 {
		writeAuthJSON(w, []any{-2})
		return
	}
	command := commands[0]
	kind, _ := command["a"].(string)
	f.commandKindsMu.Lock()
	f.commandKinds = append(f.commandKinds, kind)
	f.commandKindsMu.Unlock()
	if f.challenge && kind == "us0" && request.Header.Get("X-Hashcash") == "" {
		w.Header().Set("X-Hashcash", "1:255:fixture:"+f.challengeToken)
		w.WriteHeader(http.StatusPaymentRequired)
		return
	}
	if f.challenge && kind == "us0" && validFixtureHashcash(request.Header.Get("X-Hashcash"), f.challengeToken) {
		f.hashcashCount.Add(1)
	} else if f.challenge && kind == "us0" && request.Header.Get("X-Hashcash") != "" {
		w.Header().Set("X-Hashcash", "1:255:fixture:"+f.challengeToken)
		w.WriteHeader(http.StatusPaymentRequired)
		return
	}
	switch kind {
	case "us0":
		response := map[string]any{"v": f.version}
		if f.version == accountVersionModern {
			response["s"] = base64.RawURLEncoding.EncodeToString(f.salt)
		}
		writeAuthJSON(w, []any{response})
	case "us":
		f.loginCount.Add(1)
		if f.mfa {
			writeAuthJSON(w, []any{-26})
			return
		}
		if handle, _ := command["uh"].(string); handle != f.expectedHandle {
			writeAuthJSON(w, []any{-2})
			return
		}
		if f.version == accountVersionModern {
			sek, _ := command["sek"].(string)
			decoded, decodeErr := base64.RawURLEncoding.DecodeString(sek)
			if decodeErr != nil || len(decoded) != aes.BlockSize {
				writeAuthJSON(w, []any{-2})
				return
			}
		} else if _, present := command["sek"]; present {
			writeAuthJSON(w, []any{-2})
			return
		}
		if f.allowReauth.Load() {
			f.revoke.Store(false)
		}
		writeAuthJSON(w, []any{f.privateResponse})
	case "uq":
		f.healthCount.Add(1)
		if request.URL.Query().Get("sid") != f.sessionID || f.revoke.Load() {
			writeAuthJSON(w, []any{-15})
			return
		}
		writeAuthJSON(w, []any{map[string]any{"mstrg": f.storageBytes, "cstrg": f.usedBytes}})
	case "g":
		writeAuthJSON(w, []any{map[string]any{"g": "https://payload.example.test/opaque"}})
	case "f":
		f.treeCount.Add(1)
		writeAuthJSON(w, []any{-2})
	case "sc":
		f.eventCount.Add(1)
		writeAuthJSON(w, []any{-2})
	default:
		writeAuthJSON(w, []any{-2})
	}
}

func (f *authFixture) makeLoginResponse() map[string]string {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		f.t.Fatal(err)
	}
	plainSession := []byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEFG")
	f.sessionID = base64.RawURLEncoding.EncodeToString(plainSession)
	message := new(big.Int).SetBytes(plainSession)
	ciphertext := new(big.Int).Exp(message, big.NewInt(int64(key.PublicKey.E)), key.N)
	csid := encodeMPI(ciphertext)
	private := append(encodeMPI(key.Primes[0]), encodeMPI(key.Primes[1])...)
	private = append(private, encodeMPI(key.D)...)
	private = zeroPad(private, aes.BlockSize)
	masterKey := []byte("fixture-master16")
	passkey := fixtureLegacyPasswordKey(f.password)
	if f.version == accountVersionModern {
		derived := pbkdf2.Key([]byte(f.password), f.salt, accountKeyIterations, 2*aes.BlockSize, sha512.New)
		passkey = derived[:aes.BlockSize]
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		f.t.Fatal(err)
	}
	privateCiphertext := make([]byte, len(private))
	for offset := 0; offset < len(private); offset += aes.BlockSize {
		block.Encrypt(privateCiphertext[offset:offset+aes.BlockSize], private[offset:offset+aes.BlockSize])
	}
	keyCiphertext := make([]byte, aes.BlockSize)
	if block, err = aes.NewCipher(passkey); err != nil {
		f.t.Fatal(err)
	}
	block.Encrypt(keyCiphertext, masterKey)
	return map[string]string{
		"k":     base64.RawURLEncoding.EncodeToString(keyCiphertext),
		"privk": base64.RawURLEncoding.EncodeToString(privateCiphertext),
		"csid":  base64.RawURLEncoding.EncodeToString(csid),
	}
}

func fixtureLegacyPasswordKey(password string) []byte {
	padded := append([]byte(nil), []byte(password)...)
	if remainder := len(padded) % 4; remainder != 0 {
		padded = append(padded, make([]byte, 4-remainder)...)
	}
	result := []byte{0x93, 0xc4, 0x67, 0xe3, 0x7d, 0xb0, 0xc7, 0xa4, 0xd1, 0xbe, 0x3f, 0x81, 0x01, 0x52, 0xcb, 0x56}
	ciphers := make([]cipher.Block, 0, (len(padded)+15)/16)
	for offset := 0; offset < len(padded); offset += aes.BlockSize {
		key := make([]byte, aes.BlockSize)
		copy(key, padded[offset:min(offset+aes.BlockSize, len(padded))])
		block, err := aes.NewCipher(key)
		if err != nil {
			panic(err)
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

func fixtureLegacyStringHash(value string, key []byte) string {
	padded := append([]byte(nil), []byte(value)...)
	if remainder := len(padded) % 4; remainder != 0 {
		padded = append(padded, make([]byte, 4-remainder)...)
	}
	var words [4]uint32
	for offset := 0; offset < len(padded); offset += 4 {
		words[(offset/4)&3] ^= binary.BigEndian.Uint32(padded[offset : offset+4])
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	data := make([]byte, aes.BlockSize)
	for index, word := range words {
		binary.BigEndian.PutUint32(data[index*4:index*4+4], word)
	}
	for iteration := 0; iteration < 16384; iteration++ {
		block.Encrypt(data, data)
	}
	return base64.RawURLEncoding.EncodeToString([]byte{data[0], data[1], data[2], data[3], data[8], data[9], data[10], data[11]})
}

func validFixtureHashcash(header, token string) bool {
	parts := strings.Split(header, ":")
	if len(parts) != 3 || parts[0] != "1" || parts[1] != token {
		return false
	}
	prefix, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(prefix) != 4 {
		return false
	}
	tokenBytes, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return false
	}
	if remainder := len(tokenBytes) % 16; remainder != 0 {
		tokenBytes = append(tokenBytes, make([]byte, 16-remainder)...)
	}
	buffer := make([]byte, 4+262144*48)
	for index := 0; index < 262144; index++ {
		copy(buffer[4+index*48:], tokenBytes)
	}
	copy(buffer[:4], prefix)
	digest := sha256.Sum256(buffer)
	threshold := uint32((((255 & 63) << 1) + 1) << ((255>>6)*7 + 3))
	return binary.BigEndian.Uint32(digest[:4]) <= threshold
}

func (f *authFixture) revokeCurrentSession() {
	f.revoke.Store(true)
	f.allowReauth.Store(true)
}

func (f *authFixture) assertNoPrivateLifecycle(t *testing.T) {
	t.Helper()
	f.commandKindsMu.Lock()
	defer f.commandKindsMu.Unlock()
	for _, kind := range f.commandKinds {
		if kind == "f" || kind == "sc" {
			t.Fatalf("private filesystem/event command invoked: %q (%v)", kind, f.commandKinds)
		}
	}
	if f.treeCount.Load() != 0 || f.eventCount.Load() != 0 {
		t.Fatalf("tree/event endpoint counts = %d/%d", f.treeCount.Load(), f.eventCount.Load())
	}
}

func writeAuthJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func encodeMPI(value *big.Int) []byte {
	data := value.Bytes()
	result := []byte{byte(value.BitLen() >> 8), byte(value.BitLen())}
	return append(result, data...)
}
