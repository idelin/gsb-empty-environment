package netsec

import (
	"errors"
	"net"
	"sync"
)

// ReservedCIDMin and ReservedCIDMax bound the well-known vsock CIDs that
// never identify a guest VM:
//
//   - 0: VMADDR_CID_ANY (wildcard)
//   - 1: VMADDR_CID_HYPERVISOR
//   - 2: VMADDR_CID_LOCAL / VMADDR_CID_HOST
const (
	ReservedCIDMin uint32 = 0
	ReservedCIDMax uint32 = 2
)

var (
	// ErrReservedCID is returned when a binding or registration uses a
	// reserved source CID (0..2).
	ErrReservedCID = errors.New("netsec: reserved source CID is not a guest")
	// ErrUnknownCID is returned when no live instance owns the source CID,
	// either because it was never registered or because its generation was
	// revoked.
	ErrUnknownCID = errors.New("netsec: no live instance for source CID")
	// ErrPortNotAllowed is returned when the requested destination port is
	// not in the instance's allowlist.
	ErrPortNotAllowed = errors.New("netsec: destination port not allowed for instance")
	// ErrStaleGeneration is returned when a callback carries a token for a
	// generation that has already been superseded or revoked.
	ErrStaleGeneration = errors.New("netsec: callback references a stale generation")
)

// generation models a single lease of a CID to one VM instance. Every
// Register call starts a fresh generation, which lets late Accept callbacks
// be distinguished from callbacks belonging to the current lease even when
// the CID itself is reused.
type generation struct {
	instanceID string
	gen        uint64
	allowed    map[uint32]struct{}

	mu    sync.Mutex
	dead  bool
	conns map[*BoundConn]struct{}
}

func newGeneration(instanceID string, gen uint64, allowed map[uint32]struct{}) *generation {
	return &generation{
		instanceID: instanceID,
		gen:        gen,
		allowed:    allowed,
		conns:      make(map[*BoundConn]struct{}),
	}
}

func (g *generation) allows(port uint32) bool {
	_, ok := g.allowed[port]
	return ok
}

// kill marks the generation dead and returns a snapshot of every connection
// that still belongs to it. The caller closes the snapshot outside the lock so
// that peer goroutines blocked in Close cannot deadlock the registry.
func (g *generation) killLocked() []*BoundConn {
	g.mu.Lock()
	if g.dead {
		g.mu.Unlock()
		return nil
	}
	g.dead = true
	conns := make([]*BoundConn, 0, len(g.conns))
	for conn := range g.conns {
		conns = append(conns, conn)
	}
	g.conns = nil
	g.mu.Unlock()
	return conns
}

func (g *generation) track(conn *BoundConn) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.dead {
		return false
	}
	g.conns[conn] = struct{}{}
	return true
}

func (g *generation) untrack(conn *BoundConn) {
	g.mu.Lock()
	if g.conns != nil {
		delete(g.conns, conn)
	}
	g.mu.Unlock()
}

// Registry maps each source vsock CID to the live generation of the VM
// instance currently leasing it. A zero-value Registry is ready to use and is
// safe for concurrent access.
type Registry struct {
	mu      sync.RWMutex
	entries map[uint32]*generation
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[uint32]*generation)}
}

// IsReservedCID reports whether cid is a well-known vsock CID that must never
// be treated as a guest identity.
func IsReservedCID(cid uint32) bool {
	return cid >= ReservedCIDMin && cid <= ReservedCIDMax
}

// Register leases cid to an instance identified by instanceID and starts a new
// generation for it. If cid is already leased, the previous generation (for
// example the attacker's destroyed VM) is invalidated and all of its
// connections are closed before the new lease takes effect. allowedPorts is
// copied; callers may mutate their slice afterwards.
func (r *Registry) Register(cid uint32, instanceID string, allowedPorts []uint32) (Token, error) {
	if IsReservedCID(cid) {
		return Token{}, ErrReservedCID
	}

	allowed := make(map[uint32]struct{}, len(allowedPorts))
	for _, port := range allowedPorts {
		allowed[port] = struct{}{}
	}

	r.mu.Lock()
	old := r.entries[cid]
	genNum := uint64(1)
	if old != nil {
		genNum = old.gen + 1
	}
	current := newGeneration(instanceID, genNum, allowed)
	r.entries[cid] = current
	r.mu.Unlock()

	if old != nil {
		closeAll(old.killLocked())
	}
	return Token{cid: cid, gen: genNum}, nil
}

// Revoke invalidates the generation currently leasing cid and closes every
// connection belonging to it. Later Bind calls for the cid fail with
// ErrUnknownCID until Register re-leases it. Revoking an unknown cid has no
// effect.
func (r *Registry) Revoke(cid uint32) {
	r.mu.Lock()
	current := r.entries[cid]
	delete(r.entries, cid)
	r.mu.Unlock()

	if current != nil {
		closeAll(current.killLocked())
	}
}

// Bind is the entry point for an Accept callback: it authenticates the
// incoming connection's source CID and destination port against the currently
// live generation. On success the returned BoundConn is tracked so that a
// subsequent Register (CID reassignment) or Revoke (instance destruction)
// closes it. On every rejection the underlying conn is closed so the peer is
// unblocked and no accepted socket leaks.
func (r *Registry) Bind(cid, port uint32, conn net.Conn) (*BoundConn, error) {
	if IsReservedCID(cid) {
		conn.Close()
		return nil, ErrReservedCID
	}

	r.mu.RLock()
	current := r.entries[cid]
	r.mu.RUnlock()
	if current == nil {
		conn.Close()
		return nil, ErrUnknownCID
	}
	if !current.allows(port) {
		conn.Close()
		return nil, ErrPortNotAllowed
	}

	bound := &BoundConn{Conn: conn, generation: current, cid: cid, port: port}
	if !current.track(bound) {
		// The generation was revoked between the lookup and registration.
		conn.Close()
		return nil, ErrUnknownCID
	}
	return bound, nil
}

// BindToken validates a late Accept callback that captured the Token issued at
// registration time. It rejects callbacks whose generation has since been
// revoked or superseded by CID reuse, even if some instance still leases the
// CID. On rejection conn is closed.
func (r *Registry) BindToken(token Token, port uint32, conn net.Conn) (*BoundConn, error) {
	if IsReservedCID(token.cid) {
		conn.Close()
		return nil, ErrReservedCID
	}

	r.mu.RLock()
	current := r.entries[token.cid]
	r.mu.RUnlock()
	if current == nil || current.gen != token.gen {
		conn.Close()
		return nil, ErrStaleGeneration
	}
	if !current.allows(port) {
		conn.Close()
		return nil, ErrPortNotAllowed
	}

	bound := &BoundConn{Conn: conn, generation: current, cid: token.cid, port: port}
	if !current.track(bound) {
		conn.Close()
		return nil, ErrStaleGeneration
	}
	return bound, nil
}

// ActiveConnections returns the number of tracked connections for cid. It is
// primarily intended for diagnostics and tests.
func (r *Registry) ActiveConnections(cid uint32) int {
	r.mu.RLock()
	current := r.entries[cid]
	r.mu.RUnlock()
	if current == nil {
		return 0
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	return len(current.conns)
}

// Token is an opaque generation handle handed out at Register time. An Accept
// callback that runs asynchronously can present it to BindToken so that
// callbacks from a destroyed generation are discarded after the CID is
// reassigned to a new tenant.
type Token struct {
	cid uint32
	gen uint64
}

// Generation returns the monotonically increasing generation number carried by
// the token.
func (t Token) Generation() uint64 { return t.gen }

// BoundConn wraps a net.Conn that has been authenticated against a specific
// generation. Closing it explicitly removes the connection from that
// generation's tracking set; generation revocation closes it automatically.
type BoundConn struct {
	net.Conn
	generation *generation

	closeOnce sync.Once
	cid       uint32
	port      uint32
}

// Close closes the underlying connection and unregisters it from the
// generation. It is safe to call concurrently and repeatedly.
func (b *BoundConn) Close() error {
	var err error
	b.closeOnce.Do(func() {
		b.generation.untrack(b)
		err = b.Conn.Close()
	})
	return err
}

// CID returns the source vsock CID the connection was bound to.
func (b *BoundConn) CID() uint32 { return b.cid }

// Port returns the destination port the connection was authorized for.
func (b *BoundConn) Port() uint32 { return b.port }

// InstanceID identifies the VM instance whose generation owns the connection.
func (b *BoundConn) InstanceID() string { return b.generation.instanceID }

// Generation returns the generation number of the lease owning the connection.
func (b *BoundConn) Generation() uint64 { return b.generation.gen }

func closeAll(conns []*BoundConn) {
	for _, conn := range conns {
		conn.Close()
	}
}
