// Package lease implements a lease controller with dynamic renewal and
// graceful, zero-downtime handover between holder nodes.
package lease

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrLeaseHeld        = errors.New("lease: already held by another node")
	ErrNotHolder        = errors.New("lease: caller is not the holder")
	ErrLeaseExpired     = errors.New("lease: expired")
	ErrLeaseFrozen      = errors.New("lease: frozen, handover in progress")
	ErrInvalidTerm      = errors.New("lease: fencing token mismatch")
	ErrInvalidCandidate = errors.New("lease: handover candidate mismatch")
	ErrNoHandover       = errors.New("lease: no pending handover")
	ErrNotLeader        = errors.New("node: not the lease holder")
	ErrWriteTimeout     = errors.New("node: write timed out while frozen")
	ErrNoLeader         = errors.New("router: no leader available")
)

// Clock abstracts time so tests can control lease expiry deterministically.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// ManualClock is a test Clock advanced by hand.
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewManualClock(start time.Time) *ManualClock { return &ManualClock{now: start} }

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Lease is the credential granted by the Coordinator. Term is a fencing
// token that increases on every ownership change.
type Lease struct {
	ID         string
	HolderID   string
	Term       uint64
	AcquiredAt time.Time
	ExpiresAt  time.Time
	Renewals   int
	Context    map[string]string
}

func (l *Lease) snapshot() *Lease {
	cp := *l
	cp.Context = cloneContext(l.Context)
	return &cp
}

func cloneContext(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// HandoverPackage carries the remaining lease context from the outgoing
// holder to the designated candidate.
type HandoverPackage struct {
	LeaseID      string
	FromHolder   string
	ToCandidate  string
	Term         uint64
	IssuedAt     time.Time
	ExpiresAt    time.Time
	RemainingTTL time.Duration
	Context      map[string]string
}
