package netsec

import (
	"errors"
	"sync"
)

// handlePool mimics the kernel-side file descriptor table used by a local
// transport: numeric handles are allocated, freed and later reused for
// unrelated connections. Reuse lets tests prove that identity comes from
// the registry binding, not from a recycled transport handle.
type handlePool struct {
	mu     sync.Mutex
	next   uint64
	free   []uint64
	issued map[uint64]int
	reused int
}

func newHandlePool() *handlePool {
	return &handlePool{next: 1, issued: make(map[uint64]int)}
}

func (p *handlePool) acquire() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n := len(p.free); n > 0 {
		h := p.free[n-1]
		p.free = p.free[:n-1]
		p.issued[h]++
		p.reused++
		return h
	}
	h := p.next
	p.next++
	p.issued[h]++
	return h
}

func (p *handlePool) release(h uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.issued[h]; !ok {
		panic("netsec test: release of unknown handle")
	}
	delete(p.issued, h)
	p.free = append(p.free, h)
}

func (p *handlePool) reuseCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reused
}

// transportConn is one side of a mock transport connection. It records the
// handle it was created with so tests can observe handle recycling.
type transportConn struct {
	handle uint64
	creds  PeerCreds
	pool   *handlePool
	closed bool
	mu     sync.Mutex
}

func (t *transportConn) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	t.pool.release(t.handle)
}

// MockTransport dials endpoint names through a Registry while simulating
// transport handles and asynchronous, potentially late, authentication.
type MockTransport struct {
	registry *Registry
	handles  *handlePool

	mu    sync.Mutex
	dials []*dialRecord
}

type dialRecord struct {
	name      string
	handle    uint64
	transport *transportConn
	conn      *Conn
}

// NewMockTransport builds a mock transport fronting r.
func NewMockTransport(r *Registry) *MockTransport {
	return &MockTransport{registry: r, handles: newHandlePool()}
}

// Dial creates a transport connection from creds to name, performs the
// registry handshake and returns both the mock transport side and the
// registry-bound Conn.
func (m *MockTransport) Dial(name string, creds PeerCreds, auth Authenticator) (*transportConn, *Conn, Challenge, error) {
	handle := m.handles.acquire()
	tc := &transportConn{handle: handle, creds: creds, pool: m.handles}
	conn, challenge, err := m.registry.Connect(name, creds, auth)
	if err != nil {
		tc.close()
		return nil, nil, Challenge{}, err
	}
	m.mu.Lock()
	m.dials = append(m.dials, &dialRecord{name: name, handle: handle, transport: tc, conn: conn})
	m.mu.Unlock()
	return tc, conn, challenge, nil
}

// HangUp closes both the transport side and the registry Conn and returns
// the freed handle to the pool for reuse.
func (m *MockTransport) HangUp(tc *transportConn, conn *Conn) {
	_ = conn.Close()
	tc.close()
}

// ReuseCount reports how many times a previously freed handle was handed
// out again.
func (m *MockTransport) ReuseCount() int { return m.handles.reuseCount() }

// delayedAuth is an Authenticator that parks until the test calls Resolve,
// allowing authentication responses to arrive arbitrarily late: after
// revocation, after a same-name rebuild, or after Close.
type delayedAuth struct {
	mu      sync.Mutex
	creds   PeerCreds
	expect  Challenge
	once    sync.Once
	resolve chan error
}

func newDelayedAuth(creds PeerCreds, challenge Challenge) *delayedAuth {
	return &delayedAuth{creds: creds, expect: challenge, resolve: make(chan error, 1)}
}

// SetExpectedChallenge updates the challenge this authenticator expects.
// It is safe to call concurrently with Authenticate because Dial returns
// the issued challenge only after the handshake goroutine started.
func (d *delayedAuth) SetExpectedChallenge(ch Challenge) {
	d.mu.Lock()
	d.expect = ch
	d.mu.Unlock()
}

// Authenticate implements Authenticator. A credential or challenge
// mismatch rejects immediately; otherwise it blocks for a later Resolve.
func (d *delayedAuth) Authenticate(creds PeerCreds, challenge Challenge, result func(error)) {
	d.mu.Lock()
	wantCreds, wantChallenge := d.creds, d.expect
	d.mu.Unlock()
	if !creds.Equal(wantCreds) || !challenge.Equal(wantChallenge) {
		result(ErrChallengeMismatch)
		return
	}
	go func() { result(<-d.resolve) }()
}

// Resolve fires the parked callback with success. Only the first call
// fires; duplicate late resolutions are swallowed by the buffered channel.
func (d *delayedAuth) Resolve() {
	d.once.Do(func() { d.resolve <- nil })
}

// ResolveWith fires the parked callback with an explicit error, modeling a
// late negative authentication response.
func (d *delayedAuth) ResolveWith(err error) {
	d.once.Do(func() { d.resolve <- err })
}

var errBoom = errors.New("netsec test: simulated auth failure")
