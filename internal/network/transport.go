package network

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TransportConfig contains the bounded direct-transfer transport settings.
// Proxy-specific transports reuse this shape when that phase is implemented.
type TransportConfig struct {
	ConnectTimeout        time.Duration
	ResponseHeaderTimeout time.Duration
	MaxConnectionsPerHost int
}

type ProxyType string

const (
	ProxyDirect ProxyType = "direct"
	ProxyHTTP   ProxyType = "http"
	ProxyHTTPS  ProxyType = "https"
	ProxySOCKS5 ProxyType = "socks5"
)

type ProxyProfile struct {
	ID, Name           string
	Type               ProxyType
	Host               string
	Port               int
	Username, Password string
	Timeout            time.Duration
	Enabled            bool
}

func (p ProxyProfile) Validate() error {
	if p.Type != ProxyHTTP && p.Type != ProxyHTTPS && p.Type != ProxySOCKS5 {
		return fmt.Errorf("unsupported proxy type %q", p.Type)
	}
	if strings.TrimSpace(p.Host) == "" || p.Port < 1 || p.Port > 65535 {
		return fmt.Errorf("proxy host and port are invalid")
	}
	if p.Timeout <= 0 {
		return fmt.Errorf("proxy timeout must be positive")
	}
	return nil
}

// NewClientForProxy builds one reusable bounded transport for an immutable
// profile. Callers must close idle connections when the profile is replaced.
func NewClientForProxy(config TransportConfig, profile ProxyProfile) (*http.Client, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	client := newHTTPClient(config, profile.Timeout)
	transport := client.Transport.(*http.Transport)
	address := net.JoinHostPort(profile.Host, strconv.Itoa(profile.Port))
	if profile.Type == ProxyHTTP || profile.Type == ProxyHTTPS {
		scheme := "http"
		if profile.Type == ProxyHTTPS {
			scheme = "https"
		}
		proxyURL, err := url.Parse(scheme + "://" + address)
		if err != nil {
			return nil, fmt.Errorf("parse proxy address: %w", err)
		}
		if profile.Username != "" {
			proxyURL.User = url.UserPassword(profile.Username, profile.Password)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	} else {
		transport.Proxy = nil
		dialer := &net.Dialer{Timeout: profile.Timeout, KeepAlive: 30 * time.Second}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialSOCKS5(ctx, dialer, profile.Host, profile.Port, address, profile.Username, profile.Password)
		}
	}
	return client, nil
}

func newHTTPClient(config TransportConfig, timeout time.Duration) *http.Client {
	if config.ConnectTimeout <= 0 {
		config.ConnectTimeout = 15 * time.Second
	}
	if config.ResponseHeaderTimeout <= 0 {
		config.ResponseHeaderTimeout = 30 * time.Second
	}
	if config.MaxConnectionsPerHost <= 0 {
		config.MaxConnectionsPerHost = 8
	}
	if timeout <= 0 {
		timeout = config.ConnectTimeout
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: config.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.MaxIdleConns = config.MaxConnectionsPerHost * 2
	transport.MaxIdleConnsPerHost = config.MaxConnectionsPerHost
	transport.MaxConnsPerHost = config.MaxConnectionsPerHost
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = config.ConnectTimeout
	transport.ResponseHeaderTimeout = config.ResponseHeaderTimeout
	transport.ExpectContinueTimeout = time.Second
	return &http.Client{Transport: transport, Timeout: 0}
}

// NewHTTPClient returns a reusable direct HTTP client without a whole-request
// timeout. Body stalls are bounded separately by the transfer reader so a
// configured bandwidth limit cannot accidentally trip a total request timer.
func NewHTTPClient(config TransportConfig) *http.Client {
	return newHTTPClient(config, config.ConnectTimeout)
}

// TransportPool retains at most one current transport per profile ID. A
// changed profile closes the old idle connections before publishing the new
// client, preventing edits/deletes from accumulating stale pools.
type TransportPool struct {
	mu       sync.Mutex
	profiles map[string]ProxyProfile
	clients  map[string]*http.Client
	config   TransportConfig
}

func NewTransportPool(config TransportConfig) *TransportPool {
	return &TransportPool{profiles: map[string]ProxyProfile{}, clients: map[string]*http.Client{}, config: config}
}
func (p *TransportPool) Client(profile ProxyProfile) (*http.Client, error) {
	if profile.Type == ProxyDirect {
		return NewHTTPClient(p.config), nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if old, ok := p.profiles[profile.ID]; ok && old == profile && p.clients[profile.ID] != nil {
		return p.clients[profile.ID], nil
	}
	if old := p.clients[profile.ID]; old != nil {
		old.CloseIdleConnections()
	}
	client, err := NewClientForProxy(p.config, profile)
	if err != nil {
		return nil, err
	}
	p.profiles[profile.ID] = profile
	p.clients[profile.ID] = client
	return client, nil
}
func (p *TransportPool) Remove(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c := p.clients[id]; c != nil {
		c.CloseIdleConnections()
	}
	delete(p.clients, id)
	delete(p.profiles, id)
}
func (p *TransportPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, c := range p.clients {
		c.CloseIdleConnections()
		delete(p.clients, id)
	}
	p.profiles = map[string]ProxyProfile{}
}

func dialSOCKS5(ctx context.Context, direct *net.Dialer, proxyHost string, proxyPort int, targetAddress, username, password string) (net.Conn, error) {
	conn, err := direct.DialContext(ctx, "tcp", net.JoinHostPort(proxyHost, strconv.Itoa(proxyPort)))
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(direct.Timeout))
	fail := func(e error) (net.Conn, error) { _ = conn.Close(); return nil, e }
	method := byte(0)
	if username != "" {
		method = 2
	}
	if _, err = conn.Write([]byte{5, 1, method}); err != nil {
		return fail(err)
	}
	reply := make([]byte, 2)
	if _, err = io.ReadFull(conn, reply); err != nil {
		return fail(err)
	}
	if reply[0] != 5 {
		return fail(fmt.Errorf("SOCKS5 invalid version"))
	}
	if reply[1] == 2 {
		if username == "" {
			return fail(fmt.Errorf("SOCKS5 authentication required"))
		}
		ub, pb := []byte(username), []byte(password)
		if len(ub) > 255 || len(pb) > 255 {
			return fail(fmt.Errorf("SOCKS5 credentials are too long"))
		}
		auth := append([]byte{1, byte(len(ub))}, ub...)
		auth = append(auth, byte(len(pb)))
		auth = append(auth, pb...)
		if _, err = conn.Write(auth); err != nil {
			return fail(err)
		}
		authReply := make([]byte, 2)
		if _, err = io.ReadFull(conn, authReply); err != nil || authReply[1] != 0 {
			return fail(fmt.Errorf("SOCKS5 authentication failed"))
		}
	} else if reply[1] != 0 {
		return fail(fmt.Errorf("SOCKS5 authentication method rejected"))
	}
	targetHost, targetPortText, splitErr := net.SplitHostPort(targetAddress)
	if splitErr != nil {
		return fail(fmt.Errorf("SOCKS5 target address is invalid: %w", splitErr))
	}
	targetPort, splitErr := strconv.Atoi(targetPortText)
	if splitErr != nil || targetPort < 0 || targetPort > 65535 {
		return fail(fmt.Errorf("SOCKS5 target port is invalid"))
	}
	target := []byte(targetHost)
	if len(target) > 255 {
		return fail(fmt.Errorf("SOCKS5 target host is too long"))
	}
	request := []byte{5, 1, 0, 3, byte(len(target))}
	request = append(request, target...)
	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], uint16(targetPort))
	request = append(request, portBytes[:]...)
	if _, err = conn.Write(request); err != nil {
		return fail(err)
	}
	connectReply := make([]byte, 4)
	if _, err = io.ReadFull(conn, connectReply); err != nil {
		return fail(err)
	}
	if connectReply[0] != 5 || connectReply[1] != 0 {
		return fail(fmt.Errorf("SOCKS5 connect failed: %d", connectReply[1]))
	}
	var addressLength int
	switch connectReply[3] {
	case 1:
		addressLength = 4
	case 4:
		addressLength = 16
	case 3:
		one := []byte{0}
		if _, err = io.ReadFull(conn, one); err != nil {
			return fail(err)
		}
		addressLength = int(one[0])
	default:
		return fail(fmt.Errorf("SOCKS5 invalid address type"))
	}
	discard := make([]byte, addressLength+2)
	if _, err = io.ReadFull(conn, discard); err != nil {
		return fail(err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}
