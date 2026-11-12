package redirect

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
)

// pipeListener is an in-memory net.Listener fed by net.Pipe connections, so
// tests can run a full HTTP/1.1 server without binding any socket.
type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, errors.New("pipeListener: closed")
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }

func (l *pipeListener) Addr() net.Addr { return pipeAddr("pipe") }

// localNet maps fake host names to in-process HTTP servers and exposes a
// Transport that dials them over net.Pipe.
type localNet struct {
	Transport *http.Transport

	mu        sync.Mutex
	listeners map[string]*pipeListener
}

func newLocalNet() *localNet {
	n := &localNet{listeners: make(map[string]*pipeListener)}
	n.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			n.mu.Lock()
			l, ok := n.listeners[host]
			n.mu.Unlock()
			if !ok {
				return nil, fmt.Errorf("localNet: no such host %q", host)
			}
			client, server := net.Pipe()
			select {
			case l.conns <- server:
				return client, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-l.done:
				return nil, errors.New("localNet: server closed")
			}
		},
	}
	return n
}

// Serve starts an HTTP server for host and returns its base URL.
func (n *localNet) Serve(t *testing.T, host string, h http.Handler) string {
	t.Helper()
	l := newPipeListener()
	n.mu.Lock()
	n.listeners[host] = l
	n.mu.Unlock()
	srv := &http.Server{Handler: h}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return "http://" + host
}
