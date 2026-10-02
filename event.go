// Package leaseaudit implements a replayable audit trail ("black box")
// for distributed lease coordination. Every lease acquire, renew,
// handover, expiry, rejection and race conflict is recorded as a
// structured event carrying a Lamport logical timestamp, so the full
// contention history can be replayed along a causal timeline.
package leaseaudit

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// EventKind classifies audit events.
type EventKind string

const (
	// EventRequestArrived is emitted when a node request reaches the
	// arbiter, before any lock is taken. It fixes the arrival order.
	EventRequestArrived EventKind = "REQUEST_ARRIVED"
	// EventLockAcquired / EventLockReleased track the arbiter's
	// internal decision-lock state transitions.
	EventLockAcquired EventKind = "ARBITER_LOCK_ACQUIRED"
	EventLockReleased EventKind = "ARBITER_LOCK_RELEASED"
	// EventRaceConflict is emitted when a request arrives while the
	// arbiter is still arbitrating another request for the same lease.
	EventRaceConflict EventKind = "RACE_CONFLICT"

	EventLeaseGranted  EventKind = "LEASE_GRANTED"
	EventLeaseRenewed  EventKind = "LEASE_RENEWED"
	EventLeaseHandover EventKind = "LEASE_HANDOVER"
	EventLeaseExpired  EventKind = "LEASE_EXPIRED"
	EventLeaseRejected EventKind = "LEASE_REJECTED"
)

// Event is one structured record in the audit trail.
type Event struct {
	Seq      int64     // global append order inside the log
	Lamport  uint64    // logical timestamp of the emitting party
	NodeID   string    // emitter: a node id or "arbiter"
	Kind     EventKind // event classification
	LeaseID  string    // lease this event refers to
	Term     uint64    // lease term (fencing token) when relevant
	Detail   string    // human readable explanation
	Causes   []int64   // Seq of causal parent events (happens-before edges)
	WallNano int64     // wall clock for display only, never for ordering
}

// LamportClock is a standard Lamport logical clock.
type LamportClock struct {
	v atomic.Uint64
}

// Tick advances the clock for a local event and returns the new value.
func (c *LamportClock) Tick() uint64 { return c.v.Add(1) }

// Witness merges a received timestamp (Lamport receive rule) and
// returns the updated local value.
func (c *LamportClock) Witness(remote uint64) uint64 {
	for {
		cur := c.v.Load()
		next := remote + 1
		if remote < cur {
			next = cur + 1
		}
		if c.v.CompareAndSwap(cur, next) {
			return next
		}
	}
}

// Now returns the current clock value without advancing it.
func (c *LamportClock) Now() uint64 { return c.v.Load() }

// AuditLog is a thread-safe, append-only event store.
type AuditLog struct {
	mu     sync.Mutex
	events []Event
	seq    int64
}

func NewAuditLog() *AuditLog { return &AuditLog{} }

// Append records a new event and returns its assigned sequence number.
func (l *AuditLog) Append(kind EventKind, lamport uint64, nodeID, leaseID string, term uint64, detail string, causes ...int64) Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	ev := Event{
		Seq:      l.seq,
		Lamport:  lamport,
		NodeID:   nodeID,
		Kind:     kind,
		LeaseID:  leaseID,
		Term:     term,
		Detail:   detail,
		Causes:   append([]int64(nil), causes...),
		WallNano: time.Now().UnixNano(),
	}
	l.events = append(l.events, ev)
	return ev
}

// Events returns a copy of all recorded events in append order.
func (l *AuditLog) Events() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Event(nil), l.events...)
}

// Get returns the event with the given sequence number (1-based).
func (l *AuditLog) Get(seq int64) (Event, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if seq < 1 || seq > int64(len(l.events)) {
		return Event{}, false
	}
	return l.events[seq-1], true
}

// Len reports how many events have been recorded.
func (l *AuditLog) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.events)
}

func (e Event) String() string {
	return fmt.Sprintf("#%-3d L=%-3d %-22s node=%-8s lease=%s term=%d %s",
		e.Seq, e.Lamport, e.Kind, e.NodeID, e.LeaseID, e.Term, e.Detail)
}
