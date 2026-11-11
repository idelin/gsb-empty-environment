package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
)

// The sandboxed test environment forbids binding TCP sockets, so the HTTP
// tests run the real net/http client and server over net.Pipe connections.
// The listener stamps each accepted connection with a configurable remote
// address, which lets the tests simulate arbitrary peers (external IPs,
// IPv6 with port) while still exercising the actual HTTP wire protocol.

type staticAddr struct {
	network string
	addr    string
}

func (a staticAddr) Network() string { return a.network }
func (a staticAddr) String() string  { return a.addr }

// peerConn overrides the remote address reported to the HTTP server.
type peerConn struct {
	net.Conn
	remote net.Addr
}

func (c peerConn) RemoteAddr() net.Addr { return c.remote }

// pipeListener is an in-memory net.Listener with a fixed peer address.
type pipeListener struct {
	remote net.Addr
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener(remoteAddr string) *pipeListener {
	return &pipeListener{
		remote: staticAddr{network: "tcp", addr: remoteAddr},
		conns:  make(chan net.Conn),
		closed: make(chan struct{}),
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return peerConn{Conn: conn, remote: l.remote}, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return l.remote }

// dial connects a client end and queues the server end for Accept.
func (l *pipeListener) dial(ctx context.Context) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.closed:
		client.Close()
		server.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	}
}

// originResponse mirrors Origin with string addresses for JSON transport.
type originResponse struct {
	ClientIP      string   `json:"client_ip"`
	Path          []string `json:"path"`
	ConfigVersion string   `json:"config_version"`
}

// httpTestServer runs a real http.Server (with the resolver middleware)
// over a pipeListener whose peer is remoteAddr.
type httpTestServer struct {
	client *http.Client
	server *http.Server
}

func newHTTPTestServer(t *testing.T, resolver *Resolver, remoteAddr string) *httpTestServer {
	t.Helper()
	handler := resolver.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin, ok := FromContext(r.Context())
		if !ok {
			t.Error("origin missing from request context")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		resp := originResponse{ClientIP: origin.ClientIP.String(), ConfigVersion: origin.ConfigVersion}
		for _, hop := range origin.Path {
			resp.Path = append(resp.Path, hop.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	listener := newPipeListener(remoteAddr)
	server := &http.Server{Handler: handler}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("Serve: %v", err)
		}
	}()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return listener.dial(ctx)
		},
	}
	ts := &httpTestServer{client: &http.Client{Transport: transport}, server: server}
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		_ = server.Close()
	})
	return ts
}

func (s *httpTestServer) do(t *testing.T, xffLines ...string) (int, originResponse) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://gateway.test/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for _, line := range xffLines {
		req.Header.Add(ForwardedForHeader, line)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	var origin originResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&origin); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
	return resp.StatusCode, origin
}

// TestDirectConnectionForgedHeader: the peer is not in any trusted CIDR, so a
// forged X-Forwarded-For header must be ignored entirely.
func TestDirectConnectionForgedHeader(t *testing.T) {
	resolver := mustResolver(t, "cfg-direct", "10.0.0.0/8")
	server := newHTTPTestServer(t, resolver, "203.0.113.99:51000")

	status, origin := server.do(t, "9.9.9.9, 8.8.8.8")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if origin.ClientIP != "203.0.113.99" {
		t.Fatalf("ClientIP = %q, want the real peer (forged header ignored)", origin.ClientIP)
	}
	if len(origin.Path) != 1 {
		t.Fatalf("Path = %v, want only the direct peer", origin.Path)
	}
	if origin.ConfigVersion != "cfg-direct" {
		t.Fatalf("ConfigVersion = %q, want %q", origin.ConfigVersion, "cfg-direct")
	}
}

// TestConflictingHeaderLines: two different X-Forwarded-For lines are
// ambiguous and must be rejected; identical duplicates are tolerated.
func TestConflictingHeaderLines(t *testing.T) {
	resolver := mustResolver(t, "cfg-multi", "127.0.0.0/8")
	server := newHTTPTestServer(t, resolver, "127.0.0.1:8080")

	status, _ := server.do(t, "203.0.113.1", "198.51.100.1")
	if status != http.StatusBadRequest {
		t.Fatalf("conflicting lines: status = %d, want 400", status)
	}

	status, origin := server.do(t, "203.0.113.1", "203.0.113.1")
	if status != http.StatusOK {
		t.Fatalf("identical duplicates: status = %d, want 200", status)
	}
	if origin.ClientIP != "203.0.113.1" {
		t.Fatalf("ClientIP = %q, want %q", origin.ClientIP, "203.0.113.1")
	}
}

// TestIPv6PeerWithPort: the peer arrives as "[::1]:port" and the chain is
// resolved against IPv6 CIDRs.
func TestIPv6PeerWithPort(t *testing.T) {
	resolver := mustResolver(t, "cfg-v6", "::1/128", "2001:db8::/32")
	server := newHTTPTestServer(t, resolver, "[::1]:443")

	// Far-to-near on the wire: client 203.0.113.7 behind proxy 2001:db8::10.
	status, origin := server.do(t, "203.0.113.7, 2001:db8::10")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if origin.ClientIP != "203.0.113.7" {
		t.Fatalf("ClientIP = %q, want %q", origin.ClientIP, "203.0.113.7")
	}
	wantPath := []string{"::1", "2001:db8::10", "203.0.113.7"}
	if fmt.Sprint(origin.Path) != fmt.Sprint(wantPath) {
		t.Fatalf("Path = %v, want %v", origin.Path, wantPath)
	}
	if origin.ConfigVersion != "cfg-v6" {
		t.Fatalf("ConfigVersion = %q, want %q", origin.ConfigVersion, "cfg-v6")
	}
}

// TestUntrustedNodeInTrustedChain: the walk must stop at the first untrusted
// hop; records beyond it are not consumed.
func TestUntrustedNodeInTrustedChain(t *testing.T) {
	resolver := mustResolver(t, "cfg-chain", "127.0.0.0/8", "10.0.0.0/8")
	server := newHTTPTestServer(t, resolver, "127.0.0.1:9000")

	// Near-to-far after the peer: 10.0.0.1 (trusted), 172.16.0.5 (untrusted),
	// 192.0.2.1 (must not be consumed).
	status, origin := server.do(t, "192.0.2.1, 172.16.0.5, 10.0.0.1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if origin.ClientIP != "172.16.0.5" {
		t.Fatalf("ClientIP = %q, want %q (first untrusted hop)", origin.ClientIP, "172.16.0.5")
	}
	wantPath := []string{"127.0.0.1", "10.0.0.1", "172.16.0.5"}
	if fmt.Sprint(origin.Path) != fmt.Sprint(wantPath) {
		t.Fatalf("Path = %v, want %v", origin.Path, wantPath)
	}
}

// TestAmbiguousRecordOverHTTP: a consumed record carrying a port is rejected.
func TestAmbiguousRecordOverHTTP(t *testing.T) {
	resolver := mustResolver(t, "cfg-strict", "127.0.0.0/8")
	server := newHTTPTestServer(t, resolver, "127.0.0.1:8080")

	status, _ := server.do(t, "203.0.113.1:5000")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
}

// TestParseRemoteAddr covers the RemoteAddr forms the HTTP layer accepts.
func TestParseRemoteAddr(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:8080":       "127.0.0.1",
		"[::1]:8080":           "::1",
		"[2001:db8::1]:443":    "2001:db8::1",
		"10.0.0.1":             "10.0.0.1",
		"[::ffff:10.0.0.1]:80": "10.0.0.1",
	}
	for input, want := range cases {
		addr, err := parseRemoteAddr(input)
		if err != nil {
			t.Errorf("parseRemoteAddr(%q): %v", input, err)
			continue
		}
		if addr.String() != want {
			t.Errorf("parseRemoteAddr(%q) = %v, want %v", input, addr, want)
		}
	}
	for _, bad := range []string{"", "host:80", "[::1]", "1.2.3.4:99999"} {
		if _, err := parseRemoteAddr(bad); err == nil {
			t.Errorf("parseRemoteAddr(%q) succeeded, want error", bad)
		}
	}
}
