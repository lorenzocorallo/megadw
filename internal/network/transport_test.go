package network

import (
	"net/http"
	"testing"
	"time"
)

func TestNewHTTPClientBoundsConnectionsWithoutTotalRequestTimeout(t *testing.T) {
	client := NewHTTPClient(TransportConfig{
		ConnectTimeout:        7 * time.Second,
		ResponseHeaderTimeout: 11 * time.Second,
		MaxConnectionsPerHost: 5,
	})
	if client.Timeout != 0 {
		t.Fatalf("whole-request timeout = %s, want disabled", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if transport.MaxConnsPerHost != 5 || transport.MaxIdleConnsPerHost != 5 {
		t.Fatalf("connection limits = active %d idle %d", transport.MaxConnsPerHost, transport.MaxIdleConnsPerHost)
	}
	if transport.ResponseHeaderTimeout != 11*time.Second {
		t.Fatalf("response header timeout = %s", transport.ResponseHeaderTimeout)
	}
	if transport.Proxy != nil {
		t.Fatal("direct transport unexpectedly uses an environment proxy")
	}
}
