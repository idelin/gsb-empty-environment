package netsec

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync"
)

// PausableConnector is an in-memory Connector for deterministic security tests.
//
// It replaces real sockets with net.Pipe pairs so tests can:
//   - pause outbound dials and inspect or block the chosen target
//     (active-mode target replacement),
//   - inject inbound connections with an explicit peer identity
//     (passive-mode port preemption and late data connections),
//   - pause Accept so connections stay queued in the listener backlog.
type PausableConnector struct {
	mu        sync.Mutex
	listeners map[netip.AddrPort]*PausableListener
	nextPort  uint16
	nextEphem uint16

	dialLog      []netip.AddrPort
	pendingDials int
	lastClient   netip.AddrPort

	paused bool
	gate   chan struct{}
}

// NewPausableConnector creates an empty test connector.
func NewPausableConnector() *PausableConnector {
	return &PausableConnector{
		listeners: map[netip.AddrPort]*PausableListener{},
		nextPort:  30000,
		nextEphem: 40000,
	}
}

// PauseDials blocks subsequent Dial calls until ResumeDials.
func (c *PausableConnector) PauseDials() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.paused {
		c.paused = true
		c.gate = make(chan struct{})
	}
}

// ResumeDials releases all dials blocked by PauseDials.
func (c *PausableConnector) ResumeDials() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.paused {
		c.paused = false
		close(c.gate)
		c.gate = nil
	}
}

// PendingDials reports how many dials are blocked behind the pause gate.
func (c *PausableConnector) PendingDials() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pendingDials
}

// Dialed returns the ordered targets handed to Dial.
func (c *PausableConnector) Dialed() []netip.AddrPort {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]netip.AddrPort, len(c.dialLog))
	copy(out, c.dialLog)
	return out
}

// LastClientAddr reports the ephemeral source identity of the last Dial.
func (c *PausableConnector) LastClientAddr() (netip.AddrPort, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastClient, c.lastClient.IsValid()
}

// Listener returns the test listener bound at addr, if any.
func (c *PausableConnector) Listener(addr netip.AddrPort) (*PausableListener, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ln, ok := c.listeners[addr]
	return ln, ok
}

// Listen registers an in-memory listener, allocating an endpoint when addr is zero.
func (c *PausableConnector) Listen(_ context.Context, addr netip.AddrPort) (DataListener, netip.AddrPort, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if addr == (netip.AddrPort{}) || addr.Port() == 0 {
		ip := addr.Addr()
		if !ip.IsValid() {
			ip = netip.MustParseAddr("127.0.0.1")
		}
		for {
			addr = netip.AddrPortFrom(ip, c.nextPort)
			c.nextPort++
			if _, taken := c.listeners[addr]; !taken {
				break
			}
		}
	}
	if _, taken := c.listeners[addr]; taken {
		return nil, netip.AddrPort{}, fmt.Errorf("netsec: address already in use: %s", addr)
	}

	listener := newPausableListener(addr)
	c.listeners[addr] = listener
	return listener, addr, nil
}

// Dial opens an in-memory connection. While dials are paused it blocks without
// touching any listener, so a swapped target is evaluated only after resume.
func (c *PausableConnector) Dial(ctx context.Context, addr netip.AddrPort) (net.Conn, error) {
	c.mu.Lock()
	gate := c.gate
	c.dialLog = append(c.dialLog, addr)
	if gate != nil {
		c.pendingDials++
	}
	c.mu.Unlock()

	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			c.mu.Lock()
			c.pendingDials--
			c.mu.Unlock()
			return nil, ctx.Err()
		}
		c.mu.Lock()
		c.pendingDials--
		c.mu.Unlock()
	}

	c.mu.Lock()
	listener, ok := c.listeners[addr]
	if !ok {
		c.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrConnectionRefused, addr)
	}
	from := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), c.nextEphem)
	c.nextEphem++
	c.lastClient = from
	c.mu.Unlock()

	client, server := newPipePair(from, addr, addr, from)
	listener.enqueue(server)
	return client, nil
}

// PausableListener is an in-memory DataListener with a controllable backlog.
type PausableListener struct {
	addr netip.AddrPort

	mu           sync.Mutex
	cond         *sync.Cond
	queue        []net.Conn
	acceptPaused bool
	closed       bool
	accepted     int
	rejected     int
}

func newPausableListener(addr netip.AddrPort) *PausableListener {
	l := &PausableListener{addr: addr}
	l.cond = sync.NewCond(&l.mu)
	return l
}

// LocalAddrPort returns the bound endpoint.
func (l *PausableListener) LocalAddrPort() netip.AddrPort { return l.addr }

// Addr returns the bound endpoint as a net.Addr.
func (l *PausableListener) Addr() net.Addr { return pipeAddr(l.addr) }

// PauseAccept holds queued connections back from Accept.
func (l *PausableListener) PauseAccept() {
	l.mu.Lock()
	l.acceptPaused = true
	l.mu.Unlock()
}

// ResumeAccept releases Accept and drains the backlog in arrival order.
func (l *PausableListener) ResumeAccept() {
	l.mu.Lock()
	l.acceptPaused = false
	l.cond.Broadcast()
	l.mu.Unlock()
}

// Accepted reports how many connections Accept has handed out.
func (l *PausableListener) Accepted() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.accepted
}

// Rejected reports queued connections discarded at close without Accept.
func (l *PausableListener) Rejected() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rejected
}

// Inject presents an inbound data connection whose peer identity is remote,
// returning the client side held by the test. On a closed listener the server
// side is shut immediately, so the injected peer observes a zero body.
func (l *PausableListener) Inject(remote netip.AddrPort) net.Conn {
	client, server := newPipePair(remote, l.addr, l.addr, remote)
	l.enqueue(server)
	return client
}

func (l *PausableListener) enqueue(conn net.Conn) {
	l.mu.Lock()
	if l.closed {
		l.rejected++
		l.mu.Unlock()
		_ = conn.Close()
		return
	}
	l.queue = append(l.queue, conn)
	l.cond.Broadcast()
	l.mu.Unlock()
}

// Accept returns the next queued connection in arrival order.
func (l *PausableListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for {
		if l.closed {
			return nil, net.ErrClosed
		}
		if !l.acceptPaused && len(l.queue) > 0 {
			conn := l.queue[0]
			l.queue = l.queue[1:]
			l.accepted++
			return conn, nil
		}
		l.cond.Wait()
	}
}

// Close stops Accept and discards every connection still in the backlog.
func (l *PausableListener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return net.ErrClosed
	}
	l.closed = true
	pending := l.queue
	l.queue = nil
	l.rejected += len(pending)
	l.cond.Broadcast()
	l.mu.Unlock()

	for _, conn := range pending {
		_ = conn.Close()
	}
	return nil
}

type pipeAddr netip.AddrPort

func (a pipeAddr) Network() string { return "tcp" }
func (a pipeAddr) String() string  { return netip.AddrPort(a).String() }

type pipeConn struct {
	net.Conn
	local  net.Addr
	remote net.Addr
}

func (c pipeConn) LocalAddr() net.Addr  { return c.local }
func (c pipeConn) RemoteAddr() net.Addr { return c.remote }

func newPipePair(localA, remoteA, localB, remoteB netip.AddrPort) (net.Conn, net.Conn) {
	a, b := net.Pipe()
	return pipeConn{Conn: a, local: pipeAddr(localA), remote: pipeAddr(remoteA)},
		pipeConn{Conn: b, local: pipeAddr(localB), remote: pipeAddr(remoteB)}
}
