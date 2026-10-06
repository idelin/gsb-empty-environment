package main

import "errors"

var (
	ErrLeaseHeld    = errors.New("lease held by a live holder")
	ErrNotHolder    = errors.New("not the current lease holder")
	ErrLeaseExpired = errors.New("lease expired")
)

// Lease is a grant token. Fence is issued by the manager and is
// strictly increasing across all grants, including after recovery.
type Lease struct {
	Holder    string
	Fence     uint64
	ExpiresAt int64
}

// LeaseManager grants and renews leases. Granting, renewing and
// resource writes are separate operations: a write never implicitly
// extends a lease, and a renewal never touches the resource.
type LeaseManager struct {
	clock     *Clock
	store     *Store
	ttl       int64
	nextFence uint64 // next token to issue; never decreases, survives recovery
	current   *Lease
}

func NewLeaseManager(clock *Clock, store *Store, ttl int64) *LeaseManager {
	return &LeaseManager{clock: clock, store: store, ttl: ttl, nextFence: 1}
}

// Recover reloads the fence counter after a crash. Recovery may only
// raise (or keep) the counter, never lower it.
func (lm *LeaseManager) Recover() error {
	st, err := lm.store.Load()
	if err != nil {
		return err
	}
	if st.NextFence > lm.nextFence {
		lm.nextFence = st.NextFence
	}
	lm.current = nil // in-memory grant is lost on crash; holder must re-acquire
	return nil
}

func (lm *LeaseManager) live() bool {
	return lm.current != nil && lm.clock.Now() < lm.current.ExpiresAt
}

// Grant issues a new lease with a fresh, higher fence. Fails while a
// previous lease is still live.
func (lm *LeaseManager) Grant(holder string) (*Lease, error) {
	if lm.live() {
		return nil, ErrLeaseHeld
	}
	fence := lm.nextFence
	lm.nextFence++
	if err := lm.store.Save(persistentState{NextFence: lm.nextFence}); err != nil {
		return nil, err
	}
	lm.current = &Lease{Holder: holder, Fence: fence, ExpiresAt: lm.clock.Now() + lm.ttl}
	cp := *lm.current
	return &cp, nil
}

// Renew extends the holder's lease. Fence is unchanged: renewal does
// not create a new epoch.
func (lm *LeaseManager) Renew(holder string, fence uint64) (*Lease, error) {
	if !lm.live() {
		return nil, ErrLeaseExpired
	}
	if lm.current.Holder != holder || lm.current.Fence != fence {
		return nil, ErrNotHolder
	}
	lm.current.ExpiresAt = lm.clock.Now() + lm.ttl
	cp := *lm.current
	return &cp, nil
}
