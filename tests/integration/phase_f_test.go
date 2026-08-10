package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lorenzocorallo/megadw/internal/download"
	"github.com/lorenzocorallo/megadw/internal/network"
	"github.com/lorenzocorallo/megadw/internal/settings"
	"github.com/lorenzocorallo/megadw/internal/store"
)

func TestPhaseFExpiredPayloadURLRefreshesAndCompletes(t *testing.T) {
	fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{ExpirePayloadURL: true})
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseDManager(t, fixture)
	jobID := "phase-f-refresh-url"
	insertPhaseDJob(t, db, secrets, fixture, roots, jobID, fixture.FileLink())
	if err := manager.RunJob(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	record, err := db.GetDownloadJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != string(download.JobCompleted) {
		t.Fatalf("state = %q", record.State)
	}
	if fixture.CommandRequestCount() < 2 {
		t.Fatalf("payload URL was not refreshed; command requests = %d", fixture.CommandRequestCount())
	}
}

func TestPhaseFRetry429And5xxRecover(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		retry  string
	}{{name: "429", status: 429, retry: "1"}, {name: "503", status: 503}} {
		t.Run(test.name, func(t *testing.T) {
			fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{StatusCode: test.status, RetryAfter: test.retry})
			defer fixture.Close()
			manager, db, secrets, roots := newPhaseDManager(t, fixture)
			jobID := "phase-f-retry-" + test.name
			insertPhaseDJob(t, db, secrets, fixture, roots, jobID, fixture.FileLink())
			go func() { time.Sleep(100 * time.Millisecond); fixture.SetOptions(FakeMegaServerOptions{}) }()
			if err := manager.RunJob(context.Background(), jobID); err != nil {
				t.Fatal(err)
			}
			record, err := db.GetDownloadJob(context.Background(), jobID)
			if err != nil {
				t.Fatal(err)
			}
			if record.State != string(download.JobCompleted) {
				t.Fatalf("state = %q", record.State)
			}
			if fixture.PayloadRequestCount() < 2 {
				t.Fatalf("retry did not issue another request: %d", fixture.PayloadRequestCount())
			}
		})
	}
}

func TestPhaseFQuotaWaitPreservesSelectionAndResumeNow(t *testing.T) {
	fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{StatusCode: 509})
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseDManager(t, fixture)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	jobID := "phase-f-quota"
	insertPhaseDJob(t, db, secrets, fixture, roots, jobID, fixture.FileLink())
	if err := manager.RunJob(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	record, err := db.GetDownloadJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != string(download.JobWaitingQuota) {
		t.Fatalf("state = %q, want waiting_quota", record.State)
	}
	if record.QuotaNextRetryAt == nil {
		t.Fatal("quota retry deadline was not persisted")
	}
	fixture.SetOptions(FakeMegaServerOptions{})
	if err := manager.ResumeJob(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		record, err = db.GetDownloadJob(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if record.State == string(download.JobCompleted) {
			return
		}
		if record.State == string(download.JobFailed) {
			t.Fatalf("resume failed: %s", record.LastErrorMessage)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("resume did not complete; state=%q", record.State)
}

func TestPhaseFHTTPAndSOCKS5ProfilesRouteTransferRequests(t *testing.T) {
	for _, test := range []struct {
		name  string
		socks bool
	}{{"http", false}, {"socks5", true}} {
		t.Run(test.name, func(t *testing.T) {
			fixture := NewFakeMegaServer()
			defer fixture.Close()
			var proxy *FakeProxy
			if test.socks {
				proxy = NewFakeSOCKS5Proxy()
			} else {
				proxy = NewFakeHTTPProxy()
			}
			defer proxy.Close()
			host, port := proxy.HostPort()
			manager, db, secrets, roots := newPhaseFProxyManager(t, fixture, network.ProxyType(test.name), host, port)
			jobID := "phase-f-proxy-" + test.name
			insertPhaseDJob(t, db, secrets, fixture, roots, jobID, fixture.FileLink())
			if _, err := db.Exec(`UPDATE download_jobs SET proxy_id=? WHERE id=?`, "phase-f-proxy", jobID); err != nil {
				t.Fatal(err)
			}
			if err := manager.RunJob(context.Background(), jobID); err != nil {
				t.Fatal(err)
			}
			record, err := db.GetDownloadJob(context.Background(), jobID)
			if err != nil {
				t.Fatal(err)
			}
			if record.State != string(download.JobCompleted) {
				t.Fatalf("state=%q", record.State)
			}
			if proxy.Requests() == 0 {
				t.Fatal("proxy observed no transfer request")
			}
		})
	}
}

func newPhaseFProxyManager(t *testing.T, fixture *FakeMegaServer, proxyType network.ProxyType, host string, port int) (*download.Manager, *store.DB, *store.SecretStore, phaseDRoots) {
	t.Helper()
	root := t.TempDir()
	roots := phaseDRoots{incomplete: filepath.Join(root, "incomplete"), complete: filepath.Join(root, "complete")}
	db, err := store.Open(context.Background(), filepath.Join(root, "db.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	secrets, err := store.OpenSecretStore(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := settings.NewService(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.InsertProxyProfile(context.Background(), store.ProxyProfileInput{ID: "phase-f-proxy", Name: "fixture", Type: string(proxyType), Host: host, Port: port, TimeoutSeconds: 5, Enabled: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	pool := network.NewTransportPool(network.TransportConfig{ConnectTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxConnectionsPerHost: 4})
	t.Cleanup(pool.Close)
	manager, err := download.NewManager(download.Config{DB: db, Secrets: secrets, Mega: fixture.Client(), Settings: service, TransportPool: pool, CheckpointInterval: time.Second, CheckpointBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager, db, secrets, roots
}
