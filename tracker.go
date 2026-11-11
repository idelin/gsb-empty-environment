package dialpolicy

import (
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
)

// connTracker keeps the set of live guarded connections so that a hot policy
// swap can forcibly close every connection whose peer is no longer allowed.
type connTracker struct {
	mu   sync.Mutex
	live map[*guardedConn]struct{}
}

func (t *connTracker) wrap(conn net.Conn, guard *Guard, peer netip.Addr) *guardedConn {
	gc := &guardedConn{Conn: conn, guard: guard, tracker: t, peer: peer}
	t.mu.Lock()
	if t.live == nil {
		t.live = make(map[*guardedConn]struct{})
	}
	t.live[gc] = struct{}{}
	t.mu.Unlock()
	return gc
}

func (t *connTracker) release(gc *guardedConn) {
	t.mu.Lock()
	delete(t.live, gc)
	t.mu.Unlock()
}

// dropIf closes every tracked connection whose peer satisfies reject. The
// closing happens under no connection lock, so a concurrently blocked read or
// write is released immediately.
func (t *connTracker) dropIf(reject func(netip.Addr) bool) {
	t.mu.Lock()
	victims := make([]*guardedConn, 0, len(t.live))
	for conn := range t.live {
		if reject(conn.peer) {
			victims = append(victims, conn)
		}
	}
	t.mu.Unlock()
	for _, conn := range victims {
		conn.sever()
	}
}

// guardedConn ties a live connection to the policy that was used to establish
// it. Reads and writes additionally recheck the current policy, while the
// tracker severs blocked peers even when an I/O call is already parked on the
// underlying socket.
type guardedConn struct {
	net.Conn
	guard   *Guard
	tracker *connTracker
	peer    netip.Addr
	closed  atomic.Bool
	severed atomic.Bool
}

func (c *guardedConn) Read(b []byte) (int, error) {
	if c.deny() {
		return 0, c.sever()
	}
	n, err := c.Conn.Read(b)
	if c.deny() {
		return 0, c.sever()
	}
	return n, err
}

func (c *guardedConn) Write(b []byte) (int, error) {
	if c.deny() {
		return 0, c.sever()
	}
	n, err := c.Conn.Write(b)
	if c.deny() {
		return 0, c.sever()
	}
	return n, err
}

func (c *guardedConn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		c.tracker.release(c)
	}
	return c.Conn.Close()
}

// CloseWrite is forwarded when the underlying connection supports it, keeping
// the half-close semantics the HTTP stack relies on.
func (c *guardedConn) CloseWrite() error {
	if c.deny() {
		return c.sever()
	}
	type closeWriter interface{ CloseWrite() error }
	if w, ok := c.Conn.(closeWriter); ok {
		return w.CloseWrite()
	}
	return nil
}

func (c *guardedConn) deny() bool {
	return c.severed.Load() || !c.guard.addrAllowed(c.peer)
}

// sever closes the underlying connection and returns a policy error. It is
// idempotent and safe to invoke from the tracker or from inline I/O checks.
func (c *guardedConn) sever() error {
	if c.severed.CompareAndSwap(false, true) && !c.closed.Load() {
		c.tracker.release(c)
		_ = c.Conn.Close()
	}
	return &net.OpError{
		Op:   "write",
		Net:  c.Conn.LocalAddr().Network(),
		Addr: c.Conn.RemoteAddr(),
		Err:  ErrForbidden,
	}
}
