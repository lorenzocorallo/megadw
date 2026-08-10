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

func TestProxyProfileTimeoutControlsProxyConnectionAndTLSHandshake(t *testing.T) {
	client, err := NewClientForProxy(TransportConfig{
		ConnectTimeout:        15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxConnectionsPerHost: 8,
	}, ProxyProfile{
		ID:      "proxy-timeout",
		Type:    ProxyHTTPS,
		Host:    "proxy.example.test",
		Port:    8443,
		Timeout: 3 * time.Second,
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	transport := client.Transport.(*http.Transport)
	if transport.TLSHandshakeTimeout != 3*time.Second {
		t.Fatalf("TLS handshake timeout = %s, want 3s", transport.TLSHandshakeTimeout)
	}
}
