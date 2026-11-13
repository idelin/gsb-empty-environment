package hostresolve

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// addrConn overrides the peer address of an in-memory net.Pipe so the
// resolver's trusted-proxy logic sees the same RemoteAddr it would on a TCP
// listener.
type addrConn struct {
	net.Conn
	remote net.Addr
}

func (c *addrConn) RemoteAddr() net.Addr { return c.remote }

type pipeAddr string

func (a pipeAddr) Network() string { return "tcp" }
func (a pipeAddr) String() string  { return string(a) }

// pipeListener feeds in-memory connections to a standard http.Server so the
// server runs its real HTTP/1.1 connection state machine without binding a
// socket.
type pipeListener struct {
	ch   chan net.Conn
	addr net.Addr
}

func newPipeListener() *pipeListener {
	return &pipeListener{ch: make(chan net.Conn, 1), addr: pipeAddr("127.0.0.1:0")}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	conn, ok := <-l.ch
	if !ok {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (l *pipeListener) Close() error {
	close(l.ch)
	return nil
}

func (l *pipeListener) Addr() net.Addr { return l.addr }

// newWireHandler returns the middleware under test and a channel capturing
// every result that reaches the inner handler.
func newWireHandler(t *testing.T, r *Resolver) (http.Handler, chan *Result) {
	t.Helper()
	results := make(chan *Result, 8)
	handler := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		results <- FromContext(req.Context())
		_, _ = io.WriteString(w, "ok")
	}))
	return handler, results
}

// roundTrip sends one raw HTTP/1.1 request over net.Pipe. The server parses
// the exact request-line and header bytes a TCP entry point would parse,
// including absolute-form targets and duplicate field lines.
func roundTrip(t *testing.T, handler http.Handler, remoteAddr, raw string) *http.Response {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })

	conn := &addrConn{Conn: serverConn, remote: pipeAddr(remoteAddr)}
	listener := newPipeListener()
	server := &http.Server{Handler: handler}
	done := make(chan struct{})
	go func() {
		_ = server.Serve(listener)
		_ = serverConn.Close()
		close(done)
	}()
	listener.ch <- conn

	if _, err := io.WriteString(clientConn, raw); err != nil {
		t.Fatalf("write request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(clientConn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	t.Cleanup(func() {
		_ = resp.Body.Close()
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("server did not finish within timeout")
		}
	})
	return resp
}

func TestWireAbsoluteFormConflictResolvedByRequestTarget(t *testing.T) {
	// No trusted proxies: every peer is treated as an untrusted client, so the
	// injected X-Forwarded-Host must be ignored alongside the Host header.
	handler, results := newWireHandler(t, &Resolver{})

	raw := "GET https://tenant-b.example/objects/1 HTTP/1.1\r\n" +
		"Host: tenant-a.example\r\n" +
		"X-Forwarded-Host: evil.example\r\n" +
		"Connection: close\r\n\r\n"
	resp := roundTrip(t, handler, "203.0.113.7:5100", raw)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	select {
	case res := <-results:
		if res.Source != SourceRequestTarget {
			t.Fatalf("Source = %v, want SourceRequestTarget", res.Source)
		}
		if res.Key != "tenant-b.example" {
			t.Fatalf("Key = %q, want tenant-b.example; Host-header poisoning was not blocked", res.Key)
		}
		if res.RouteKey() != res.CacheKey() {
			t.Fatalf("route key %q != cache key %q", res.RouteKey(), res.CacheKey())
		}
	default:
		t.Fatal("handler never ran")
	}
}

func TestWireDuplicateHostRejected(t *testing.T) {
	handler, results := newWireHandler(t, &Resolver{})

	raw := "GET / HTTP/1.1\r\n" +
		"Host: a.example\r\n" +
		"Host: b.example\r\n" +
		"Connection: close\r\n\r\n"
	resp := roundTrip(t, handler, "203.0.113.7:5100", raw)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	select {
	case res := <-results:
		t.Fatalf("handler must not run for duplicated Host, got result %+v", res)
	default:
	}
}

func TestWireUserinfoAndPathHostRejected(t *testing.T) {
	handler, results := newWireHandler(t, &Resolver{})
	cases := map[string]string{
		"userinfo": "GET / HTTP/1.1\r\nHost: user@a.example\r\nConnection: close\r\n\r\n",
		"path":     "GET / HTTP/1.1\r\nHost: a.example/internal\r\nConnection: close\r\n\r\n",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			resp := roundTrip(t, handler, "203.0.113.7:5100", raw)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			select {
			case res := <-results:
				t.Fatalf("handler must not run, got %+v", res)
			default:
			}
		})
	}
}

func TestWireUntrustedForwardedHostIgnored(t *testing.T) {
	// Loopback peer, but the resolver is configured to trust nobody: the
	// forwarding header must not influence the resolved key.
	handler, results := newWireHandler(t, &Resolver{})

	raw := "GET / HTTP/1.1\r\n" +
		"Host: tenant-a.example\r\n" +
		"X-Forwarded-Host: evil.example\r\n" +
		"Connection: close\r\n\r\n"
	resp := roundTrip(t, handler, "127.0.0.1:5100", raw)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	res := <-results
	if res.Key != "tenant-a.example" || res.Source != SourceHost {
		t.Fatalf("got key=%q source=%v, untrusted XFH was honored", res.Key, res.Source)
	}
}

func TestWireTrustedForwardedHostHonored(t *testing.T) {
	handler, results := newWireHandler(t, &Resolver{IsTrustedProxy: trustLoopback})

	raw := "GET / HTTP/1.1\r\n" +
		"Host: proxy.internal\r\n" +
		"X-Forwarded-Host: tenant-a.example\r\n" +
		"Connection: close\r\n\r\n"
	resp := roundTrip(t, handler, "127.0.0.1:5100", raw)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	res := <-results
	if res.Key != "tenant-a.example" || res.Source != SourceForwardedHost {
		t.Fatalf("got key=%q source=%v, want trusted XFH override", res.Key, res.Source)
	}
}

func TestWireMissingHostRejected(t *testing.T) {
	handler, results := newWireHandler(t, &Resolver{})

	raw := "GET / HTTP/1.1\r\nConnection: close\r\n\r\n"
	resp := roundTrip(t, handler, "203.0.113.7:5100", raw)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	select {
	case res := <-results:
		t.Fatalf("handler must not run without Host, got %+v", res)
	default:
	}
}

func TestSourceStringNames(t *testing.T) {
	if got := SourceForwardedHost.String(); !strings.Contains(got, "X-Forwarded-Host") {
		t.Fatalf("Source.String() = %q", got)
	}
	if got := SourceRequestTarget.String(); got != "request-target" {
		t.Fatalf("Source.String() = %q", got)
	}
}
