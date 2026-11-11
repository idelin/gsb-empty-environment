package jwtmw

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// memoryServer serves an http.Handler over in-memory net.Pipe connections.
// It exercises the full net/http client/server stack (request serialization,
// response parsing, context cancellation) without binding a TCP port, which
// the sandbox forbids.
type memoryServer struct {
	URL       string
	handler   http.Handler
	transport *memoryTransport
}

func newMemoryServer(t *testing.T, handler http.Handler) *memoryServer {
	t.Helper()
	s := &memoryServer{
		URL:     "http://jwks.test/.well-known/jwks.json",
		handler: handler,
	}
	s.transport = &memoryTransport{server: s}
	return s
}

func (s *memoryServer) client() *http.Client {
	return &http.Client{
		Timeout:   5 * time.Second,
		Transport: s.transport,
	}
}

type memoryTransport struct {
	server *memoryServer
}

func (t2 *memoryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clientConn, serverConn := net.Pipe()
	deadline := time.Now().Add(10 * time.Second)
	_ = clientConn.SetDeadline(deadline)
	_ = serverConn.SetDeadline(deadline)

	listener := newPipeListener(serverConn)
	go func() {
		_ = http.Serve(listener, t2.server.handler)
	}()

	if err := req.Write(clientConn); err != nil {
		_ = clientConn.Close()
		return nil, err
	}

	reader := bufio.NewReader(clientConn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		_ = clientConn.Close()
		return nil, err
	}
	resp.Body = &pipeBody{body: resp.Body, conn: clientConn}
	return resp, nil
}

type pipeBody struct {
	body   io.ReadCloser
	conn   net.Conn
	closed bool
}

func (b *pipeBody) Read(p []byte) (int, error) {
	return b.body.Read(p)
}

func (b *pipeBody) Close() error {
	if b.closed {
		return nil
	}
	b.closed = true
	err := b.body.Close()
	// Closing the client end makes the server goroutine's connection close,
	// after which its Serve loop returns. We do not block on it here.
	_ = b.conn.Close()
	return err
}

// pipeListener hands out exactly one connection, then blocks later Accept
// calls until Close so the http.Serve loop terminates without busy-looping.
type pipeListener struct {
	conn net.Conn
	done chan struct{}
	once sync.Once
	used bool
	mu   sync.Mutex
}

func newPipeListener(conn net.Conn) *pipeListener {
	return &pipeListener{conn: conn, done: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if !l.used {
		l.used = true
		conn := l.conn
		l.mu.Unlock()
		return conn, nil
	}
	l.mu.Unlock()

	<-l.done
	return nil, net.ErrClosed
}

func (l *pipeListener) Close() error {
	l.once.Do(func() {
		_ = l.conn.Close()
		close(l.done)
	})
	return nil
}

func (l *pipeListener) Addr() net.Addr {
	return l.conn.LocalAddr()
}
