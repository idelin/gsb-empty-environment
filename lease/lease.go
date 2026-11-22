package lease

import (
	"errors"
	"sync"
	"time"
)

var (
	// ErrRouteInUse is returned when issuing a lease on a
	// (connection, stream) route that already has an active lease.
	ErrRouteInUse = errors.New("lease: route already has an active lease")
	// ErrRequestLeased is returned when establishing a request that
	// already has a current lease; use Migrate instead.
	ErrRequestLeased = errors.New("lease: request already leased; migrate instead")
	// ErrNoLease is returned when no lease matches a route or request.
	ErrNoLease = errors.New("lease: no lease for route or request")
	// ErrOverlapExpired is returned when a datagram arrives on a
	// superseded lease whose dual-path overlap window has closed.
	ErrOverlapExpired = errors.New("lease: dual-path overlap window expired")
)

// Options configures a Manager or Dispatcher.
type Options struct {
	// Now supplies the current time; nil means time.Now. Tests inject a
	// FakeClock to control the overlap window.
	Now func() time.Time
	// Overlap is the bounded dual-path overlap window granted to a
	// superseded lease after migration. Zero means no overlap: the old
	// path stops accepting datagrams immediately.
	Overlap time.Duration
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Lease binds a tenant, a connection, a request stream, a business
// authorization and a generation. The host issues one lease per
// establishment or migration.
type Lease struct {
	ID         uint64
	Tenant     TenantID
	Conn       ConnectionID
	Stream     StreamID
	Auth       AuthID
	Generation Generation
	// Anchor and MaxSpan declare the sequence expansion window for
	// datagrams arriving on this lease's path.
	Anchor  uint64
	MaxSpan uint32

	IssuedAt time.Time
	// OverlapUntil is zero while the lease is current. When superseded
	// by migration it bounds the dual-path overlap window.
	OverlapUntil time.Time
}

// Current reports whether the lease is the request's current generation.
func (l *Lease) Current() bool { return l.OverlapUntil.IsZero() }

// Accepts reports whether the lease still routes datagrams at now.
func (l *Lease) Accepts(now time.Time) bool {
	return l.OverlapUntil.IsZero() || now.Before(l.OverlapUntil)
}

type routeKey struct {
	conn   ConnectionID
	stream StreamID
}

type reqKey struct {
	tenant TenantID
	auth   AuthID
}

// Manager owns the lease lifecycle: establishment, migration, overlap
// windows and routing. It carries no datagram state; see Dispatcher.
type Manager struct {
	mu      sync.Mutex
	opts    Options
	nextID  uint64
	leases  map[uint64]*Lease
	active  map[routeKey]*Lease
	overlap map[routeKey][]*Lease
	byReq   map[reqKey]*Lease
}

// NewManager returns an empty lease manager.
func NewManager(opts Options) *Manager {
	return &Manager{
		opts:    opts,
		leases:  make(map[uint64]*Lease),
		active:  make(map[routeKey]*Lease),
		overlap: make(map[routeKey][]*Lease),
		byReq:   make(map[reqKey]*Lease),
	}
}

// Issue establishes generation 1 of a business request on a route.
func (m *Manager) Issue(tenant TenantID, conn ConnectionID, stream StreamID, auth AuthID, anchor uint64, maxSpan uint32) (*Lease, error) {
	if uint64(maxSpan) >= SeqHalf {
		return nil, ErrInvalidSpan
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rk := routeKey{conn, stream}
	if _, ok := m.active[rk]; ok {
		return nil, ErrRouteInUse
	}
	rq := reqKey{tenant, auth}
	if _, ok := m.byReq[rq]; ok {
		return nil, ErrRequestLeased
	}
	l := m.newLeaseLocked(tenant, conn, stream, auth, 1, anchor, maxSpan)
	m.active[rk] = l
	m.byReq[rq] = l
	return l, nil
}

// Migrate moves a business request to a new route. The previous lease is
// superseded but keeps routing datagrams until the overlap window closes.
func (m *Manager) Migrate(tenant TenantID, auth AuthID, newConn ConnectionID, newStream StreamID, anchor uint64, maxSpan uint32) (*Lease, error) {
	if uint64(maxSpan) >= SeqHalf {
		return nil, ErrInvalidSpan
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rq := reqKey{tenant, auth}
	old, ok := m.byReq[rq]
	if !ok {
		return nil, ErrNoLease
	}
	newRK := routeKey{newConn, newStream}
	if _, ok := m.active[newRK]; ok {
		return nil, ErrRouteInUse
	}
	oldRK := routeKey{old.Conn, old.Stream}
	delete(m.active, oldRK)
	old.OverlapUntil = m.opts.now().Add(m.opts.Overlap)
	m.overlap[oldRK] = append(m.overlap[oldRK], old)
	l := m.newLeaseLocked(tenant, newConn, newStream, auth, old.Generation+1, anchor, maxSpan)
	m.active[newRK] = l
	m.byReq[rq] = l
	return l, nil
}

func (m *Manager) newLeaseLocked(tenant TenantID, conn ConnectionID, stream StreamID, auth AuthID, gen Generation, anchor uint64, maxSpan uint32) *Lease {
	m.nextID++
	l := &Lease{
		ID:         m.nextID,
		Tenant:     tenant,
		Conn:       conn,
		Stream:     stream,
		Auth:       auth,
		Generation: gen,
		Anchor:     anchor,
		MaxSpan:    maxSpan,
		IssuedAt:   m.opts.now(),
	}
	m.leases[l.ID] = l
	return l
}

// Route resolves the lease that should handle a datagram or ack arriving
// on (conn, stream) now. Superseded leases still resolve inside their
// overlap window; afterwards Route fails with ErrOverlapExpired.
func (m *Manager) Route(conn ConnectionID, stream StreamID) (*Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rk := routeKey{conn, stream}
	if l, ok := m.active[rk]; ok {
		return l, nil
	}
	now := m.opts.now()
	kept := m.overlap[rk][:0]
	var expired bool
	for _, l := range m.overlap[rk] {
		if l.Accepts(now) {
			kept = append(kept, l)
		} else {
			expired = true
		}
	}
	if len(kept) == 0 {
		delete(m.overlap, rk)
	} else {
		m.overlap[rk] = kept
	}
	if len(kept) > 0 {
		return kept[len(kept)-1], nil
	}
	if expired {
		return nil, ErrOverlapExpired
	}
	return nil, ErrNoLease
}

// Current returns the request's current (highest generation) lease.
func (m *Manager) Current(tenant TenantID, auth AuthID) (*Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.byReq[reqKey{tenant, auth}]; ok {
		return l, nil
	}
	return nil, ErrNoLease
}

// Close retires a lease entirely, e.g. once migration has completed and
// the old path must go silent. Late datagrams on a closed route fail
// with ErrNoLease.
func (m *Manager) Close(leaseID uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[leaseID]
	if !ok {
		return ErrNoLease
	}
	delete(m.leases, leaseID)
	rk := routeKey{l.Conn, l.Stream}
	if m.active[rk] == l {
		delete(m.active, rk)
	}
	if rest := removeLease(m.overlap[rk], l); len(rest) == 0 {
		delete(m.overlap, rk)
	} else {
		m.overlap[rk] = rest
	}
	if m.byReq[reqKey{l.Tenant, l.Auth}] == l {
		delete(m.byReq, reqKey{l.Tenant, l.Auth})
	}
	return nil
}

func removeLease(list []*Lease, l *Lease) []*Lease {
	out := list[:0]
	for _, x := range list {
		if x != l {
			out = append(out, x)
		}
	}
	return out
}
