package main

import (
	"net/http"
	"net/http/httptest"
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
