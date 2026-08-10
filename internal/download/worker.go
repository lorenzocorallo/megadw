package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lorenzocorallo/megadw/internal/mega"
)

const transferBufferSize = 512 << 10

// PayloadFetcher is the narrow HTTP boundary used by the single range worker.
// Keeping it injectable makes response validation and crash tests deterministic
// without introducing a transfer framework.
type PayloadFetcher interface {
	FetchPayloadRange(ctx context.Context, payloadURL string, start, end int64) (*http.Response, error)
}

// RangeWorker downloads one encrypted MEGA range directly into a random-access
// plaintext writer. The worker intentionally owns no queue or retry policy.
type RangeWorker struct {
	Fetcher PayloadFetcher
	Buffers sync.Pool
}

// TransferOptions contains the bounded, per-read instrumentation owned by a
// scheduler. It does not change the protocol or the plaintext writer contract.
type TransferOptions struct {
	// Limiter is normally the per-job bucket. GlobalLimiter is applied in
	// addition to it when both process-wide and per-job limits are configured.
	Limiter         *BandwidthLimiter
	GlobalLimiter   *BandwidthLimiter
	Meter           *SpeedMeter
	ReadIdleTimeout time.Duration
}

func NewRangeWorker(fetcher PayloadFetcher) *RangeWorker {
	worker := &RangeWorker{Fetcher: fetcher}
	worker.Buffers.New = func() any { return make([]byte, transferBufferSize) }
	return worker
}

// DownloadRange fetches and decrypts exactly segment.Size() bytes.
func (w *RangeWorker) DownloadRange(ctx context.Context, writer interface {
	WriteAt([]byte, int64) (int, error)
}, key mega.FileKey, payloadURL string, segment Segment, fileSize int64) (int64, error) {
	return w.DownloadRangeWithOptions(ctx, writer, key, payloadURL, segment, fileSize, TransferOptions{})
}

// DownloadRangeWithOptions is the scheduler-facing range operation. The
// limiter is applied after each bounded network read and the meter records
// only bytes that were successfully read and decrypted/written.
func (w *RangeWorker) DownloadRangeWithOptions(ctx context.Context, writer interface {
	WriteAt([]byte, int64) (int, error)
}, key mega.FileKey, payloadURL string, segment Segment, fileSize int64, options TransferOptions) (int64, error) {
	if w == nil || w.Fetcher == nil {
		return 0, fmt.Errorf("payload fetcher is unavailable")
	}
	if segment.Start < 0 || segment.End < segment.Start || segment.End >= fileSize {
		return 0, fmt.Errorf("invalid segment range %d-%d for file size %d", segment.Start, segment.End, fileSize)
	}
	response, err := w.Fetcher.FetchPayloadRange(ctx, payloadURL, segment.Start, segment.End)
	if err != nil {
		return 0, err
	}
	if response == nil || response.Body == nil {
		return 0, fmt.Errorf("payload fetcher returned no response body")
	}
	if options.ReadIdleTimeout > 0 {
		response.Body = newReadIdleBody(response.Body, options.ReadIdleTimeout)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent {
		return 0, &HTTPStatusError{StatusCode: response.StatusCode, Status: response.Status, RetryAfter: response.Header.Get("Retry-After")}
	}
	if err := validateContentRange(response.Header.Get("Content-Range"), segment, fileSize); err != nil {
		return 0, err
	}
	want := segment.Size()
	if response.ContentLength >= 0 && response.ContentLength != want {
		return 0, fmt.Errorf("payload content length %d, want %d", response.ContentLength, want)
	}
	stream, err := mega.NewCTR(key, segment.Start)
	if err != nil {
		return 0, err
	}
	limited := io.LimitReader(response.Body, want)
	buffer := w.Buffers.Get().([]byte)
	defer w.Buffers.Put(buffer)
	var written int64
	for written < want {
		readSize := int64(len(buffer))
		if remaining := want - written; remaining < readSize {
			readSize = remaining
		}
		if options.Limiter != nil {
			if err := options.Limiter.WaitN(ctx, readSize); err != nil {
				return written, err
			}
		}
		if options.GlobalLimiter != nil {
			if err := options.GlobalLimiter.WaitN(ctx, readSize); err != nil {
				return written, err
			}
		}
		n, readErr := io.ReadAtLeast(limited, buffer[:readSize], 1)
		if n > 0 {
			stream.XORKeyStream(buffer[:n], buffer[:n])
			if err := writeAllAt(writer, buffer[:n], segment.Start+written); err != nil {
				return written, err
			}
			written += int64(n)
			if options.Meter != nil {
				options.Meter.Add(int64(n))
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
				return written, fmt.Errorf("payload ended after %d of %d bytes: %w", written, want, readErr)
			}
			return written, fmt.Errorf("read encrypted payload: %w", readErr)
		}
	}
	var extra [1]byte
	n, readErr := response.Body.Read(extra[:])
	if n != 0 || readErr == nil {
		return written, fmt.Errorf("payload returned more than %d bytes", want)
	}
	if readErr != io.EOF && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return written, fmt.Errorf("validate payload length: %w", readErr)
	}
	return written, nil
}

type readIdleBody struct {
	source    io.ReadCloser
	timeout   time.Duration
	timerMu   sync.Mutex
	timer     *time.Timer
	closeOnce sync.Once
	timedOut  atomic.Bool
}

func newReadIdleBody(source io.ReadCloser, timeout time.Duration) *readIdleBody {
	body := &readIdleBody{source: source, timeout: timeout}
	body.resetTimer()
	return body
}

func (b *readIdleBody) Read(buffer []byte) (int, error) {
	b.resetTimer()
	n, err := b.source.Read(buffer)
	if n > 0 {
		b.resetTimer()
	}
	if b.timedOut.Load() {
		return n, fmt.Errorf("payload read idle timeout after %s", b.timeout)
	}
	return n, err
}

func (b *readIdleBody) Close() error {
	b.timerMu.Lock()
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timerMu.Unlock()
	var err error
	b.closeOnce.Do(func() { err = b.source.Close() })
	return err
}

func (b *readIdleBody) resetTimer() {
	b.timerMu.Lock()
	if b.timer == nil {
		b.timer = time.AfterFunc(b.timeout, b.expire)
	} else {
		b.timer.Reset(b.timeout)
	}
	b.timerMu.Unlock()
}

func (b *readIdleBody) expire() {
	b.timedOut.Store(true)
	b.closeOnce.Do(func() { _ = b.source.Close() })
}

// DownloadSegment is the descriptive alias used by scheduler-facing callers.
func (w *RangeWorker) DownloadSegment(ctx context.Context, writer interface {
	WriteAt([]byte, int64) (int, error)
}, key mega.FileKey, payloadURL string, segment Segment, fileSize int64) (int64, error) {
	return w.DownloadRange(ctx, writer, key, payloadURL, segment, fileSize)
}

func writeAllAt(writer interface {
	WriteAt([]byte, int64) (int, error)
}, data []byte, offset int64) error {
	for len(data) > 0 {
		n, err := writer.WriteAt(data, offset)
		if n > 0 {
			offset += int64(n)
			data = data[n:]
		}
		if err != nil {
			return fmt.Errorf("write plaintext range: %w", err)
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// HTTPStatusError retains the status and Retry-After value for later retry
// phases without making the single-worker core silently retry.
type HTTPStatusError struct {
	StatusCode int
	Status     string
	RetryAfter string
}

func (e *HTTPStatusError) Error() string {
	if e == nil {
		return "payload HTTP request failed"
	}
	return fmt.Sprintf("payload HTTP status %s", e.Status)
}

func validateContentRange(value string, segment Segment, fileSize int64) error {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "bytes ") {
		return fmt.Errorf("payload Content-Range %q is invalid", value)
	}
	body := strings.TrimPrefix(value, "bytes ")
	rangePart, totalPart, ok := strings.Cut(body, "/")
	if !ok {
		return fmt.Errorf("payload Content-Range %q is invalid", value)
	}
	startText, endText, ok := strings.Cut(rangePart, "-")
	if !ok {
		return fmt.Errorf("payload Content-Range %q is invalid", value)
	}
	start, startErr := strconv.ParseInt(startText, 10, 64)
	end, endErr := strconv.ParseInt(endText, 10, 64)
	total, totalErr := strconv.ParseInt(totalPart, 10, 64)
	if startErr != nil || endErr != nil || totalErr != nil || start != segment.Start || end != segment.End || total != fileSize {
		return fmt.Errorf("payload Content-Range %q does not match bytes %d-%d/%d", value, segment.Start, segment.End, fileSize)
	}
	return nil
}

// HTTPPayloadFetcher adapts a standard client for focused worker tests.
type HTTPPayloadFetcher struct {
	Client *http.Client
}

func (f HTTPPayloadFetcher) FetchPayloadRange(ctx context.Context, payloadURL string, start, end int64) (*http.Response, error) {
	if f.Client == nil {
		return nil, fmt.Errorf("HTTP client is unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, payloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create payload request: %w", err)
	}
	request.Header.Set("Range", "bytes="+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10))
	return f.Client.Do(request)
}
