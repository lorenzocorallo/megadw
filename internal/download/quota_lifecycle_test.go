package download

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lorenzocorallo/megadw/internal/mega"
	"github.com/lorenzocorallo/megadw/internal/store"
)

func TestCloseContextWaitsForRunningQuotaTimerCallback(t *testing.T) {
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
		Mega:    mega.NewClient(&http.Client{Timeout: time.Second}, "https://example.invalid"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	manager.scheduleQuotaRetry("missing-job", 0)
	time.Sleep(50 * time.Millisecond)
	manager.mu.Lock()
	timer := manager.quotaTimers["missing-job"]
	manager.mu.Unlock()
	if timer == nil {
		t.Fatal("quota timer was not registered")
	}

	closeContext, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := manager.CloseContext(closeContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CloseContext error = %v, want deadline while callback owns database work", err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-timer.done:
	case <-time.After(time.Second):
		t.Fatal("quota timer callback did not finish after database was released")
	}
}
