package dialpolicy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
)

// pipeAddr is a net.Addr carrying a concrete peer IP so that the policy layer
// and tests can reason about the "real" remote endpoint without binding a
// socket. The sandbox used during development forbids real listeners, so the
// whole topology is emulated with net.Pipe.
type pipeAddr struct{ ip netip.Addr }

func (a pipeAddr) Network() string { return "tcp" }
func (a pipeAddr) String() string  { return net.JoinHostPort(a.ip.String(), "80") }

// pipeConn wraps net.Pipe endpoints with stable local/remote addresses.
type pipeConn struct {
	net.Conn
	local, remote net.Addr
}

func (c *pipeConn) LocalAddr() net.Addr  { return c.local }
func (c *pipeConn) RemoteAddr() net.Addr { return c.remote }

// pipeListener is an in-memory listener keyed by the IP address a dialer
// targeted. Each Accept hands back one half of a net.Pipe pair.
type pipeListener struct {
	addr net.Addr
	ch   chan net.Conn
	done chan struct{}
}

func newPipeListener(ip netip.Addr) *pipeListener {
	return &pipeListener{
		addr: pipeAddr{ip: ip},
		ch:   make(chan net.Conn, 16),
		done: make(chan struct{}),
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.ch:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

func (l *pipeListener) Addr() net.Addr { return l.addr }

// fakeDialer routes a dial to the pipe listener registered for its concrete
// target IP. It records every attempted peer so tests can prove which address
// was (or was not) dialed.
type fakeDialer struct {
	mu        sync.Mutex
	listeners map[netip.Addr]*pipeListener
	dials     []string
	dialCount atomic.Int64
}

func newFakeDialer() *fakeDialer {
	return &fakeDialer{listeners: map[netip.Addr]*pipeListener{}}
}

func (d *fakeDialer) register(ip string) *pipeListener {
	addr := MustAddr(ip)
	listener := newPipeListener(addr)
	d.mu.Lock()
	d.listeners[addr] = listener
	d.mu.Unlock()
	return listener
}

func (d *fakeDialer) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return nil, err
	}
	addr = addr.Unmap()

	d.mu.Lock()
	listener, ok := d.listeners[addr]
	d.dials = append(d.dials, addr.String())
	d.mu.Unlock()
	d.dialCount.Add(1)
	if !ok {
		return nil, &net.OpError{Op: "dial", Net: network, Err: fmt.Errorf("no listener for %s", addr)}
	}

	server, client := net.Pipe()
	serverConn := &pipeConn{Conn: server, local: listener.addr, remote: pipeAddr{ip: addr}}
	clientConn := &pipeConn{Conn: client, local: pipeAddr{ip: netip.MustParseAddr("127.0.0.1")}, remote: listener.addr}
	select {
	case listener.ch <- serverConn:
		return clientConn, nil
	case <-ctx.Done():
		_ = server.Close()
		_ = client.Close()
		return nil, ctx.Err()
	}
}

func (d *fakeDialer) attempted() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.dials))
	copy(out, d.dials)
	return out
}

// scriptResolver returns scripted answers per host name, letting tests
// simulate multi-answer DNS and same-name rebinding between requests.
type scriptResolver struct {
	mu      sync.Mutex
	answers map[string][][]netip.Addr
	calls   []string
}

func newScriptResolver() *scriptResolver {
	return &scriptResolver{answers: map[string][][]netip.Addr{}}
}

func (r *scriptResolver) set(host string, answers ...[]netip.Addr) {
	r.mu.Lock()
	r.answers[host] = answers
	r.mu.Unlock()
}

func (r *scriptResolver) LookupIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, host)
	answers, ok := r.answers[host]
	if !ok || len(answers) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	next := answers[0]
	if len(answers) > 1 {
		r.answers[host] = answers[1:]
	}
	return append([]netip.Addr(nil), next...), nil
}

// countingListener reports how many connections were accepted, which is how
// tests distinguish a reused connection from a fresh dial.
type countingListener struct {
	net.Listener
	accepted atomic.Int64
}

func (l *countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err == nil {
		l.accepted.Add(1)
	}
	return conn, err
}

type testServer struct {
	httpServer *http.Server
}

func (s *testServer) Close() {
	_ = s.httpServer.Close()
}

func serve(t *testing.T, listener net.Listener, handler http.Handler, tlsCert *tls.Certificate) *testServer {
	t.Helper()
	server := &http.Server{Handler: handler}
	if tlsCert != nil {
		server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{*tlsCert}}
		go func() { _ = server.ServeTLS(listener, "", "") }()
		return &testServer{httpServer: server}
	} else {
		go func() { _ = server.Serve(listener) }()
	}
	t.Cleanup(func() { _ = server.Close() })
	return &testServer{httpServer: server}
}

func allowOnly(ips ...string) PrefixPolicy {
	policy := PrefixPolicy{}
	for _, ip := range ips {
		addr := MustAddr(ip)
		bits := 32
		if addr.Is6() {
			bits = 128
		}
		policy.AllowPrefixes = append(policy.AllowPrefixes, netip.PrefixFrom(addr, bits))
	}
	return policy
}

func newGuard(t *testing.T, policy Policy, resolver Resolver, dialer *fakeDialer) *Guard {
	t.Helper()
	guard := New(policy, WithResolver(resolver))
	guard.dialFn = dialer.dialContext
	return guard
}

func get(t *testing.T, client *http.Client, url string) (*http.Response, string) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(body)
}

func expectForbidden(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}
