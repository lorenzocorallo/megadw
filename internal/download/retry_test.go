package download

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lorenzocorallo/megadw/internal/mega"
	"github.com/lorenzocorallo/megadw/internal/settings"
	"github.com/lorenzocorallo/megadw/internal/store"
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

func TestManagerHonorsZeroRetryLimitFromSettings(t *testing.T) {
	root := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(root, "megadw.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secrets, err := store.OpenSecretStore(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := settings.NewService(database)
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Default()
	value.Downloads.NormalRetryLimit = 0
	if err := service.Update(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(Config{
		DB:       database,
		Secrets:  secrets,
		Mega:     mega.NewClient(http.DefaultClient, "https://example.invalid"),
		Settings: service,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if manager.normalRetryLimit != 0 {
		t.Fatalf("normal retry limit = %d, want configured zero", manager.normalRetryLimit)
	}
}

func TestManagerAppliesSafeRuntimeSettings(t *testing.T) {
	root := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(root, "megadw.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secrets, err := store.OpenSecretStore(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(Config{
		DB:      database,
		Secrets: secrets,
		Mega:    mega.NewClient(http.DefaultClient, "https://example.invalid"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	value := settings.Default()
	value.Downloads.GlobalSpeedLimitBytesPerSecond = 12_345
	value.Downloads.PerJobDefaultLimitBytesPerSecond = 6_789
	value.Downloads.NormalRetryLimit = 0
	value.Downloads.WorkersPerFile = 3
	value.Downloads.ConflictPolicy = "fail"
	value.Downloads.CheckpointIntervalMs = 750
	value.Downloads.CheckpointBytes = 2 << 20
	value.Network.ReadIdleTimeoutSeconds = 12
	manager.ApplySettings(value)

	runtime := manager.runtimeSettings()
	if runtime.perJobLimit != 6_789 || runtime.normalRetryLimit != 0 || runtime.workersPerFile != 3 || runtime.conflictPolicy != "fail" || runtime.checkpointInterval != 750*time.Millisecond || runtime.checkpointBytes != 2<<20 || runtime.readIdleTimeout != 12*time.Second {
		t.Fatalf("applied runtime settings = %#v", runtime)
	}
	if manager.globalLimiter.Rate() != 12_345 {
		t.Fatalf("global limiter rate = %d", manager.globalLimiter.Rate())
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
