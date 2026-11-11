package pathgate

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// pipeListener is an in-memory net.Listener backed by net.Pipe, so the
// whole request path runs through real standard-library HTTP parsing
// without opening any sockets.
type pipeListener struct {
	conns chan net.Conn
}

func newPipeListener() *pipeListener { return &pipeListener{conns: make(chan net.Conn)} }

func (l *pipeListener) dial() (net.Conn, error) {
	client, server := net.Pipe()
	l.conns <- server
	return client, nil
}

func (l *pipeListener) Accept() (net.Conn, error) {
	conn, ok := <-l.conns
	if !ok {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (l *pipeListener) Close() error {
	close(l.conns)
	return nil
}

type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }

func (l *pipeListener) Addr() net.Addr { return pipeAddr("pipe") }

// recordingBackend is a standard-library http.Server that logs every
// RequestURI it receives, exactly as parsed from the wire.
type recordingBackend struct {
	server *http.Server
	ln     *pipeListener
	mu     sync.Mutex
	uris   []string
}

func newRecordingBackend(t *testing.T) *recordingBackend {
	t.Helper()
	b := &recordingBackend{ln: newPipeListener()}
	b.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.uris = append(b.uris, r.RequestURI)
		b.mu.Unlock()
		fmt.Fprintln(w, "ok")
	})}
	go b.server.Serve(b.ln)
	t.Cleanup(func() { b.server.Close() })
	return b
}

// transport dials the backend over an in-memory pipe.
func (b *recordingBackend) transport() *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return b.ln.dial()
		},
	}
}

func (b *recordingBackend) received() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.uris...)
}

func (b *recordingBackend) lastURI(t *testing.T) string {
	t.Helper()
	uris := b.received()
	if len(uris) == 0 {
		t.Fatal("backend received no request")
	}
	return uris[len(uris)-1]
}

func newGateway(backend *recordingBackend, prefixes ...string) *Gateway {
	gw := &Gateway{Transport: backend.transport()}
	target := &url.URL{Scheme: "http", Host: "backend"}
	for _, p := range prefixes {
		gw.Routes = append(gw.Routes, Route{Prefix: p, Target: target})
	}
	return gw
}

// serve feeds a raw request target through the gateway exactly as a real
// server would parse it off the wire.
func serve(t *testing.T, gw *Gateway, target string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)
	resp := rec.Result()
	resp.Body.Close()
	return resp
}

// An escaped question mark must stay path data end to end: the gateway
// matches the decoded form and the upstream still sees "%3F", not a query
// delimiter.
func TestEscapedQuestionMark(t *testing.T) {
	backend := newRecordingBackend(t)
	gw := newGateway(backend, "/search?q=go")

	resp := serve(t, gw, "/search%3Fq=go?x=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got, want := backend.lastURI(t), "/search%3Fq=go?x=1"; got != want {
		t.Errorf("backend RequestURI = %q, want %q", got, want)
	}
}

// Invalid UTF-8 bytes are forwarded byte-for-byte and never rejected or
// mangled into the replacement character.
func TestInvalidUTF8Passthrough(t *testing.T) {
	backend := newRecordingBackend(t)
	gw := newGateway(backend, "/files")

	resp := serve(t, gw, "/files/%C3%28")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got, want := backend.lastURI(t), "/files/%C3%28"; got != want {
		t.Errorf("backend RequestURI = %q, want %q", got, want)
	}
}

// Duplicate query parameters pass through untouched, in order.
func TestDuplicateQueryParams(t *testing.T) {
	backend := newRecordingBackend(t)
	gw := newGateway(backend, "/api")

	resp := serve(t, gw, "/api?tag=a&tag=b&tag=a")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got, want := backend.lastURI(t), "/api?tag=a&tag=b&tag=a"; got != want {
		t.Errorf("backend RequestURI = %q, want %q", got, want)
	}
}

// An encoded slash is one segment at the gateway and one segment upstream:
// both sides agree on "/a/b" as data, forwarded as "%2F".
func TestEncodedSlashStaysInSegment(t *testing.T) {
	backend := newRecordingBackend(t)
	gw := newGateway(backend, "/a/b")

	resp := serve(t, gw, "/a%2Fb")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got, want := backend.lastURI(t), "/a%2Fb"; got != want {
		t.Errorf("backend RequestURI = %q, want %q", got, want)
	}
}

// A separator that only appears after a second decode is rejected before
// any route lookup and never reaches the backend.
func TestDoubleEncodedSeparatorRejected(t *testing.T) {
	backend := newRecordingBackend(t)
	gw := newGateway(backend, "/")

	resp := serve(t, gw, "/a%252Fb")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if got := backend.received(); len(got) != 0 {
		t.Errorf("backend received %v, want no requests", got)
	}
}

// Route prefixes match on segment boundaries only.
func TestPrefixMatchSegmentBoundary(t *testing.T) {
	backend := newRecordingBackend(t)
	gw := newGateway(backend, "/api")

	if resp := serve(t, gw, "/api/users"); resp.StatusCode != http.StatusOK {
		t.Errorf("/api/users: status = %d, want 200", resp.StatusCode)
	}
	if resp := serve(t, gw, "/apix"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("/apix: status = %d, want 404", resp.StatusCode)
	}
}
