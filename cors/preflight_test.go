package cors

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testServer is a real HTTP/1.1 server (http.Server) whose transport runs over
// in-process net.Pipe connections instead of a kernel port, so it works in
// sandboxes that deny socket binding. It counts preflight (OPTIONS) and
// business requests separately and answers with configurable CORS rules.
type testServer struct {
	URL string

	handler http.Handler
	server  *http.Server
	listen  *pipeListener
	dialer  func(ctx context.Context, network, addr string) (net.Conn, error)

	preflights atomic.Int64
	requests   atomic.Int64

	mu               sync.Mutex
	echoOrigin       bool
	wildcard         bool
	allowCredentials bool
	allowMethods     []string
	allowHeaders     []string
	maxAge           string
	failPreflight    bool
	redirectTo       string
}

func newTestServer() *testServer {
	s := &testServer{URL: "http://cors-target.test"}
	s.handler = http.HandlerFunc(s.handle)
	s.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.handler.ServeHTTP(w, r)
	})}
	s.listen = newPipeListener()
	go func() { _ = s.server.Serve(s.listen) }()

	s.dialer = func(ctx context.Context, network, addr string) (net.Conn, error) {
		clientConn, serverConn := net.Pipe()
		s.listen.deliver(serverConn)
		return clientConn, nil
	}
	return s
}

// transport returns an http.Transport whose connections are the in-process
// pipes owned by this server.
func (s *testServer) transport() *http.Transport {
	return &http.Transport{
		DialContext:           s.dialer,
		DisableKeepAlives:     false,
		MaxIdleConns:          10,
		IdleConnTimeout:       2 * time.Second,
		TLSHandshakeTimeout:   time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

func (s *testServer) Close() {
	_ = s.server.Close()
	_ = s.listen.Close()
}

// pipeListener feeds in-process net.Pipe server ends into http.Server.Serve,
// emulating a listening socket without touching the kernel network stack.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
}

func newPipeListener() *pipeListener {
	return &pipeListener{
		conns:  make(chan net.Conn, 64),
		closed: make(chan struct{}),
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

func (l *pipeListener) deliver(conn net.Conn) {
	select {
	case l.conns <- conn:
	case <-l.closed:
		_ = conn.Close()
	}
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "in-process" }

func (s *testServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	echoOrigin := s.echoOrigin
	wildcard := s.wildcard
	allowCredentials := s.allowCredentials
	allowMethods := append([]string(nil), s.allowMethods...)
	allowHeaders := append([]string(nil), s.allowHeaders...)
	maxAge := s.maxAge
	failPreflight := s.failPreflight
	redirectTo := s.redirectTo
	s.mu.Unlock()

	origin := r.Header.Get("Origin")

	if r.Method == http.MethodOptions &&
		r.Header.Get("Access-Control-Request-Method") != "" {
		s.preflights.Add(1)
		if failPreflight {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch {
		case echoOrigin && origin != "":
			w.Header().Set("Access-Control-Allow-Origin", origin)
		case wildcard:
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		if allowCredentials {
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if len(allowMethods) > 0 {
			w.Header().Set("Access-Control-Allow-Methods", strings.Join(allowMethods, ", "))
		}
		if len(allowHeaders) > 0 {
			w.Header().Set("Access-Control-Allow-Headers", strings.Join(allowHeaders, ", "))
		}
		if maxAge != "" {
			w.Header().Set("Access-Control-Max-Age", maxAge)
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	s.requests.Add(1)
	if redirectTo != "" {
		http.Redirect(w, r, redirectTo, http.StatusTemporaryRedirect)
		return
	}
	if echoOrigin && origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "ok:%s", r.URL.Path)
}

func (s *testServer) configure(fn func(c *testServerConfig)) {
	cfg := &testServerConfig{s: s}
	fn(cfg)
}

type testServerConfig struct{ s *testServer }

func (c *testServerConfig) echo() *testServerConfig     { c.s.echoOrigin = true; return c }
func (c *testServerConfig) wildcard() *testServerConfig { c.s.wildcard = true; return c }
func (c *testServerConfig) credentials() *testServerConfig {
	c.s.allowCredentials = true
	return c
}
func (c *testServerConfig) methods(m ...string) *testServerConfig {
	c.s.allowMethods = m
	return c
}
func (c *testServerConfig) headers(h ...string) *testServerConfig {
	c.s.allowHeaders = h
	return c
}
func (c *testServerConfig) maxAgeSeconds(v string) *testServerConfig {
	c.s.maxAge = v
	return c
}
func (c *testServerConfig) failPreflight() *testServerConfig {
	c.s.failPreflight = true
	return c
}
func (c *testServerConfig) redirectTo(target string) *testServerConfig {
	c.s.redirectTo = target
	return c
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// policyTransport injects the browsing-context policy into every request made
// by an http.Client, leaving redirect handling to the client.
type policyTransport struct {
	base   http.RoundTripper
	policy Policy
}

func (p *policyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := WithPolicy(req.Context(), p.policy)
	return p.base.RoundTrip(req.WithContext(ctx))
}

// newClient builds an http.Client whose underlying connections reach the
// supplied servers through their in-process dialers.
func newClient(policy Policy, base http.RoundTripper) *http.Client {
	if base == nil {
		base = http.DefaultTransport
	}
	return &http.Client{Transport: &policyTransport{base: base, policy: policy}}
}

func doRequest(t *testing.T, client *http.Client, server *testServer, method string, headers map[string]string) (string, error) {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+"/resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body), nil
}

func expectCounts(t *testing.T, server *testServer, wantPreflights, wantRequests int64) {
	t.Helper()
	if got := server.preflights.Load(); got != wantPreflights {
		t.Fatalf("preflight count = %d, want %d", got, wantPreflights)
	}
	if got := server.requests.Load(); got != wantRequests {
		t.Fatalf("business request count = %d, want %d", got, wantRequests)
	}
}

func expectPreflightError(t *testing.T, err error) {
	t.Helper()
	var perr *PreflightError
	if !errors.As(err, &perr) {
		t.Fatalf("expected *PreflightError, got %v", err)
	}
}

// multiDialer routes connections to one of several in-process servers based on
// the request host, supporting cross-origin redirect tests without sockets.
type multiDialer struct {
	hosts map[string]*testServer
}

func (m *multiDialer) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host := addr
	if idx := strings.LastIndex(addr, ":"); idx >= 0 {
		host = addr[:idx]
	}
	server, ok := m.hosts[host]
	if !ok {
		return nil, fmt.Errorf("no test server registered for host %q", host)
	}
	return server.dialer(ctx, network, addr)
}

func multiServerTransport(servers ...*testServer) *http.Transport {
	hosts := make(map[string]*testServer, len(servers))
	for _, server := range servers {
		hosts[mustHost(server.URL)] = server
	}
	dialer := (&multiDialer{hosts: hosts}).dial
	return &http.Transport{
		DialContext:           dialer,
		DisableKeepAlives:     false,
		MaxIdleConns:          10,
		IdleConnTimeout:       2 * time.Second,
		TLSHandshakeTimeout:   time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

func mustHost(rawURL string) string {
	start := strings.Index(rawURL, "://") + 3
	rest := rawURL[start:]
	if idx := strings.Index(rest, "/"); idx >= 0 {
		rest = rest[:idx]
	}
	return rest
}
