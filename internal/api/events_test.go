package api_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorenzocorallo/megadw/internal/api"
	"github.com/lorenzocorallo/megadw/internal/app"
	"github.com/lorenzocorallo/megadw/internal/events"
	"github.com/lorenzocorallo/megadw/tests/integration"
)

func TestAuthenticatedSSEStreamsEventsAndDisconnectsCleanly(t *testing.T) {
	fixture := integration.NewFakeMegaServer()
	defer fixture.Close()
	stateDir := t.TempDir()
	application, err := app.Open(context.Background(), app.Config{
		StateDir:       stateDir,
		DatabasePath:   filepath.Join(stateDir, "megad.sqlite3"),
		SecretKeyPath:  filepath.Join(stateDir, "secret.key"),
		MegaAPIBaseURL: fixture.APIBaseURL(),
		HTTPClient:     fixture.HTTPClient(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	handler := api.New(api.Config{
		DB:         application.DB,
		Secrets:    application.Secrets,
		Settings:   application.Settings,
		Auth:       application.Auth,
		Mega:       application.Mega,
		Downloads:  application.Downloads,
		Transports: application.Transports,
		Events:     application.Events,
	})
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/events", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated SSE status = %d", unauthorized.Code)
	}
	setup := doJSON(t, handler, http.MethodPost, "/api/v1/auth/setup", `{"username":"admin","password":"correct horse battery"}`, nil)
	if setup.status != http.StatusCreated {
		t.Fatalf("setup status = %d", setup.status)
	}

	server := httptest.NewServer(handler)
	defer server.Close()
	requestContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(setup.cookies[0])
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" {
		t.Fatalf("SSE response = %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatal(readErr)
		}
		if line == "\n" {
			break
		}
	}
	application.Events.Publish(events.Event{Name: events.JobUpdated, JobID: "job-1", Data: map[string]any{"state": "downloading"}})
	foundEvent := false
	for !foundEvent {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.TrimSpace(line) == "event: job.updated" {
			foundEvent = true
		}
	}
	response.Body.Close()
	cancel()

	reconnectContext, reconnectCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer reconnectCancel()
	reconnectRequest, err := http.NewRequestWithContext(reconnectContext, http.MethodGet, server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	reconnectRequest.AddCookie(setup.cookies[0])
	reconnectResponse, err := server.Client().Do(reconnectRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer reconnectResponse.Body.Close()
	if reconnectResponse.StatusCode != http.StatusOK {
		t.Fatalf("reconnect response = %d", reconnectResponse.StatusCode)
	}
	reconnectReader := bufio.NewReader(reconnectResponse.Body)
	readSSEPrelude(t, reconnectReader)
	application.Events.Publish(events.Event{Name: events.JobUpdated, JobID: "job-2", Data: map[string]any{"state": "queued"}})
	for {
		line, readErr := reconnectReader.ReadString('\n')
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.TrimSpace(line) == "event: job.updated" {
			break
		}
	}
}

func readSSEPrelude(t *testing.T, reader *bufio.Reader) {
	t.Helper()
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\n" {
			return
		}
	}
}
