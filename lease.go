package main

import (
	"errors"
	"fmt"
)

// Lease is a grant of mastership. Fence is the fencing token issued
// with the grant; it never changes across renewals of the same lease.
type Lease struct {
	Holder string
	Fence  uint64
	Expiry int64
}

func (l Lease) String() string {
	return fmt.Sprintf("Lease{holder=%s fence=%d expiry=%d}", l.Holder, l.Fence, l.Expiry)
}

var (
	ErrNotHolder    = errors.New("not the current lease holder")
	ErrLeaseExpired = errors.New("lease has expired")
)

// LeaseHeldError reports a grant attempt while another competitor
// still holds an unexpired lease.
type LeaseHeldError struct {
	Holder string
	Expiry int64
}

func (e *LeaseHeldError) Error() string {
	return fmt.Sprintf("lease is held by %s until t=%d", e.Holder, e.Expiry)
}

// LeaseManager grants and renews leases. Granting, renewing and
// resource writes are deliberately separate operations: the manager
// never talks to the resource, and the resource enforces fencing on
// its own.
type LeaseManager struct {
	clock *Clock
	store *Store
	fence uint64 // highest fence ever issued, recovered from store
	lease *Lease // currently outstanding lease, if any
}

// NewLeaseManager bootstraps a manager. Recovery is safe: the fence
// counter is reloaded from the durable store, so it can never go
// below a fence issued before a crash.
func NewLeaseManager(clock *Clock, store *Store) *LeaseManager {
	return &LeaseManager{clock: clock, store: store, fence: store.LoadFence()}
}

// Grant issues a new lease if no unexpired lease is outstanding.
// The fence is persisted before the lease is returned; if persistence
// fails, no lease is granted and no fence is consumed.
func (m *LeaseManager) Grant(holder string, ttl int64) (Lease, error) {
	if m.lease != nil && m.clock.Now() < m.lease.Expiry {
		return Lease{}, &LeaseHeldError{Holder: m.lease.Holder, Expiry: m.lease.Expiry}
	}
	next := m.fence + 1
	if err := m.store.SaveFence(next); err != nil {
		return Lease{}, err
	}
	m.fence = next
	lease := &Lease{Holder: holder, Fence: next, Expiry: m.clock.Now() + ttl}
	m.lease = lease
	return *lease, nil
}

// Renew extends the current lease for its holder. The fence number is
// unchanged: renewal is a time extension, not a new grant.
func (m *LeaseManager) Renew(holder string, fence uint64, ttl int64) (Lease, error) {
	if m.lease == nil || m.lease.Holder != holder || m.lease.Fence != fence {
		return Lease{}, ErrNotHolder
	}
	if m.clock.Now() >= m.lease.Expiry {
		return Lease{}, ErrLeaseExpired
	}
	m.lease.Expiry = m.clock.Now() + ttl
	return *m.lease, nil
}
