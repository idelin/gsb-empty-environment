package leaseread

import (
	"sync"
	"time"
)

// Lease represents the leader's time-bounded promise that no other node can
// become leader and commit writes before Expiry. While the local clock is
// strictly inside the lease window, the leader is guaranteed to hold the
// newest committed data and may serve reads locally.
type Lease struct {
	mu     sync.RWMutex
	expiry time.Time
	term   uint64
}

func NewLease(term uint64) *Lease {
	return &Lease{term: term}
}

// Renew extends the lease expiry. Called after a quorum of followers
// acknowledges a heartbeat for the current term.
func (l *Lease) Renew(expiry time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if expiry.After(l.expiry) {
		l.expiry = expiry
	}
}

// Expire forces the lease to be invalid (e.g. on leader step-down).
func (l *Lease) Expire() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expiry = time.Time{}
}

// Remaining returns how much lease time is left relative to now.
// A non-positive value means the lease cannot authorize local reads.
func (l *Lease) Remaining(now time.Time) time.Duration {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.expiry.Sub(now)
}

func (l *Lease) Term() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.term
}
