package integration

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
)

// FakeProxy is a local explicit-routing fixture. It supports both ordinary
// HTTP proxy requests and CONNECT so the same fixture can validate HTTP and
// HTTPS proxy profiles without contacting the public internet.
type FakeProxy struct {
	server   *httptest.Server
	listener net.Listener
	requests atomic.Int64
	socks    bool
}

func NewFakeHTTPProxy() *FakeProxy {
	p := &FakeProxy{}
	p.server = httptest.NewServer(http.HandlerFunc(p.serveHTTP))
	return p
}

func NewFakeSOCKS5Proxy() *FakeProxy {
	p := &FakeProxy{socks: true}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	p.listener = listener
	go p.serveSOCKS()
	return p
}

func (p *FakeProxy) Close() {
	if p.server != nil {
		p.server.Close()
	}
	if p.listener != nil {
		_ = p.listener.Close()
	}
}
func (p *FakeProxy) Requests() int64 { return p.requests.Load() }
func (p *FakeProxy) HostPort() (string, int) {
	if p.socks {
		host, port, _ := net.SplitHostPort(p.listener.Addr().String())
		value, _ := strconv.Atoi(port)
		return host, value
	}
	parsed, _ := url.Parse(p.server.URL)
	value, _ := strconv.Atoi(parsed.Port())
	return parsed.Hostname(), value
}

func (p *FakeProxy) serveHTTP(w http.ResponseWriter, r *http.Request) {
	p.requests.Add(1)
	if r.Method == http.MethodConnect {
		p.serveCONNECT(w, r)
		return
	}
	request := r.Clone(r.Context())
	request.RequestURI = ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	response, err := transport.RoundTrip(request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	for key, values := range response.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func (p *FakeProxy) serveCONNECT(w http.ResponseWriter, r *http.Request) {
	connection, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "CONNECT unsupported", 501)
		return
	}
	target, err := net.Dial("tcp", r.Host)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	client, buffered, err := connection.Hijack()
	if err != nil {
		_ = target.Close()
		return
	}
	_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	_ = buffered.Flush()
	go func() { _, _ = io.Copy(target, client); _ = target.Close() }()
	go func() { _, _ = io.Copy(client, target); _ = client.Close() }()
}

func (p *FakeProxy) serveSOCKS() {
	for {
		connection, err := p.listener.Accept()
		if err != nil {
			return
		}
		go p.handleSOCKS(connection)
	}
}
func (p *FakeProxy) handleSOCKS(connection net.Conn) {
	defer connection.Close()
	p.requests.Add(1)
	reader := bufio.NewReader(connection)
	greeting := make([]byte, 3)
	if _, err := io.ReadFull(reader, greeting); err != nil {
		return
	}
	_, _ = connection.Write([]byte{5, 0})
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
		n, err := reader.ReadByte()
		if err != nil {
			return
		}
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
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		return
	}
	target, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(portBytes[0])<<8|int(portBytes[1]))))
	if err != nil {
		return
	}
	defer target.Close()
	_, _ = connection.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	go io.Copy(target, reader)
	_, _ = io.Copy(connection, target)
}
