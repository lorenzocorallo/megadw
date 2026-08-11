package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAllowedHostsIncludesListenerAndExplicitProxyName(t *testing.T) {
	hosts := allowedHosts("127.0.0.1:8080", "downloads.example.test")
	want := map[string]bool{
		"127.0.0.1:8080":         false,
		"localhost:8080":         false,
		"downloads.example.test": false,
	}
	for _, host := range hosts {
		if _, ok := want[host]; ok {
			want[host] = true
		}
	}
	for host, found := range want {
		if !found {
			t.Fatalf("allowed hosts omitted %q: %v", host, hosts)
		}
	}
}

func TestHealthcheckAcceptsHealthyAPIEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":{"status":"ok","database":"ok"}}`))
	}))
	defer server.Close()
	if err := runHealthcheck(server.URL); err != nil {
		t.Fatalf("healthy response rejected: %v", err)
	}
}

func TestHealthcheckRejectsDegradedAPIEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":{"status":"degraded","database":"error"}}`))
	}))
	defer server.Close()
	if err := runHealthcheck(server.URL); err == nil {
		t.Fatal("degraded response accepted")
	}
}

func TestSecurityHeadersProtectApplicationResponses(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
	for header, want := range map[string]string{
		"Content-Security-Policy": "frame-ancestors 'none'",
		"Referrer-Policy":         "no-referrer",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
	} {
		if got := response.Header().Get(header); !strings.Contains(got, want) {
			t.Fatalf("%s = %q, want it to contain %q", header, got, want)
		}
	}
}
