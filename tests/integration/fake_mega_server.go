package integration

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lorenzocorallo/megadw/internal/mega"
)

const (
	fakeFolderHandle = "folder01"
	fakeRootHandle   = "root0001"
	fakeNestedHandle = "nested01"
	fakeFileHandle   = "file0001"
)

// FakeMegaServerOptions controls deterministic payload-side failures. A zero
// value produces a healthy server; CorruptByteAt is used only when
// CorruptPayload is true.
type FakeMegaServerOptions struct {
	Delay                 time.Duration
	ResetAfterBytes       int
	MalformedContentRange bool
	StatusCode            int
	RetryAfter            string
	ExpirePayloadURL      bool
	CorruptPayload        bool
	CorruptByteAt         int64
}

// FakeMegaServer is a local MEGA-compatible public metadata and range server.
// It intentionally exposes only deterministic project-owned fixture data.
type FakeMegaServer struct {
	server *httptest.Server

	optionsMu sync.RWMutex
	options   FakeMegaServerOptions

	commandRequests  atomic.Int64
	payloadRequests  atomic.Int64
	nextPayloadToken atomic.Uint64
	expiredURL       atomic.Bool

	masterKey  mega.NodeKey
	rootKey    mega.NodeKey
	nestedKey  mega.NodeKey
	fileKey    mega.FileKey
	fileLink   string
	folderLink string
	plaintext  []byte
}

// NewFakeMegaServer constructs the healthy deterministic fixture.
func NewFakeMegaServer() *FakeMegaServer {
	return NewFakeMegaServerWithOptions(FakeMegaServerOptions{})
}

// NewFakeMegaServerWithOptions constructs the fixture with payload fault
// injection enabled according to options.
func NewFakeMegaServerWithOptions(options FakeMegaServerOptions) *FakeMegaServer {
	fixture := &FakeMegaServer{options: options}
	fixture.initializeData()
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	return fixture
}

// Close releases the fake server.
func (f *FakeMegaServer) Close() {
	if f.server != nil {
		f.server.Close()
	}
}

// Client returns a MEGA client configured to use this fixture's API endpoint.
func (f *FakeMegaServer) Client() *mega.Client {
	return mega.NewClient(f.server.Client(), f.server.URL)
}

// HTTPClient returns the fixture's local HTTP client for payload assertions.
func (f *FakeMegaServer) HTTPClient() *http.Client {
	return f.server.Client()
}

// FileLink returns the modern public file URL for the deterministic fixture.
func (f *FakeMegaServer) FileLink() string {
	return "https://mega.nz/file/" + fakeFileHandle + "#" + f.fileLink
}

// FolderLink returns the modern public folder URL for the deterministic fixture.
func (f *FakeMegaServer) FolderLink() string {
	return "https://mega.nz/folder/" + fakeFolderHandle + "#" + f.folderLink
}

// Plaintext returns a copy of the deterministic plaintext payload.
func (f *FakeMegaServer) Plaintext() []byte {
	return append([]byte(nil), f.plaintext...)
}

// FileKey returns the deterministic file key used by the fixture payload.
func (f *FakeMegaServer) FileKey() mega.FileKey {
	return f.fileKey
}

// PayloadRequestCount reports payload requests, including intentionally
// failed requests. Metadata requests are not counted here.
func (f *FakeMegaServer) PayloadRequestCount() int64 {
	return f.payloadRequests.Load()
}

// CommandRequestCount reports requests received by the fake /cs endpoint.
func (f *FakeMegaServer) CommandRequestCount() int64 {
	return f.commandRequests.Load()
}

// SetOptions changes payload fault injection for subsequent requests.
func (f *FakeMegaServer) SetOptions(options FakeMegaServerOptions) {
	f.optionsMu.Lock()
	f.options = options
	f.optionsMu.Unlock()
	f.expiredURL.Store(false)
}

func (f *FakeMegaServer) initializeData() {
	masterRaw := mustHex("0102030405060708090a0b0c0d0e0f10")
	rootRaw := mustHex("2122232425262728292a2b2c2d2e2f30")
	nestedRaw := mustHex("1112131415161718191a1b1c1d1e1f20")
	f.masterKey = mustNodeKey(masterRaw)
	f.rootKey = mustNodeKey(rootRaw)
	f.nestedKey = mustNodeKey(nestedRaw)
	f.plaintext = make([]byte, 300_123)
	for index := range f.plaintext {
		f.plaintext[index] = byte((index*29 + index/17 + 7) & 0xff)
	}

	var contentKey [16]byte
	copy(contentKey[:], mustHex("10311273143516f718391a7b1c3d1eff"))
	var nonce [8]byte
	copy(nonce[:], mustHex("1020304050607080"))
	provisional := mega.FileKey{AESKey: contentKey, Nonce: nonce}
	metaMAC := fixtureMetaMAC(f.plaintext, provisional)
	fileRaw := rawFileKey(contentKey, nonce, metaMAC)
	f.fileKey = mustFileKey(fileRaw)
	if err := mega.VerifyIntegrity(bytes.NewReader(f.plaintext), int64(len(f.plaintext)), f.fileKey); err != nil {
		panic(err)
	}
	f.fileLink = base64.RawURLEncoding.EncodeToString(fileRaw)
	f.folderLink = base64.RawURLEncoding.EncodeToString(masterRaw)
}

func (f *FakeMegaServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/cs":
		f.serveCommand(w, r)
	case strings.HasPrefix(r.URL.Path, "/payload/"):
		f.servePayload(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *FakeMegaServer) serveCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	f.commandRequests.Add(1)
	defer r.Body.Close()
	var commands []map[string]any
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := decoder.Decode(&commands); err != nil || len(commands) != 1 {
		writeJSON(w, []any{-2})
		return
	}
	command := commands[0]
	switch stringValue(command["a"]) {
	case "g":
		handle := stringValue(command["p"])
		if handle == "" {
			handle = stringValue(command["n"])
		}
		if handle != fakeFileHandle {
			writeJSON(w, []any{-5})
			return
		}
		token := f.nextPayloadToken.Add(1)
		writeJSON(w, []any{map[string]any{
			"g":  f.server.URL + "/payload/" + handle + "?token=" + strconv.FormatUint(token, 10),
			"s":  int64(len(f.plaintext)),
			"at": encryptAttributes(f.fileKey.AESKey, "fixture.txt"),
		}})
	case "f":
		if r.URL.Query().Get("n") != fakeFolderHandle {
			writeJSON(w, []any{-5})
			return
		}
		writeJSON(w, []any{map[string]any{"f": f.folderNodes()}})
	default:
		writeJSON(w, []any{-2})
	}
}

func (f *FakeMegaServer) folderNodes() []any {
	return []any{
		map[string]any{
			"h": fakeRootHandle,
			"p": "owner01",
			"t": 1,
			"k": fakePublicNodeKeys(f.rootKey.Raw, f.masterKey),
			"a": encryptAttributes(f.rootKey.AESKey, "Fixture root"),
		},
		map[string]any{
			"h": fakeNestedHandle,
			"p": fakeRootHandle,
			"t": 1,
			"k": fakePublicNodeKeys(f.nestedKey.Raw, f.masterKey),
			"a": encryptAttributes(f.nestedKey.AESKey, "Nested folder"),
		},
		map[string]any{
			"h": fakeFileHandle,
			"p": fakeNestedHandle,
			"t": 0,
			"s": int64(len(f.plaintext)),
			"k": fakePublicNodeKeys(f.fileKey.Raw[:], f.masterKey),
			"a": encryptAttributes(f.fileKey.AESKey, "fixture.txt"),
		},
	}
}

func rawFileKey(contentKey [16]byte, nonce [8]byte, metaMAC [8]byte) []byte {
	raw := make([]byte, 32)
	copy(raw[16:24], nonce[:])
	copy(raw[24:], metaMAC[:])
	for index := 0; index < 4; index++ {
		contentWord := binary.BigEndian.Uint32(contentKey[index*4 : index*4+4])
		var otherWord uint32
		switch index {
		case 0, 1:
			otherWord = binary.BigEndian.Uint32(nonce[index*4 : index*4+4])
		case 2, 3:
			otherWord = binary.BigEndian.Uint32(metaMAC[(index-2)*4 : (index-2)*4+4])
		}
		binary.BigEndian.PutUint32(raw[index*4:index*4+4], contentWord^otherWord)
	}
	return raw
}

func (f *FakeMegaServer) servePayload(w http.ResponseWriter, r *http.Request) {
	f.payloadRequests.Add(1)
	options := f.getOptions()
	if options.Delay > 0 {
		time.Sleep(options.Delay)
	}
	if options.ExpirePayloadURL && r.URL.Query().Get("token") == "1" && !f.expiredURL.Swap(true) {
		w.WriteHeader(http.StatusGone)
		return
	}
	if options.StatusCode != 0 {
		if options.RetryAfter != "" {
			w.Header().Set("Retry-After", options.RetryAfter)
		}
		w.WriteHeader(options.StatusCode)
		return
	}

	start, end, ranged, err := requestedRange(r.Header.Get("Range"), int64(len(f.plaintext)))
	if err != nil {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	if ranged {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", contentRange(start, end, int64(len(f.plaintext)), options.MalformedContentRange))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)
	}

	plaintext := f.plaintext[start : end+1]
	ciphertext, err := mega.CryptAt(plaintext, f.fileKey, start)
	if err != nil {
		return
	}
	if options.CorruptPayload {
		corruptAt := options.CorruptByteAt
		if corruptAt >= start && corruptAt <= end {
			ciphertext[corruptAt-start] ^= 1
		}
	}
	if options.ResetAfterBytes > 0 && options.ResetAfterBytes < len(ciphertext) {
		f.writeResetResponse(w, ciphertext, options.ResetAfterBytes, ranged, start, end, options.MalformedContentRange)
		return
	}
	_, _ = w.Write(ciphertext)
}

func (f *FakeMegaServer) writeResetResponse(w http.ResponseWriter, body []byte, count int, ranged bool, start, end int64, malformed bool) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	connection, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer connection.Close()
	status := http.StatusOK
	statusText := http.StatusText(status)
	contentRangeHeader := ""
	if ranged {
		status = http.StatusPartialContent
		statusText = http.StatusText(status)
		contentRangeHeader = "Content-Range: " + contentRange(start, end, int64(len(f.plaintext)), malformed) + "\r\n"
	}
	header := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Length: %d\r\n%sConnection: close\r\n\r\n", status, statusText, len(body), contentRangeHeader)
	_, _ = buffered.WriteString(header)
	_, _ = buffered.Write(body[:count])
	_ = buffered.Flush()
}

func (f *FakeMegaServer) getOptions() FakeMegaServerOptions {
	f.optionsMu.RLock()
	options := f.options
	f.optionsMu.RUnlock()
	return options
}

func requestedRange(value string, size int64) (start, end int64, ranged bool, err error) {
	if value == "" {
		if size == 0 {
			return 0, -1, false, nil
		}
		return 0, size - 1, false, nil
	}
	if !strings.HasPrefix(value, "bytes=") {
		return 0, 0, true, fmt.Errorf("unsupported range")
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, 0, true, fmt.Errorf("unsupported range")
	}
	start, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, true, err
	}
	end, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil || start < 0 || start > end || end >= size {
		return 0, 0, true, fmt.Errorf("invalid range")
	}
	return start, end, true, nil
}

func contentRange(start, end, size int64, malformed bool) string {
	if malformed {
		return fmt.Sprintf("bytes %d-%d/*", start, start)
	}
	return fmt.Sprintf("bytes %d-%d/%d", start, end, size)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func fakeEncryptedNodeKey(raw []byte, master mega.NodeKey, owner string) string {
	return owner + ":" + base64.RawURLEncoding.EncodeToString(encryptECB(raw, master.AESKey[:]))
}

func fakePublicNodeKeys(raw []byte, master mega.NodeKey) string {
	// Real public-folder listings may include an account-owned alternative
	// before the key encrypted for the exported root. Only the latter can be
	// decrypted with the public folder key.
	unavailable := base64.RawURLEncoding.EncodeToString(make([]byte, len(raw)))
	return "owner01:" + unavailable + "/" + fakeEncryptedNodeKey(raw, master, fakeRootHandle)
}

func encryptECB(plaintext, key []byte) []byte {
	if len(plaintext) == 0 || len(plaintext)%aes.BlockSize != 0 {
		panic("ECB plaintext must be non-empty and block aligned")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	ciphertext := make([]byte, len(plaintext))
	for offset := 0; offset < len(plaintext); offset += aes.BlockSize {
		block.Encrypt(ciphertext[offset:offset+aes.BlockSize], plaintext[offset:offset+aes.BlockSize])
	}
	return ciphertext
}

func encryptAttributes(key [16]byte, name string) string {
	attributes, err := json.Marshal(map[string]string{"n": name})
	if err != nil {
		panic(err)
	}
	payload := append([]byte("MEGA"), attributes...)
	if remainder := len(payload) % aes.BlockSize; remainder != 0 {
		payload = append(payload, make([]byte, aes.BlockSize-remainder)...)
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		panic(err)
	}
	ciphertext := make([]byte, len(payload))
	cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(ciphertext, payload)
	return base64.RawURLEncoding.EncodeToString(ciphertext)
}

func fixtureMetaMAC(plaintext []byte, key mega.FileKey) [8]byte {
	block, err := aes.NewCipher(key.AESKey[:])
	if err != nil {
		panic(err)
	}
	var iv [aes.BlockSize]byte
	copy(iv[:8], key.Nonce[:])
	copy(iv[8:], key.Nonce[:])
	final := make([]byte, aes.BlockSize)
	finalCipher := cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize))
	offset := 0
	for chunkNumber := 1; offset < len(plaintext); chunkNumber++ {
		chunkSize := fakeChunkSize(chunkNumber)
		if remaining := len(plaintext) - offset; chunkSize > remaining {
			chunkSize = remaining
		}
		paddedSize := (chunkSize + aes.BlockSize - 1) / aes.BlockSize * aes.BlockSize
		padded := make([]byte, paddedSize)
		copy(padded, plaintext[offset:offset+chunkSize])
		chunkCipher := cipher.NewCBCEncrypter(block, iv[:])
		paddedCiphertext := make([]byte, len(padded))
		chunkCipher.CryptBlocks(paddedCiphertext, padded)
		chunkMAC := paddedCiphertext[len(paddedCiphertext)-aes.BlockSize:]
		finalCipher.CryptBlocks(final, chunkMAC)
		offset += chunkSize
	}
	words := [4]uint32{
		binary.BigEndian.Uint32(final[0:4]),
		binary.BigEndian.Uint32(final[4:8]),
		binary.BigEndian.Uint32(final[8:12]),
		binary.BigEndian.Uint32(final[12:16]),
	}
	var result [8]byte
	binary.BigEndian.PutUint32(result[0:4], words[0]^words[1])
	binary.BigEndian.PutUint32(result[4:8], words[2]^words[3])
	return result
}

func fakeChunkSize(number int) int {
	if number <= 8 {
		return number * 128 * 1024
	}
	return 1024 * 1024
}

func mustHex(value string) []byte {
	decoded, err := hex.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return decoded
}

func mustNodeKey(raw []byte) mega.NodeKey {
	return mustNodeKeyEncoded(base64.RawURLEncoding.EncodeToString(raw))
}

func mustNodeKeyEncoded(encoded string) mega.NodeKey {
	key, err := mega.DecodeNodeKey(encoded)
	if err != nil {
		panic(err)
	}
	return key
}

func mustFileKey(raw []byte) mega.FileKey {
	key, err := mega.DecodeFileKey(base64.RawURLEncoding.EncodeToString(raw))
	if err != nil {
		panic(err)
	}
	return key
}
