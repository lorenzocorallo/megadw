package webui

import (
	"io"
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
		if !strings.Contains(string(body), "MEGA Downloader") {
			t.Fatalf("GET %s response does not contain the embedded shell title", route)
		}
	}
}
