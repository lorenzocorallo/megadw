package download

import (
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lorenzocorallo/megadw/internal/mega"
)

type RetryClass string

const (
	RetryNone       RetryClass = "none"
	RetryTransport  RetryClass = "transport"
	RetryRateLimit  RetryClass = "rate_limit"
	RetryServer     RetryClass = "server"
	RetryRefreshURL RetryClass = "refresh_url"
	RetryQuota      RetryClass = "quota"
)

var ErrQuota = errors.New("MEGA transfer quota is exhausted")

type QuotaError struct {
	Cause      error
	RetryAfter time.Duration
}

func (e *QuotaError) Error() string {
	if e == nil {
		return ErrQuota.Error()
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", ErrQuota, e.Cause)
	}
	return ErrQuota.Error()
}
func (e *QuotaError) Unwrap() error {
	if e == nil {
		return ErrQuota
	}
	if e.Cause != nil {
		return e.Cause
	}
	return ErrQuota
}
func IsQuotaError(err error) bool {
	var q *QuotaError
	return errors.As(err, &q) || errors.Is(err, ErrQuota)
}

func ClassifyRetry(err error) RetryClass {
	if err == nil {
		return RetryNone
	}
	var apiError *mega.APIError
	if errors.As(err, &apiError) && (apiError.Code == -17 || apiError.Code == -24) {
		return RetryQuota
	}
	if IsQuotaError(err) {
		return RetryQuota
	}
	var status *HTTPStatusError
	if errors.As(err, &status) {
		switch status.StatusCode {
		case http.StatusTooManyRequests:
			return RetryRateLimit
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
			return RetryRefreshURL
		case http.StatusInternalServerError, http.StatusNotImplemented, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return RetryServer
		case 509:
			return RetryQuota
		}
		if status.StatusCode >= 500 && status.StatusCode <= 599 {
			return RetryServer
		}
		return RetryNone
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNREFUSED) {
		return RetryTransport
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return RetryTransport
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "unexpected eof") || strings.Contains(text, "connection reset") || strings.Contains(text, "payload ended") || strings.Contains(text, "temporary") || strings.Contains(text, "timeout") {
		return RetryTransport
	}
	return RetryNone
}

func ExponentialBackoff(attempt int, retryAfter string, now time.Time, random *rand.Rand) time.Duration {
	if retryAfter != "" {
		if delay, ok := ParseRetryAfter(retryAfter, now); ok {
			return delay
		}
	}
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 9 {
		attempt = 9
	}
	delay := time.Second * time.Duration(1<<(attempt-1))
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	// Full jitter is bounded to 25% of the base delay. Tests may pass a seeded
	// source; production uses the package source only for a small non-critical
	// delay variance.
	if random == nil {
		random = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	result := delay + time.Duration(random.Int63n(int64(delay/4)+1))
	if result > 5*time.Minute {
		result = 5 * time.Minute
	}
	return result
}

func ParseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 {
			seconds = 0
		}
		if seconds > int64((24*time.Hour)/time.Second) {
			seconds = int64((24 * time.Hour) / time.Second)
		}
		return time.Duration(seconds) * time.Second, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	delay := when.Sub(now)
	if delay < 0 {
		delay = 0
	}
	if delay > 24*time.Hour {
		delay = 24 * time.Hour
	}
	return delay, true
}

func IsRetryableHTTPStatus(status int) bool {
	return status == 429 || status == 509 || status >= 500 && status <= 599
}
