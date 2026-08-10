package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorenzocorallo/megadw/internal/api"
	"github.com/lorenzocorallo/megadw/internal/app"
	"github.com/lorenzocorallo/megadw/internal/settings"
	"github.com/lorenzocorallo/megadw/tests/integration"
)

type responseEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestSetupLoginSettingsResolveAndCreateKeepSecretsOutOfSQLite(t *testing.T) {
	fixture := integration.NewFakeMegaServer()
	defer fixture.Close()
	stateDir := t.TempDir()
	config := app.Config{
		StateDir:       stateDir,
		DatabasePath:   filepath.Join(stateDir, "megad.sqlite3"),
		SecretKeyPath:  filepath.Join(stateDir, "secret.key"),
		MegaAPIBaseURL: fixture.APIBaseURL(),
		HTTPClient:     fixture.HTTPClient(),
	}
	application, err := app.Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	handler := api.New(api.Config{DB: application.DB, Secrets: application.Secrets, Settings: application.Settings, Auth: application.Auth, Mega: application.Mega})

	setup := doJSON(t, handler, http.MethodPost, "/api/v1/auth/setup", `{"username":"admin","password":"correct horse battery"}`, nil)
	if setup.status != http.StatusCreated {
		t.Fatalf("setup status = %d, body = %s", setup.status, setup.body)
	}
	cookie := setup.cookies[0]
	if cookie.Value == "" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %#v", cookie)
	}
	settingsResponse := doJSON(t, handler, http.MethodGet, "/api/v1/settings", "", cookie)
	if settingsResponse.status != http.StatusOK {
		t.Fatalf("settings status = %d, body = %s", settingsResponse.status, settingsResponse.body)
	}
	var value map[string]any
	decodeData(t, settingsResponse, &value)
	paths := value["paths"].(map[string]any)
	if paths["completeRoot"] != "" || paths["incompleteRoot"] != "" {
		t.Fatalf("fresh settings contain implicit transfer roots: %#v", paths)
	}

	unconfigured := doJSON(t, handler, http.MethodPost, "/api/v1/downloads", `{"url":"`+fixture.FileLink()+`"}`, cookie)
	if unconfigured.status != http.StatusConflict || !strings.Contains(unconfigured.body, "transfer_paths_unconfigured") {
		t.Fatalf("unconfigured create status = %d, body = %s", unconfigured.status, unconfigured.body)
	}

	resolved := doJSON(t, handler, http.MethodPost, "/api/v1/downloads/resolve", `{"url":"`+fixture.FileLink()+`"}`, cookie)
	if resolved.status != http.StatusOK || fixture.PayloadRequestCount() != 0 {
		t.Fatalf("resolve status = %d, payload requests = %d, body = %s", resolved.status, fixture.PayloadRequestCount(), resolved.body)
	}
	if strings.Contains(resolved.body, "FileKey") || strings.Contains(resolved.body, fixture.FileLink()) {
		t.Fatalf("resolve response leaked source secret: %s", resolved.body)
	}

	transferRoot := t.TempDir()
	configured := settings.Default()
	configured.Paths.IncompleteRoot = filepath.Join(transferRoot, "partial")
	configured.Paths.CompleteRoot = filepath.Join(transferRoot, "complete")
	encodedSettings, err := json.Marshal(configured)
	if err != nil {
		t.Fatal(err)
	}
	updated := doJSON(t, handler, http.MethodPut, "/api/v1/settings", string(encodedSettings), cookie)
	if updated.status != http.StatusOK {
		t.Fatalf("configured settings status = %d, body = %s", updated.status, updated.body)
	}

	created := doJSON(t, handler, http.MethodPost, "/api/v1/downloads", `{"url":"`+fixture.FileLink()+`","destinationSubdirectory":"incoming","startImmediately":false}`, cookie)
	if created.status != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.status, created.body)
	}
	if strings.Contains(created.body, "ciphertext") || strings.Contains(created.body, "PayloadURL") || strings.Contains(created.body, fixture.FileLink()) {
		t.Fatalf("create response leaked secret fields: %s", created.body)
	}
	var state string
	if err := application.DB.QueryRow(`SELECT state FROM download_jobs`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "ready" {
		t.Fatalf("state = %q, want ready when toggle is false", state)
	}
	var sourceCiphertext, fileCiphertext, payloadCiphertext []byte
	if err := application.DB.QueryRow(`SELECT source_key_ciphertext FROM download_jobs`).Scan(&sourceCiphertext); err != nil {
		t.Fatal(err)
	}
	if err := application.DB.QueryRow(`SELECT file_key_ciphertext, payload_url_ciphertext FROM download_files`).Scan(&fileCiphertext, &payloadCiphertext); err != nil {
		t.Fatal(err)
	}
	for name, ciphertext := range map[string][]byte{"source": sourceCiphertext, "file": fileCiphertext, "payload": payloadCiphertext} {
		if len(ciphertext) == 0 || strings.Contains(string(ciphertext), fixture.FileLink()) || strings.Contains(string(ciphertext), "fixture") {
			t.Fatalf("%s secret was not encrypted: %x", name, ciphertext)
		}
	}

	if err := application.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := app.Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restartedHandler := api.New(api.Config{DB: restarted.DB, Secrets: restarted.Secrets, Settings: restarted.Settings, Auth: restarted.Auth, Mega: restarted.Mega})
	me := doJSON(t, restartedHandler, http.MethodGet, "/api/v1/auth/me", "", cookie)
	if me.status != http.StatusOK {
		t.Fatalf("session after restart status = %d, body = %s", me.status, me.body)
	}
	list := doJSON(t, restartedHandler, http.MethodGet, "/api/v1/downloads", "", cookie)
	if list.status != http.StatusOK {
		t.Fatalf("download list after restart status = %d, body = %s", list.status, list.body)
	}
}

func TestSettingsPutIsAtomicAndOriginChecked(t *testing.T) {
	fixture := integration.NewFakeMegaServer()
	defer fixture.Close()
	stateDir := t.TempDir()
	application, err := app.Open(context.Background(), app.Config{StateDir: stateDir, DatabasePath: filepath.Join(stateDir, "db.sqlite3"), SecretKeyPath: filepath.Join(stateDir, "secret.key"), MegaAPIBaseURL: fixture.APIBaseURL(), HTTPClient: fixture.HTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	handler := api.New(api.Config{DB: application.DB, Secrets: application.Secrets, Settings: application.Settings, Auth: application.Auth, Mega: application.Mega})
	setup := doJSON(t, handler, http.MethodPost, "/api/v1/auth/setup", `{"username":"admin","password":"correct horse battery"}`, nil)
	cookie := setup.cookies[0]
	bad := `{"paths":{"incompleteRoot":"relative","completeRoot":"/tmp/complete"},"downloads":{"autoStart":true,"segmentSizeBytes":8388608,"workersPerFile":4,"maxActiveFiles":2,"maxGlobalWorkers":8,"globalSpeedLimitBytesPerSecond":0,"perJobDefaultLimitBytesPerSecond":0,"conflictPolicy":"rename","checkpointIntervalMs":2000,"checkpointBytes":268435456,"normalRetryLimit":5},"network":{"connectTimeoutSeconds":15,"responseHeaderTimeoutSeconds":30,"readIdleTimeoutSeconds":90},"ui":{"theme":"system","locale":"en"}}`
	badResponse := doJSON(t, handler, http.MethodPut, "/api/v1/settings", bad, cookie)
	if badResponse.status != http.StatusBadRequest {
		t.Fatalf("bad settings status = %d, body = %s", badResponse.status, badResponse.body)
	}
	crossOrigin := doJSONWithOrigin(t, handler, http.MethodPut, "/api/v1/settings", bad, cookie, "https://evil.example")
	if crossOrigin.status != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, body = %s", crossOrigin.status, crossOrigin.body)
	}
	good := doJSON(t, handler, http.MethodGet, "/api/v1/settings", "", cookie)
	if good.status != http.StatusOK {
		t.Fatalf("settings after rejected put status = %d, body = %s", good.status, good.body)
	}
}

func TestVersionExposesReleaseBuildMetadata(t *testing.T) {
	handler := api.New(api.Config{Version: "1.2.3", Commit: "abc123", BuildTime: "2026-08-10T12:00:00Z"})
	response := doJSON(t, handler, http.MethodGet, "/api/v1/version", "", nil)
	if response.status != http.StatusOK {
		t.Fatalf("version status = %d, body = %s", response.status, response.body)
	}
	for _, value := range []string{"\"version\":\"1.2.3\"", "\"commit\":\"abc123\"", "\"buildTime\":\"2026-08-10T12:00:00Z\""} {
		if !strings.Contains(response.body, value) {
			t.Fatalf("version response missing %s: %s", value, response.body)
		}
	}
}

type testResponse struct {
	status  int
	body    string
	cookies []*http.Cookie
}

func doJSON(t *testing.T, handler http.Handler, method, path, body string, cookie *http.Cookie) testResponse {
	return doJSONWithOrigin(t, handler, method, path, body, cookie, "")
}

func doJSONWithOrigin(t *testing.T, handler http.Handler, method, path, body string, cookie *http.Cookie, origin string) testResponse {
	request := httptest.NewRequest(method, "http://example.test"+path, strings.NewReader(body))
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return testResponse{status: response.StatusCode, body: string(data), cookies: response.Cookies()}
}

func decodeData(t *testing.T, response testResponse, destination any) {
	var envelope responseEnvelope
	if err := json.Unmarshal([]byte(response.body), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error != nil {
		t.Fatalf("API error %s: %s", envelope.Error.Code, envelope.Error.Message)
	}
	if err := json.Unmarshal(envelope.Data, destination); err != nil {
		t.Fatal(err)
	}
}
