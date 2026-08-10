package network

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPProxyProfileRoutesRequestsAndPoolReusesClient(t *testing.T) {
	var proxyRequests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("target")) }))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyRequests.Add(1)
		if r.URL.IsAbs() == false {
			t.Errorf("proxy request URL is not absolute: %s", r.URL)
		}
		req := r.Clone(r.Context())
		req.RequestURI = ""
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	port, _ := strconv.Atoi(proxyURL.Port())
	client, err := NewClientForProxy(TransportConfig{ConnectTimeout: time.Second, ResponseHeaderTimeout: time.Second, MaxConnectionsPerHost: 2}, ProxyProfile{ID: "p", Type: ProxyHTTP, Host: proxyURL.Hostname(), Port: port, Timeout: time.Second, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if string(body) != "target" {
		t.Fatalf("body=%q", body)
	}
	if proxyRequests.Load() != 1 {
		t.Fatalf("proxy requests=%d", proxyRequests.Load())
	}
	p := NewTransportPool(TransportConfig{MaxConnectionsPerHost: 2})
	one, err := p.Client(ProxyProfile{ID: "p", Type: ProxyHTTP, Host: proxyURL.Hostname(), Port: port, Timeout: time.Second, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	two, err := p.Client(ProxyProfile{ID: "p", Type: ProxyHTTP, Host: proxyURL.Hostname(), Port: port, Timeout: time.Second, Enabled: true})
	if err != nil || one != two {
		t.Fatalf("pool did not reuse client: %p %p %v", one, two, err)
	}
	p.Remove("p")
	p.Close()
}

func TestSOCKS5ProxyProfileRoutesRequests(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("socks-target")) }))
	defer target.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go serveTestSOCKS5(listener)
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	client, err := NewClientForProxy(TransportConfig{ConnectTimeout: time.Second, ResponseHeaderTimeout: time.Second, MaxConnectionsPerHost: 1}, ProxyProfile{ID: "s", Type: ProxySOCKS5, Host: host, Port: port, Timeout: time.Second, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if string(body) != "socks-target" {
		t.Fatalf("body=%q", body)
	}
}

func serveTestSOCKS5(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			reader := bufio.NewReader(conn)
			greeting := make([]byte, 3)
			if _, err := io.ReadFull(reader, greeting); err != nil {
				return
			}
			_, _ = conn.Write([]byte{5, 0})
			request := make([]byte, 4)
			if _, err := io.ReadFull(reader, request); err != nil {
				return
			}
			var host string
			switch request[3] {
			case 1:
				b := make([]byte, 4)
				_, _ = io.ReadFull(reader, b)
				host = net.IP(b).String()
			case 3:
				n, _ := reader.ReadByte()
				b := make([]byte, n)
				_, _ = io.ReadFull(reader, b)
				host = string(b)
			case 4:
				b := make([]byte, 16)
				_, _ = io.ReadFull(reader, b)
				host = net.IP(b).String()
			default:
				return
			}
			portBytes := make([]byte, 2)
			_, _ = io.ReadFull(reader, portBytes)
			target, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(portBytes[0])<<8|int(portBytes[1]))))
			if err != nil {
				return
			}
			defer target.Close()
			_, _ = conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
			go io.Copy(target, reader)
			_, _ = io.Copy(conn, target)
		}()
	}
}

func TestProxyProfileRejectsInvalidType(t *testing.T) {
	if _, err := NewClientForProxy(TransportConfig{}, ProxyProfile{Type: ProxyType(strings.ToLower("invalid")), Host: "localhost", Port: 1, Timeout: time.Second}); err == nil {
		t.Fatal("invalid proxy type accepted")
	}
}
