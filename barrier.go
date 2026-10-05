// Package barrier implements a reusable phased (cyclic) barrier.
//
// A Barrier synchronizes a dynamically sized group of participants across
// a sequence of numbered rounds. A round settles in exactly one of two ways:
//
//   - Released:  the last expected participant arrived;
//   - Cancelled: Cancel was called before the round completed.
//
// Every call that waits on a round reports the round number and the
// settlement reason via Result.
//
// Group membership changes are deferred to the next round: a participant
// declares its departure upon arrival (ArriveAndLeave) and new participants
// registered with Join only become active from the following round. All
// membership deltas are applied exactly once, at the moment a round settles,
// so concurrent last-arrival, departure and cancellation can never
// double-count the group size.
package barrier

import "sync"

// Reason describes why a round of the barrier settled.
type Reason int

const (
	// Released means the round completed because the last expected
	// participant arrived.
	Released Reason = iota
	// Cancelled means the round was cancelled before it completed.
	Cancelled
)

// String returns a human-readable name for the settlement reason.
func (r Reason) String() string {
	switch r {
	case Released:
		return "released"
	case Cancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// Result reports the outcome of a call that waits on a round.
type Result struct {
	// Round is the number of the round that settled.
	Round int64
	// Reason is why that round settled.
	Reason Reason
}

// generation tracks the waiters of a single round. The result is written
// before done is closed, so reading it after <-done is race-free.
type generation struct {
	done   chan struct{}
	result Result
}

// Barrier is a reusable phased barrier. The zero value is not usable;
// construct one with New.
type Barrier struct {
	mu sync.Mutex

	round   int64 // current round number
	parties int   // arrivals expected in the current round
	arrived int   // arrivals seen in the current round

	pendingJoin  int // joins taking effect next round
	pendingLeave int // departures taking effect next round

	gen *generation
}

// New returns a Barrier expecting parties arrivals per round.
// It panics if parties is not positive.
func New(parties int) *Barrier {
	if parties <= 0 {
		panic("barrier: parties must be positive")
	}
	return &Barrier{
		parties: parties,
		gen:     &generation{done: make(chan struct{})},
	}
}

// Arrive signals arrival at the current round and blocks until the round
// settles, either because the last expected participant arrived or because
// the round was cancelled.
func (b *Barrier) Arrive() Result {
	b.mu.Lock()
	g := b.arriveLocked()
	b.mu.Unlock()
	<-g.done
	return g.result
}

// ArriveAndLeave signals arrival at the current round and declares that the
// caller leaves the barrier: starting from the next round it is no longer
// counted among the expected participants. It blocks until the current
// round settles, exactly like Arrive.
func (b *Barrier) ArriveAndLeave() Result {
	b.mu.Lock()
	b.pendingLeave++
	g := b.arriveLocked()
	b.mu.Unlock()
	<-g.done
	return g.result
}

// arriveLocked records an arrival and settles the round if it was the last
// one. It returns the generation the caller must wait on.
func (b *Barrier) arriveLocked() *generation {
	g := b.gen
	b.arrived++
	if b.arrived >= b.parties {
		b.settleLocked(Released)
	}
	return g
}

// Join registers a new participant. The join takes effect only from the
// next round, so Join blocks until the current round settles and then
// returns the number of the round in which the caller may first Arrive.
func (b *Barrier) Join() int64 {
	b.mu.Lock()
	b.pendingJoin++
	g := b.gen
	round := b.round + 1
	b.mu.Unlock()
	<-g.done
	return round
}

// Cancel settles the current round immediately with reason Cancelled,
// waking every participant waiting on it, and advances the barrier to the
// next round. It returns the number of the round that was cancelled.
func (b *Barrier) Cancel() int64 {
	b.mu.Lock()
	round := b.round
	b.settleLocked(Cancelled)
	b.mu.Unlock()
	return round
}

// Round returns the current round number.
func (b *Barrier) Round() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.round
}

// Parties returns the number of arrivals expected in the current round.
func (b *Barrier) Parties() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.parties
}

// Arrived returns how many participants have arrived in the current round
// and are still waiting for it to settle.
func (b *Barrier) Arrived() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.arrived
}

// settleLocked settles the current round with the given reason and advances
// to the next round. It is the only place where membership deltas are
// applied, and it runs at most once per round because it rotates the
// generation and resets the arrival count; this guarantees that departures
// and joins are never applied twice, even when the last arrival and a
// cancellation race with each other.
func (b *Barrier) settleLocked(reason Reason) {
	b.gen.result = Result{Round: b.round, Reason: reason}
	close(b.gen.done)

	b.round++
	b.parties += b.pendingJoin - b.pendingLeave
	if b.parties < 0 {
		b.parties = 0
	}
	b.pendingJoin = 0
	b.pendingLeave = 0
	b.arrived = 0
	b.gen = &generation{done: make(chan struct{})}
}
