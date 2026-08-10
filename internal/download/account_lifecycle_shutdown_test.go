package download

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lorenzocorallo/megadw/internal/mega"
	"github.com/lorenzocorallo/megadw/internal/store"
)

func TestAuthenticatedAccountShutdownHasNoMegaLifecycleLeak(t *testing.T) {
	var treePollRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/sc" || request.URL.Path != "/cs" {
			treePollRequests.Add(1)
			http.NotFound(writer, request)
			return
		}
		var commands []map[string]any
		if err := json.NewDecoder(request.Body).Decode(&commands); err != nil || len(commands) != 1 {
			writeShutdownJSON(writer, []any{-2})
			return
		}
		if command, _ := commands[0]["a"].(string); command == "uq" && request.URL.Query().Get("sid") == "shutdown-session" {
			writeShutdownJSON(writer, []any{map[string]any{"mstrg": 1, "cstrg": 0}})
			return
		}
		writeShutdownJSON(writer, []any{-15})
	}))
	defer server.Close()

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
	session, err := secrets.Encrypt([]byte("shutdown-session"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.InsertMegaAccount(context.Background(), store.MegaAccountInput{ID: "shutdown-account", Label: "Shutdown", Email: "shutdown@example.test", SessionCiphertext: session, Status: "active"}, time.Now()); err != nil {
		t.Fatal(err)
	}

	client := mega.NewClient(server.Client(), server.URL)
	manager, err := NewManager(Config{DB: database, Secrets: secrets, Mega: client, CheckpointInterval: time.Second, CheckpointBytes: 1 << 20, ReadIdleTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	baseline := runtime.NumGoroutine()
	if _, _, err := manager.workerForJob(context.Background(), &store.DownloadJobRecord{AccountID: "shutdown-account"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.CloseContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	client.CloseIdleConnections()
	server.Close()
	runtime.GC()
	if treePollRequests.Load() != 0 {
		t.Fatalf("filesystem tree/event requests = %d", treePollRequests.Load())
	}
	if got := runtime.NumGoroutine(); got > baseline+2 {
		t.Fatalf("goroutines after authenticated shutdown = %d, baseline %d", got, baseline)
	}
}

func writeShutdownJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}
