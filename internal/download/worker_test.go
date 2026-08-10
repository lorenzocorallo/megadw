package download

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lorenzocorallo/megadw/internal/mega"
)

func TestRangeWorkerDecryptsAndWritesExactRange(t *testing.T) {
	key := testWorkerKey()
	plain := make([]byte, 321)
	for index := range plain {
		plain[index] = byte(index*17 + 3)
	}
	const fileSize = int64(321)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		start, end := int64(37), int64(218)
		writer.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, fileSize))
		writer.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		writer.WriteHeader(http.StatusPartialContent)
		ciphertext, err := mega.CryptAt(plain[start:end+1], key, start)
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = writer.Write(ciphertext)
	}))
	defer server.Close()

	output := make([]byte, len(plain))
	worker := NewRangeWorker(HTTPPayloadFetcher{Client: server.Client()})
	written, err := worker.DownloadRange(context.Background(), byteWriterAt{data: output}, key, server.URL, Segment{Index: 0, Start: 37, End: 218}, fileSize)
	if err != nil {
		t.Fatal(err)
	}
	if written != 182 || !bytes.Equal(output[37:219], plain[37:219]) {
		t.Fatalf("written = %d, range does not match plaintext", written)
	}
}

func TestRangeWorkerRejectsMalformedContentRange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Range", "bytes 0-1/*")
		writer.WriteHeader(http.StatusPartialContent)
		_, _ = writer.Write([]byte{1, 2})
	}))
	defer server.Close()
	worker := NewRangeWorker(HTTPPayloadFetcher{Client: server.Client()})
	_, err := worker.DownloadRange(context.Background(), byteWriterAt{data: make([]byte, 2)}, testWorkerKey(), server.URL, Segment{Start: 0, End: 1}, 2)
	if err == nil {
		t.Fatal("malformed Content-Range was accepted")
	}
}

func TestRangeWorkerReadIdleTimeoutClosesStalledBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Range", "bytes 0-0/1")
		writer.WriteHeader(http.StatusPartialContent)
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	}))
	defer server.Close()
	worker := NewRangeWorker(HTTPPayloadFetcher{Client: server.Client()})
	started := time.Now()
	_, err := worker.DownloadRangeWithOptions(context.Background(), byteWriterAt{data: make([]byte, 1)}, testWorkerKey(), server.URL, Segment{Start: 0, End: 0}, 1, TransferOptions{ReadIdleTimeout: 20 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "read idle timeout") {
		t.Fatalf("stalled read error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("stalled read cancellation took %s", elapsed)
	}
}

type byteWriterAt struct{ data []byte }

func (w byteWriterAt) WriteAt(data []byte, offset int64) (int, error) {
	if offset < 0 || offset+int64(len(data)) > int64(len(w.data)) {
		return 0, io.ErrShortWrite
	}
	copy(w.data[offset:], data)
	return len(data), nil
}

func testWorkerKey() mega.FileKey {
	var key mega.FileKey
	for index := range key.AESKey {
		key.AESKey[index] = byte(index + 1)
	}
	for index := range key.Nonce {
		key.Nonce[index] = byte(index + 11)
	}
	return key
}
