// Package leasearb implements a generation-fenced write arbiter for leased
// resources. A resource has exactly one current holder identified by a
// monotonically increasing generation (fence token). The arbiter
// distinguishes a duplicated request (same request ID, idempotent replay)
// from a stale holder (older generation, fenced off). Renewal can extend a
// live lease but can never resurrect a superseded generation.
package leasearb

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Status is the outcome of a Write call.
type Status string

const (
	// StatusApplied means the write was accepted and recorded.
	StatusApplied Status = "APPLIED"
	// StatusDuplicate means the request ID was already processed; the
	// recorded result is returned and nothing is re-applied.
	StatusDuplicate Status = "DUPLICATE"
	// StatusStale means the caller's generation is older than the current
	// one: the caller is a fenced-off former holder.
	StatusStale Status = "STALE"
	// StatusExpired means the caller holds the current generation but its
	// lease has lapsed; it must renew before writing.
	StatusExpired Status = "EXPIRED"
)

// WriteResult is the outcome of a Write call.
type WriteResult struct {
	Status     Status
	Generation uint64 // generation the result was decided under
	Detail     string
}

func (r WriteResult) String() string {
	if r.Detail == "" {
		return fmt.Sprintf("%s(gen=%d)", r.Status, r.Generation)
	}
	return fmt.Sprintf("%s(gen=%d, %s)", r.Status, r.Generation, r.Detail)
}

// ErrStaleGeneration is returned by Renew when the caller's generation has
// been superseded: renewal cannot resurrect an old generation.
var ErrStaleGeneration = errors.New("leasearb: stale generation, renewal cannot resurrect it")

// ErrNotHolder is returned when the named holder does not hold the resource.
var ErrNotHolder = errors.New("leasearb: not the current holder")

// Snapshot is a read-only view of a resource's arbitration state.
type Snapshot struct {
	Holder     string
	Generation uint64
	ExpiresAt  time.Time
	Expired    bool
	Writes     []string
}

type resource struct {
	holder     string
	generation uint64
	expiresAt  time.Time
	// acks records decided writes by request ID. It intentionally survives
	// generation changes so a very late retry of an already-applied write
	// is recognised as a duplicate instead of being misreported as stale.
	acks   map[string]WriteResult
	writes []string
}

// Arbiter arbitrates writes to leased resources. It is safe for concurrent
// use; that is what makes handoff races deterministic to reason about: every
// operation is serialised against the current generation.
type Arbiter struct {
	mu       sync.Mutex
	clock    *ManualClock
	leaseTTL time.Duration
	res      map[string]*resource
}

func NewArbiter(clock *ManualClock, leaseTTL time.Duration) *Arbiter {
	return &Arbiter{clock: clock, leaseTTL: leaseTTL, res: make(map[string]*resource)}
}

// Now reports the arbiter's current time.
func (a *Arbiter) Now() time.Time { return a.clock.Now() }

// LeaseTTL returns the lease duration granted by Acquire/Renew.
func (a *Arbiter) LeaseTTL() time.Duration { return a.leaseTTL }

// Acquire takes (or transfers) the resource for holder, bumping the
// generation. Any writes carrying the previous generation are fenced off
// from this point on. Returns the new generation and its expiry.
func (a *Arbiter) Acquire(name, holder string) (uint64, time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.res[name]
	if r == nil {
		r = &resource{acks: make(map[string]WriteResult)}
		a.res[name] = r
	}
	r.generation++
	r.holder = holder
	r.expiresAt = a.clock.Now().Add(a.leaseTTL)
	return r.generation, r.expiresAt
}

// Renew extends the lease of the current holder. It refuses to resurrect a
// superseded generation: if gen is older than the current generation the
// resource has already been handed off and ErrStaleGeneration is returned.
// Renewing an expired-but-still-current lease is allowed and re-anchors the
// expiry at now+TTL.
func (a *Arbiter) Renew(name, holder string, gen uint64) (time.Time, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.res[name]
	if r == nil {
		return time.Time{}, ErrNotHolder
	}
	if gen < r.generation {
		return time.Time{}, fmt.Errorf("%w: gen %d < current gen %d", ErrStaleGeneration, gen, r.generation)
	}
	if r.holder != holder {
		return time.Time{}, fmt.Errorf("%w: %q holds gen %d", ErrNotHolder, r.holder, r.generation)
	}
	r.expiresAt = a.clock.Now().Add(a.leaseTTL)
	return r.expiresAt, nil
}

// Write arbitrates a single write. holder and gen identify the caller's
// lease; reqID makes the write idempotent. Exactly one of four things
// happens, checked in this order:
//
//  1. reqID was already decided -> DUPLICATE, recorded result returned.
//  2. gen is behind the current generation -> STALE (fenced old holder).
//  3. caller is not the holder, or the lease has lapsed -> STALE / EXPIRED.
//  4. otherwise the write is applied and the result recorded under reqID.
func (a *Arbiter) Write(name, holder string, gen uint64, reqID, payload string) WriteResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.res[name]
	if r == nil {
		return WriteResult{Status: StatusStale, Generation: gen, Detail: "resource never acquired"}
	}
	if prev, ok := r.acks[reqID]; ok {
		return WriteResult{Status: StatusDuplicate, Generation: prev.Generation,
			Detail: fmt.Sprintf("req %s already %s", reqID, prev.Status)}
	}
	if gen < r.generation {
		return WriteResult{Status: StatusStale, Generation: r.generation,
			Detail: fmt.Sprintf("fenced: gen %d < current gen %d (held by %s)", gen, r.generation, r.holder)}
	}
	if r.holder != holder {
		return WriteResult{Status: StatusStale, Generation: r.generation,
			Detail: fmt.Sprintf("holder mismatch: %q holds gen %d", r.holder, r.generation)}
	}
	if !a.clock.Now().Before(r.expiresAt) {
		return WriteResult{Status: StatusExpired, Generation: r.generation,
			Detail: "lease lapsed, renew before writing"}
	}
	res := WriteResult{Status: StatusApplied, Generation: gen}
	r.acks[reqID] = res
	r.writes = append(r.writes, payload)
	return res
}

// Snapshot returns the current arbitration state of a resource.
func (a *Arbiter) Snapshot(name string) (Snapshot, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.res[name]
	if r == nil {
		return Snapshot{}, false
	}
	writes := make([]string, len(r.writes))
	copy(writes, r.writes)
	return Snapshot{
		Holder:     r.holder,
		Generation: r.generation,
		ExpiresAt:  r.expiresAt,
		Expired:    !a.clock.Now().Before(r.expiresAt),
		Writes:     writes,
	}, true
}
