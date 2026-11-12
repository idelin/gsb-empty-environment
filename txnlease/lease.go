// Package txnlease allocates and retires DNS transaction IDs for a
// forwarder whose upstream ID space is small enough to exhaust.
//
// A lease binds a transaction ID to the full forwarding context: the
// upstream endpoint, the connection instance the query was sent on, the
// question tuple, and a random challenge echoed by the upstream (for
// example an EDNS cookie). A response is only delivered when every bound
// attribute matches, so a late or forged answer cannot be handed to a
// newer query that happens to reuse the same ID.
//
// Released and cancelled leases are not recycled immediately: the slot
// enters an isolation window during which late duplicates are recognised
// as stale and dropped. Cancellation follows the same rule, because a
// cancelled query may still have an in-flight response.
package txnlease

import (
	crand "crypto/rand"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"sync"
	"time"
)

// Endpoint identifies one upstream resolver socket.
type Endpoint = netip.AddrPort

// ConnID identifies one upstream connection instance. It must be unique
// per connection and never reused, so that a packet belonging to a dead
// connection can never match a lease issued on its replacement.
type ConnID uint64

// Question is the exact wire-format question tuple of a forwarded query.
// Callers should record the bytes as sent; matching is exact.
type Question struct {
	Name  string
	Type  uint16
	Class uint16
}

// Response carries the binding attributes extracted from an inbound
// upstream packet.
type Response struct {
	Endpoint  Endpoint
	Conn      ConnID
	ID        uint16
	Question  Question
	Challenge uint64
}

// Verdict is the outcome of presenting a response to the manager.
type Verdict int

const (
	// Delivered means the response matched an active lease; the lease is
	// now released and its slot is quarantined.
	Delivered Verdict = iota
	// Stale means the ID's slot is quarantined (released or cancelled);
	// the packet is a late duplicate and must be dropped.
	Stale
	// Mismatch means the slot is active but at least one bound attribute
	// differs; the packet is forged or belongs to a dead connection.
	Mismatch
	// Unknown means the ID is outside the managed space or never leased.
	Unknown
)

func (v Verdict) String() string {
	switch v {
	case Delivered:
		return "delivered"
	case Stale:
		return "stale"
	case Mismatch:
		return "mismatch"
	case Unknown:
		return "unknown"
	}
	return fmt.Sprintf("verdict(%d)", int(v))
}

// ErrExhausted is returned by Acquire when every slot is active or
// quarantined. Callers must queue or wait; IDs are never recycled early.
var ErrExhausted = errors.New("txnlease: transaction ID space exhausted")

type slotState int

const (
	slotFree slotState = iota
	slotActive
	slotIsolation
)

type slot struct {
	state        slotState
	generation   uint64
	endpoint     Endpoint
	conn         ConnID
	question     Question
	challenge    uint64
	isolateUntil time.Time
}

// Lease is a handle to one acquired transaction ID. It is only valid for
// the generation of the slot it was taken from; once the slot is reused,
// operations through the old handle fail instead of corrupting the new
// tenant's state.
type Lease struct {
	mgr        *Manager
	id         uint16
	generation uint64
}

// ID returns the leased transaction ID.
func (l *Lease) ID() uint16 { return l.id }

// Cancel retires the lease without a response. The slot stays quarantined
// for the isolation window because a response may still arrive. It reports
// whether the lease was still active.
func (l *Lease) Cancel() bool { return l.mgr.Cancel(l) }

// Manager owns the transaction ID space.
type Manager struct {
	mu        sync.Mutex
	clock     Clock
	isolation time.Duration
	rng       *rand.Rand
	slots     []slot
}

// Config tunes a Manager.
type Config struct {
	// Space is the number of transaction IDs. Use the real 65536 in
	// production and a tiny value in tests.
	Space int
	// Isolation is how long a released or cancelled slot stays
	// quarantined. It should cover the longest plausible late packet.
	Isolation time.Duration
	// Clock defaults to SystemClock.
	Clock Clock
	// Rand supplies challenges. If nil, a crypto-seeded source is used.
	Rand *rand.Rand
}

// NewManager builds a Manager.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.Space <= 0 || cfg.Space > 65536 {
		return nil, fmt.Errorf("txnlease: space %d out of range", cfg.Space)
	}
	if cfg.Isolation <= 0 {
		return nil, errors.New("txnlease: isolation window must be positive")
	}
	clock := cfg.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	rng := cfg.Rand
	if rng == nil {
		var seed [32]byte
		if _, err := crand.Read(seed[:]); err != nil {
			return nil, fmt.Errorf("txnlease: seeding challenge source: %w", err)
		}
		rng = rand.New(rand.NewChaCha8(seed))
	}
	return &Manager{
		clock:     clock,
		isolation: cfg.Isolation,
		rng:       rng,
		slots:     make([]slot, cfg.Space),
	}, nil
}

// Acquire takes the first available ID for the given forwarding context
// and returns its lease. IDs whose isolation window has expired are
// reclaimed lazily; if none are available it returns ErrExhausted.
func (m *Manager) Acquire(ep Endpoint, conn ConnID, q Question) (*Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.Now()
	for id := range m.slots {
		s := &m.slots[id]
		if s.state == slotIsolation && !now.Before(s.isolateUntil) {
			s.state = slotFree
		}
		if s.state != slotFree {
			continue
		}
		s.state = slotActive
		s.generation++
		s.endpoint = ep
		s.conn = conn
		s.question = q
		s.challenge = m.rng.Uint64()
		s.isolateUntil = time.Time{}
		return &Lease{mgr: m, id: uint16(id), generation: s.generation}, nil
	}
	return nil, ErrExhausted
}

// Challenge returns the random challenge bound to the lease, for the
// caller to embed in the forwarded query (e.g. as an EDNS cookie).
func (m *Manager) Challenge(l *Lease) (uint64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.live(l); s != nil {
		return s.challenge, true
	}
	return 0, false
}

// Deliver presents an inbound response. On Delivered the lease is
// released and its slot enters the isolation window, so late duplicates
// of the same packet are subsequently reported Stale.
func (m *Manager) Deliver(r Response) (Verdict, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if int(r.ID) >= len(m.slots) {
		return Unknown, nil
	}
	s := &m.slots[r.ID]
	now := m.clock.Now()
	switch s.state {
	case slotFree:
		return Unknown, nil
	case slotIsolation:
		if !now.Before(s.isolateUntil) {
			s.state = slotFree
			return Unknown, nil
		}
		return Stale, nil
	}
	if s.endpoint != r.Endpoint || s.conn != r.Conn ||
		s.question != r.Question || s.challenge != r.Challenge {
		return Mismatch, nil
	}
	s.state = slotIsolation
	s.isolateUntil = now.Add(m.isolation)
	return Delivered, nil
}

// Cancel retires an active lease. The slot enters the isolation window
// rather than becoming free, because a response may still be in flight.
func (m *Manager) Cancel(l *Lease) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l == nil || l.mgr != m {
		return false
	}
	s := m.live(l)
	if s == nil {
		return false
	}
	s.state = slotIsolation
	s.isolateUntil = m.clock.Now().Add(m.isolation)
	return true
}

// live returns the slot if the lease still names its current tenant.
func (m *Manager) live(l *Lease) *slot {
	if l == nil || int(l.id) >= len(m.slots) {
		return nil
	}
	s := &m.slots[l.id]
	if s.state != slotActive || s.generation != l.generation {
		return nil
	}
	return s
}

// Stats reports slot occupancy, primarily for tests and metrics.
type Stats struct {
	Active    int
	Isolation int
	Free      int
}

// Stats snapshots the slot states, reclaiming expired isolations.
func (m *Manager) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.Now()
	var st Stats
	for i := range m.slots {
		s := &m.slots[i]
		if s.state == slotIsolation && !now.Before(s.isolateUntil) {
			s.state = slotFree
		}
		switch s.state {
		case slotActive:
			st.Active++
		case slotIsolation:
			st.Isolation++
		default:
			st.Free++
		}
	}
	return st
}

// ConnIDSource hands out monotonically increasing connection instance
// IDs, one per upstream connection establishment.
type ConnIDSource struct {
	mu   sync.Mutex
	next ConnID
}

// Next returns a fresh, never-reused connection instance ID.
func (s *ConnIDSource) Next() ConnID {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	return s.next
}
