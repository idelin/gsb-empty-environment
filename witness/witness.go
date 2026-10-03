// Package witness implements the third-party "judge" (Witness) that owns the
// single global write lease across datacenters. It is the only authority
// allowed to grant, renew and revoke the global lease, which guarantees that
// at most one datacenter can hold a valid write lease at any moment.
package witness

import (
	"sync"
	"time"
)

// Lease is the global write lease issued by the Witness.
// Epoch doubles as a fencing token: it increases monotonically every time the
// lease changes hands, so a stale holder can always be detected.
type Lease struct {
	Epoch   uint64    `json:"epoch"`
	Holder  string    `json:"holder"`
	Expires time.Time `json:"expires"`
}

// Valid reports whether the lease is held by someone at time t.
func (l Lease) Valid(t time.Time) bool {
	return l.Holder != "" && t.Before(l.Expires)
}

// Witness is the external judge keeping the global lease state.
// It is deliberately lightweight: a single mutex-protected lease record.
type Witness struct {
	mu    sync.Mutex
	lease Lease
	now   func() time.Time // injectable for tests
}

// New returns an empty Witness.
func New() *Witness {
	return &Witness{now: time.Now}
}

// Acquire tries to grant the lease to holder for the given ttl.
// It succeeds only when no lease is active, or the previous lease has
// expired (i.e. the previous holder is considered dead). Every handover
// bumps the Epoch fencing token. It returns the current lease and whether
// the caller was granted it.
func (w *Witness) Acquire(holder string, ttl time.Duration) (Lease, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	if w.lease.Valid(now) {
		if w.lease.Holder == holder {
			// Idempotent re-acquire by the current holder: just extend.
			w.lease.Expires = now.Add(ttl)
			return w.lease, true
		}
		// Someone else holds a live lease: refuse. This is what prevents
		// dual-primary while DC1 is still alive.
		return w.lease, false
	}
	w.lease = Lease{
		Epoch:   w.lease.Epoch + 1,
		Holder:  holder,
		Expires: now.Add(ttl),
	}
	return w.lease, true
}

// Renew extends the lease held by holder with the matching epoch.
// A renew is rejected when the holder's credential is stale — e.g. after the
// Witness has issued a takeover lease to the backup datacenter. This is how
// a recovered DC1 learns that its credential has been invalidated.
func (w *Witness) Renew(holder string, epoch uint64, ttl time.Duration) (Lease, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	if !w.lease.Valid(now) || w.lease.Holder != holder || w.lease.Epoch != epoch {
		return w.lease, false
	}
	w.lease.Expires = now.Add(ttl)
	return w.lease, true
}

// Status returns the current lease record.
func (w *Witness) Status() Lease {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lease
}
