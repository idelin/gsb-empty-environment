package discovery

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// MemNetwork is an in-process transport implementing the same
// Listen/Dial surface as TCP. It lets tests and demos run a full
// cluster inside one process (and one address space) without
// touching real sockets. The zero value is not ready for use; call
// NewMemNetwork.
type MemNetwork struct {
	mu        sync.Mutex
	listeners map[string]*memListener
	seq       int
}

// NewMemNetwork creates an empty in-process network.
func NewMemNetwork() *MemNetwork {
	return &MemNetwork{listeners: make(map[string]*memListener)}
}

// Listen binds a named in-process endpoint. An empty address or one
// ending in ":0" allocates a fresh unique address, mirroring TCP
// ephemeral ports.
func (m *MemNetwork) Listen(addr string) (net.Listener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if addr == "" || strings.HasSuffix(addr, ":0") {
		m.seq++
		addr = fmt.Sprintf("mem-%d.local", m.seq)
	}
	if _, ok := m.listeners[addr]; ok {
		return nil, fmt.Errorf("listen %s: address already in use", addr)
	}
	l := &memListener{
		net:   m,
		addr:  addr,
		conns: make(chan net.Conn, 1024),
		done:  make(chan struct{}),
	}
	m.listeners[addr] = l
	return l, nil
}

// Dial connects to a previously bound in-process endpoint.
func (m *MemNetwork) Dial(addr string, timeout time.Duration) (net.Conn, error) {
	m.mu.Lock()
	l, ok := m.listeners[addr]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("dial %s: connection refused", addr)
	}
	server, client := net.Pipe()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.done:
		server.Close()
		client.Close()
		return nil, fmt.Errorf("dial %s: connection refused", addr)
	case <-timer.C:
		server.Close()
		client.Close()
		return nil, fmt.Errorf("dial %s: i/o timeout", addr)
	}
}

type memAddr string

func (a memAddr) Network() string { return "mem" }
func (a memAddr) String() string  { return string(a) }

type memListener struct {
	net   *MemNetwork
	addr  string
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, fmt.Errorf("accept %s: listener closed", l.addr)
	}
}

func (l *memListener) Close() error {
	l.once.Do(func() {
		close(l.done)
		l.net.mu.Lock()
		delete(l.net.listeners, l.addr)
		l.net.mu.Unlock()
	})
	return nil
}

func (l *memListener) Addr() net.Addr { return memAddr(l.addr) }
