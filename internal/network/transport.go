package network

import (
	"net"
	"net/http"
	"time"
)

// TransportConfig contains the bounded direct-transfer transport settings.
// Proxy-specific transports reuse this shape when that phase is implemented.
type TransportConfig struct {
	ConnectTimeout        time.Duration
	ResponseHeaderTimeout time.Duration
	MaxConnectionsPerHost int
}

// NewHTTPClient returns a reusable direct HTTP client without a whole-request
// timeout. Body stalls are bounded separately by the transfer reader so a
// configured bandwidth limit cannot accidentally trip a total request timer.
func NewHTTPClient(config TransportConfig) *http.Client {
	if config.ConnectTimeout <= 0 {
		config.ConnectTimeout = 15 * time.Second
	}
	if config.ResponseHeaderTimeout <= 0 {
		config.ResponseHeaderTimeout = 30 * time.Second
	}
	if config.MaxConnectionsPerHost <= 0 {
		config.MaxConnectionsPerHost = 8
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{
		Timeout:   config.ConnectTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.MaxIdleConns = config.MaxConnectionsPerHost * 2
	transport.MaxIdleConnsPerHost = config.MaxConnectionsPerHost
	transport.MaxConnsPerHost = config.MaxConnectionsPerHost
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = config.ConnectTimeout
	transport.ResponseHeaderTimeout = config.ResponseHeaderTimeout
	transport.ExpectContinueTimeout = time.Second
	return &http.Client{Transport: transport}
}
