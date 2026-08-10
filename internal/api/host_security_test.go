package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnsafeRequestRejectsAttackerControlledMatchingOriginAndHost(t *testing.T) {
	handler := New(Config{AllowedHosts: []string{"127.0.0.1:8080", "localhost:8080"}})
	request := httptest.NewRequest(http.MethodPost, "http://evil.example/api/v1/auth/setup", strings.NewReader(`{}`))
	request.Host = "evil.example"
	request.Header.Set("Origin", "http://evil.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("attacker Host status = %d, want 403", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/v1/auth/setup", strings.NewReader(`{}`))
	request.Host = "127.0.0.1:8080"
	request.Header.Set("Origin", "http://127.0.0.1:8080")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code == http.StatusForbidden {
		t.Fatal("configured listener Host was rejected")
	}
}
