// Package leasefence implements a lease fence for safety-critical jobs that may
// outlive the lease they were started under.
//
// A Fence protects one abstract resource. Each successful Acquire starts a new
// holder generation with a strictly larger generation number; within a
// generation, every Renew produces a strictly larger renewal version. Both
// counters are therefore monotonic and together form an unambiguous fencing
// token (generation, version).
//
// A job must call Begin before it starts preparing its commit. Begin
// registers an in-flight ticket and snapshots the current fencing token. The
// ticket is validated again at commit time: Commit re-checks that the same
// generation is still the holder and that the exact (generation, version) pair
// has not been terminated. A renewal bumps the version but never terminates
// the previous version, so a job already in flight remains committable until
// the lease is actually reaped.
//
// Expiry itself changes nothing until a reaper runs. ReapExpired (and the
// optional background reaper) terminates only the exact version that is
// current and past its deadline; after termination, a stale reaper call finds
// a different current token and is a no-op, so late reaping can never kill the
// new holder's version.
package leasefence

import (
	"errors"
	"sync"
	"time"
)

var (
	// ErrClosed is returned after Fence.Close has been called.
	ErrClosed = errors.New("leasefence: fence is closed")
	// ErrHeld is returned by Acquire while a generation still holds the fence.
	ErrHeld = errors.New("leasefence: fence already has a holder")
	// ErrNoLease is returned when no generation has ever been acquired.
	ErrNoLease = errors.New("leasefence: no lease has been acquired")
	// ErrFenced is returned when a handle or ticket belongs to a generation
	// that is no longer the holder.
	ErrFenced = errors.New("leasefence: generation is no longer the holder")
	// ErrExpired is returned when the exact token has been reaped, or an
	// operation is attempted after the current lease passed its deadline.
	ErrExpired = errors.New("leasefence: lease expired")
)

// Token is the immutable fencing identity captured by a handle or ticket.
type Token struct {
	generation uint64
	version    uint64
	deadline   time.Time
}

// Generation is the monotonic holder generation; every Acquire increments it.
func (t Token) Generation() uint64 { return t.generation }

// Version is the monotonic renewal version within a generation; Acquire
// starts it at 1 and every Renew increments it.
func (t Token) Version() uint64 { return t.version }

// Deadline is the time at which this token's lease expires.
func (t Token) Deadline() time.Time { return t.deadline }

// Handle identifies one holder generation and its latest renewal version.
// Renew returns a new Handle; older Handles remain readable but can never be
// used to mutate a newer generation.
type Handle struct {
	fence *Fence
	token Token
}

// Token returns the fencing token captured by this handle.
func (h Handle) Token() Token { return h.token }

// Ticket is an in-flight authorization returned by Begin. It captures the
// token under which the job prepared its commit together with a unique
// identifier so consumed or cancelled tickets cannot be replayed.
type Ticket struct {
	fence *Fence
	token Token
	id    uint64
}

// Token returns the fencing token captured by this ticket.
func (t Ticket) Token() Token { return t.token }

// Snapshot is a point-in-time diagnostic view of a Fence.
type Snapshot struct {
	Closed          bool
	Held            bool
	Generation      uint64
	Version         uint64
	Deadline        time.Time
	InFlight        int
	TerminatedPairs int
}

// Fence serializes holder generations and tracks in-flight commit tickets.
// The zero value is not usable; create one with NewFence.
type Fence struct {
	mu sync.Mutex

	clock Clock
	ttl   time.Duration

	closed bool

	held       bool
	generation uint64
	version    uint64
	deadline   time.Time
	nextID     uint64

	// tickets holds the IDs of outstanding in-flight tickets per holder
	// generation. Keying by generation keeps tickets valid across renewals
	// while the per-ID set ensures a consumed or cancelled ticket can never
	// pass final validation again.
	tickets map[tokenKey]map[uint64]struct{}
	// terminated holds exact (generation, version) pairs killed by the
	// reaper. Once a pair is terminated its tickets can never commit.
	terminated map[tokenKey]struct{}
}

type tokenKey struct {
	generation uint64
	version    uint64
}

func keyOf(t Token) tokenKey {
	return tokenKey{generation: t.generation, version: t.version}
}

// NewFence creates a fence whose leases live for ttl without renewal. A nil
// clock selects SystemClock.
func NewFence(clock Clock, ttl time.Duration) *Fence {
	if ttl <= 0 {
		panic("leasefence: ttl must be positive")
	}
	if clock == nil {
		clock = SystemClock{}
	}
	return &Fence{
		clock:      clock,
		ttl:        ttl,
		tickets:    make(map[tokenKey]map[uint64]struct{}),
		terminated: make(map[tokenKey]struct{}),
	}
}

// Acquire starts a new holder generation after silently reaping any expired
// current lease. It returns ErrHeld if the current generation is still alive.
// The generation counter never repeats: a reused resource always gets a
// strictly larger generation than every previous holder.
func (f *Fence) Acquire() (Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return Handle{}, ErrClosed
	}
	f.reapExpiredLocked()
	if f.held {
		return Handle{}, ErrHeld
	}
	f.generation++
	f.version = 1
	f.deadline = f.clock.Now().Add(f.ttl)
	f.held = true
	return f.handleLocked(), nil
}

// Renew extends the lease of the handle's generation and returns a handle
// carrying the next monotonic version. Handles from reaped or superseded
// generations return ErrFenced; renewing after the deadline without a reaper
// returns ErrExpired.
func (f *Fence) Renew(h Handle) (Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return Handle{}, ErrClosed
	}
	if !f.belongsToCurrentGenerationLocked(h) {
		return Handle{}, ErrFenced
	}
	if f.pastDeadlineLocked() {
		return Handle{}, ErrExpired
	}
	f.version++
	f.deadline = f.clock.Now().Add(f.ttl)
	return f.handleLocked(), nil
}

// Begin registers an in-flight ticket for the handle's generation. The ticket
// snapshots the current token and must be presented to Commit, Cancel or
// Validate. Registering before any side-effecting preparation is what lets the
// final validation prove the job was authorized when it started.
func (f *Fence) Begin(h Handle) (Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return Ticket{}, ErrClosed
	}
	if !f.belongsToCurrentGenerationLocked(h) {
		return Ticket{}, ErrFenced
	}
	if f.pastDeadlineLocked() {
		return Ticket{}, ErrExpired
	}
	key := tokenKey{generation: f.generation}
	ids := f.tickets[key]
	if ids == nil {
		ids = make(map[uint64]struct{})
		f.tickets[key] = ids
	}
	ticket := f.ticketLocked()
	ticket.id = f.nextID + 1
	f.nextID = ticket.id
	ids[ticket.id] = struct{}{}
	return ticket, nil
}

// Validate re-checks an in-flight ticket without mutating the ticket set. The
// host calls it both inside Commit and again immediately before applying a
// prepared transaction to an external resource.
func (f *Fence) Validate(t Ticket) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	return f.validateTicketLocked(t)
}

// Commit runs prepare while holding no fence lock (so a blocking preparer does
// not stall the reaper), then performs the mandatory final validation of the
// ticket against the current generation. On success the ticket is consumed and
// the host may apply the prepared work. On failure the ticket is consumed as
// well and the host must roll the preparation back.
func (f *Fence) Commit(t Ticket, prepare func(Token) error) error {
	if prepare != nil {
		if err := prepare(t.token); err != nil {
			_ = f.Cancel(t)
			return err
		}
	}

	f.mu.Lock()
	err := ErrClosed
	if !f.closed {
		err = f.validateTicketLocked(t)
	}
	if err == nil {
		f.releaseTicketLocked(t)
	}
	f.mu.Unlock()

	if err != nil && !f.closed {
		// Validation rejected a live ticket; make sure it cannot linger.
		f.releaseTicket(t)
	}
	return err
}

// releaseTicket is the lock-free wrapper around releaseTicketLocked used on
// the Commit failure path.
func (f *Fence) releaseTicket(t Ticket) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.releaseTicketLocked(t)
	}
}

// Cancel releases an in-flight ticket without committing. It is safe to call
// on an already consumed or terminated ticket.
func (f *Fence) Cancel(t Ticket) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	f.releaseTicketLocked(t)
	return nil
}

// ReapExpired terminates the current version only when its deadline has
// passed. It is idempotent and safe to call late: once the version has been
// replaced by a renewal or a new generation, the stale call is a no-op and the
// new holder is left untouched.
func (f *Fence) ReapExpired() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || !f.held {
		return false
	}
	return f.terminateIfCurrentLocked(f.currentKeyLocked(), f.clock.Now())
}

// RunReaper invokes ReapExpired for every value received from tickCh until the
// channel is closed. It is intended to run in its own goroutine fed by a
// time.Ticker; the fence clock remains the sole authority on expiry.
func (f *Fence) RunReaper(tickCh <-chan time.Time) {
	for range tickCh {
		f.ReapExpired()
	}
}

// Close stops the fence. Subsequent operations fail with ErrClosed; existing
// tickets become uncommittable. It is safe to call multiple times.
func (f *Fence) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// Snapshot returns a diagnostic copy of the fence state.
func (f *Fence) Snapshot() Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := Snapshot{
		Closed:          f.closed,
		Held:            f.held,
		Generation:      f.generation,
		Version:         f.version,
		Deadline:        f.deadline,
		TerminatedPairs: len(f.terminated),
	}
	for _, ids := range f.tickets {
		s.InFlight += len(ids)
	}
	return s
}

// terminateVersion is the exact-version reaping primitive. It terminates only
// the pair identified by key and only while that pair is still the current
// token; a stale reaper carrying an old key is therefore a no-op. The deadline
// observation is re-checked inside the fence so reaping cannot be fooled by a
// deadline check performed before a renewal.
func (f *Fence) terminateVersion(key tokenKey, now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || !f.held {
		return false
	}
	return f.terminateIfCurrentLocked(key, now)
}

func (f *Fence) reapExpiredLocked() {
	if !f.held {
		return
	}
	f.terminateIfCurrentLocked(f.currentKeyLocked(), f.clock.Now())
}

// terminateIfCurrentLocked kills exactly key when it is still the current
// token and its deadline has passed. The caller must hold f.mu.
func (f *Fence) terminateIfCurrentLocked(key tokenKey, now time.Time) bool {
	if !f.held || f.currentKeyLocked() != key {
		return false
	}
	if now.Before(f.deadline) {
		return false
	}
	f.terminated[key] = struct{}{}
	f.held = false
	f.deadline = time.Time{}
	return true
}

// validateTicketLocked implements the mandatory final check: the ticket's
// generation must still be the holder, the exact token captured at Begin must
// not have been reaped, the current lease must not have passed its deadline,
// and this exact ticket must still be registered as in flight.
// Renewal intentionally does not terminate earlier versions, so a ticket
// remains valid across renewals until its own version is reaped; a consumed
// (committed or cancelled) ticket fails the in-flight check instead. The
// caller must hold f.mu.
func (f *Fence) validateTicketLocked(t Ticket) error {
	if t.fence != f || f.generation == 0 {
		return ErrNoLease
	}
	if !f.held || f.generation != t.token.generation {
		return ErrFenced
	}
	key := keyOf(t.token)
	if _, dead := f.terminated[key]; dead {
		return ErrExpired
	}
	if f.pastDeadlineLocked() {
		return ErrExpired
	}
	ids := f.tickets[tokenKey{generation: f.generation}]
	if _, live := ids[t.id]; !live {
		return ErrFenced
	}
	return nil
}

func (f *Fence) releaseTicketLocked(t Ticket) {
	key := tokenKey{generation: t.token.generation}
	if ids, ok := f.tickets[key]; ok {
		delete(ids, t.id)
		if len(ids) == 0 {
			delete(f.tickets, key)
		}
	}
}

func (f *Fence) belongsToCurrentGenerationLocked(h Handle) bool {
	return h.fence == f && f.held && h.token.generation == f.generation
}

func (f *Fence) pastDeadlineLocked() bool {
	return !f.clock.Now().Before(f.deadline)
}

func (f *Fence) currentKeyLocked() tokenKey {
	return tokenKey{generation: f.generation, version: f.version}
}

func (f *Fence) handleLocked() Handle {
	return Handle{fence: f, token: f.tokenLocked()}
}

func (f *Fence) ticketLocked() Ticket {
	return Ticket{fence: f, token: f.tokenLocked()}
}

func (f *Fence) tokenLocked() Token {
	return Token{
		generation: f.generation,
		version:    f.version,
		deadline:   f.deadline,
	}
}
