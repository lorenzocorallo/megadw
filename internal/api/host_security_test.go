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

func TestUnsafeRequestAcceptsExplicitHTTPSReverseProxyOrigin(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://downloads.example.test/api/v1/auth/setup", strings.NewReader(`{}`))
	request.Host = "downloads.example.test"
	request.Header.Set("Origin", "https://downloads.example.test")

	insecure := httptest.NewRecorder()
	New(Config{AllowedHosts: []string{"downloads.example.test"}}).ServeHTTP(insecure, request.Clone(request.Context()))
	if insecure.Code != http.StatusForbidden {
		t.Fatalf("HTTPS proxy origin without secure-cookie opt-in status = %d, want 403", insecure.Code)
	}

	secure := httptest.NewRecorder()
	New(Config{AllowedHosts: []string{"downloads.example.test"}, SecureCookies: true}).ServeHTTP(secure, request.Clone(request.Context()))
	if secure.Code == http.StatusForbidden {
		t.Fatal("explicit HTTPS reverse-proxy origin was rejected")
	}
}
