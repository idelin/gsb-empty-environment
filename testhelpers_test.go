package taskpkg

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// fakeResolver answers LookupIP from a scripted slice and records the hosts it
// was asked about. Tests mutate Answers between retries to model a rebinding
// attacker.
type fakeResolver struct {
	mu      sync.Mutex
	answers []net.IP
	err     error
	hosts   []string
}

func (r *fakeResolver) LookupIP(_ context.Context, _, host string) ([]net.IP, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hosts = append(r.hosts, host)
	if r.err != nil {
		return nil, r.err
	}
	out := make([]net.IP, len(r.answers))
	copy(out, r.answers)
	return out, nil
}

func (r *fakeResolver) setAnswers(ips ...net.IP) {
	r.mu.Lock()
	r.answers = ips
	r.mu.Unlock()
}

func (r *fakeResolver) queriedHosts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.hosts...)
}

// fakeConn is an in-memory net.Conn whose RemoteAddr is fully controlled by
// the test, including peers that differ from the dialed target.
type fakeConn struct {
	remote *net.TCPAddr
	local  *net.TCPAddr

	mu     sync.Mutex
	closed bool
	read   chan struct{}
}

func newFakeConn(ip string, port int) *fakeConn {
	return &fakeConn{
		remote: &net.TCPAddr{IP: net.ParseIP(ip), Port: port},
		local:  &net.TCPAddr{IP: net.ParseIP("198.51.100.9"), Port: 50000},
		read:   make(chan struct{}),
	}
}

func (c *fakeConn) Read(p []byte) (int, error) {
	<-c.read
	return 0, errors.New("fakeConn: closed")
}

func (c *fakeConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, errors.New("fakeConn: closed")
	}
	return len(p), nil
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	close(c.read)
	return nil
}

func (c *fakeConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *fakeConn) LocalAddr() net.Addr  { return c.local }
func (c *fakeConn) RemoteAddr() net.Addr { return c.remote }

func (c *fakeConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error { return nil }

// dialCall records the exact target string handed to the dialer. The test
// asserts these are always IP literals, never host names.
type dialCall struct {
	network string
	address string
}

// fakeDialer returns scripted connections keyed by dial target. A per-target
// hook can block until the test releases the dial, which is how the
// double-success and revocation races are constructed deterministically.
type fakeDialer struct {
	mu       sync.Mutex
	calls    []dialCall
	conns    map[string]*fakeConn
	errors   map[string]error
	entering map[string]chan struct{}
	release  map[string]chan struct{}
}

func newFakeDialer() *fakeDialer {
	return &fakeDialer{
		conns:    make(map[string]*fakeConn),
		errors:   make(map[string]error),
		entering: make(map[string]chan struct{}),
		release:  make(map[string]chan struct{}),
	}
}

func (d *fakeDialer) setConn(target string, conn *fakeConn) {
	d.mu.Lock()
	d.conns[target] = conn
	d.mu.Unlock()
}

func (d *fakeDialer) setError(target string, err error) {
	d.mu.Lock()
	d.errors[target] = err
	d.mu.Unlock()
}

// gate makes DialContext for target signal entering and then block until
// release is closed.
func (d *fakeDialer) gate(target string) (entering, release chan struct{}) {
	entering = make(chan struct{})
	release = make(chan struct{})
	d.mu.Lock()
	d.entering[target] = entering
	d.release[target] = release
	d.mu.Unlock()
	return entering, release
}

func (d *fakeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.calls = append(d.calls, dialCall{network: network, address: address})
	entering := d.entering[address]
	release := d.release[address]
	conn := d.conns[address]
	dialErr := d.errors[address]
	d.mu.Unlock()

	if entering != nil {
		close(entering)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if dialErr != nil {
		return nil, dialErr
	}
	if conn != nil {
		return conn, nil
	}
	return nil, &net.OpError{Op: "dial", Net: network, Addr: addrFor(address), Err: errors.New("no scripted connection")}
}

func (d *fakeDialer) dialCalls() []dialCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]dialCall(nil), d.calls...)
}

func (d *fakeDialer) connFor(target string) *fakeConn {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conns[target]
}

func addrFor(address string) net.Addr {
	if host, port, err := net.SplitHostPort(address); err == nil {
		if p, err := net.LookupPort("tcp", port); err == nil {
			return &net.TCPAddr{IP: net.ParseIP(host), Port: p}
		}
	}
	return &net.TCPAddr{}
}

// delivered records every OnConnect callback for assertion.
type delivered struct {
	mu        sync.Mutex
	conns     []net.Conn
	peerAddrs []net.Addr
}

func (d *delivered) onConnect(conn net.Conn, peer net.Addr) {
	d.mu.Lock()
	d.conns = append(d.conns, conn)
	d.peerAddrs = append(d.peerAddrs, peer)
	d.mu.Unlock()
}

func (d *delivered) peers() []net.Addr {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]net.Addr(nil), d.peerAddrs...)
}

func (d *delivered) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.conns)
}
