package netsec

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
)

// memListener is an in-process net.Listener backed by net.Pipe. It lets the
// HTTP tests run without binding any real socket, which is blocked in the
// sandbox.
type memListener struct {
	mu     sync.Mutex
	conns  chan net.Conn
	closed bool
	addr   memAddr
}

type memAddr struct{}

func (memAddr) Network() string { return "mem" }
func (memAddr) String() string  { return "mem-listener:80" }

func newMemListener() *memListener {
	return &memListener{conns: make(chan net.Conn, 16), addr: memAddr{}}
}

func (l *memListener) Accept() (net.Conn, error) {
	conn, ok := <-l.conns
	if !ok {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (l *memListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.closed = true
		close(l.conns)
	}
	return nil
}

func (l *memListener) Addr() net.Addr { return l.addr }

// dial creates one end of a pipe for the client and hands the other end to
// the HTTP server as an accepted connection.
func (l *memListener) dial() (net.Conn, error) {
	client, server := net.Pipe()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		client.Close()
		server.Close()
		return nil, net.ErrClosed
	}
	l.conns <- server
	return client, nil
}

// newMemoryHTTPServer runs handler behind an in-memory listener and returns
// the listener plus a port-like number used when composing test URLs. No
// real network socket is opened.
func newMemoryHTTPServer(handler http.Handler) (*memListener, *httptest.Server) {
	listener := newMemListener()
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	return listener, server
}
