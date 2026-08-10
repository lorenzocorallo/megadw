package download

import (
	"errors"
	"math/rand"
	"net/http"
	"testing"
	"time"
)

func TestRetryClassificationAndBackoff(t *testing.T) {
	checks := []struct {
		status int
		want   RetryClass
	}{{http.StatusTooManyRequests, RetryRateLimit}, {http.StatusServiceUnavailable, RetryServer}, {http.StatusUnauthorized, RetryRefreshURL}, {509, RetryQuota}}
	for _, check := range checks {
		if got := ClassifyRetry(&HTTPStatusError{StatusCode: check.status}); got != check.want {
			t.Fatalf("status %d classified %q, want %q", check.status, got, check.want)
		}
	}
	if got := ClassifyRetry(errors.New("connection reset by peer")); got != RetryTransport {
		t.Fatalf("transport classified %q", got)
	}
	rng := rand.New(rand.NewSource(7))
	if got := ExponentialBackoff(3, "", time.Unix(0, 0), rng); got < 4*time.Second || got > 5*time.Second {
		t.Fatalf("backoff = %s", got)
	}
}

func TestRetryAfterParsesSecondsAndHTTPDate(t *testing.T) {
	if got, ok := ParseRetryAfter("12", time.Now()); !ok || got != 12*time.Second {
		t.Fatalf("seconds = %s, %v", got, ok)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got, ok := ParseRetryAfter("Fri, 02 Jan 2026 03:04:15 GMT", now); !ok || got != 10*time.Second {
		t.Fatalf("date = %s, %v", got, ok)
	}
}
