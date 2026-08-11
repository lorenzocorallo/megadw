package webui

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesEmbeddedShellAndClientRoutes(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}

	for _, route := range []string{"/", "/downloads/fixture"} {
		request := httptest.NewRequest(http.MethodGet, route, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want %d", route, recorder.Code, http.StatusOK)
		}
		body, readErr := io.ReadAll(recorder.Result().Body)
		if readErr != nil {
			t.Fatalf("read GET %s response: %v", route, readErr)
		}
		if !strings.Contains(string(body), "megadw") {
			t.Fatalf("GET %s response does not contain the embedded shell title", route)
		}
	}
}

func TestHandlerDoesNotServeHTMLForMissingHashedAsset(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/assets/stale-build.js", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want 404", recorder.Code)
	}
}

func TestHandlerSetsProductionCachePolicy(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}

	index := httptest.NewRecorder()
	handler.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := index.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("index cache policy = %q", got)
	}

	entries, err := fs.ReadDir(dist, "dist/assets")
	if err != nil || len(entries) == 0 {
		t.Fatalf("read embedded assets: entries=%d err=%v", len(entries), err)
	}
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/"+entries[0].Name(), nil))
	if got := asset.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset cache policy = %q", got)
	}
}
