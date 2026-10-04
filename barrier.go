// Package barrier provides a reusable phased barrier.
//
// A Barrier lets a dynamic set of participants synchronize in rounds.
// A round settles when either all expected participants have arrived
// (ReasonTripped) or Cancel is called (ReasonCancelled). Settling wakes
// every waiter of that round and opens the next one.
//
// Participants may deregister by passing exitNext=true to Arrive: the
// exit takes effect at the next round boundary. New participants register
// with Join and become active from the next round. Pending joins and
// exits are applied exactly once, at the moment a round settles, so a
// last arrival, an exit and a cancellation racing each other can never
// decrement the participant count twice.
package barrier

import "sync"

// Reason describes why a round settled.
type Reason int

const (
	// ReasonTripped means all expected participants arrived.
	ReasonTripped Reason = iota
	// ReasonCancelled means the round was aborted by Cancel.
	ReasonCancelled
)

func (r Reason) String() string {
	switch r {
	case ReasonTripped:
		return "tripped"
	case ReasonCancelled:
		return "cancelled"
	}
	return "unknown"
}

// generation is one round of the barrier. Its result fields are written
// under the barrier mutex before done is closed, so they are safe to read
// by any goroutine that observes <-done.
type generation struct {
	done   chan struct{}
	round  uint64
	reason Reason
}

// Barrier is a reusable phased barrier with dynamic membership.
// The zero value is not usable; construct with New.
type Barrier struct {
	mu      sync.Mutex
	gen     *generation
	round   uint64 // current round
	parties int    // expected arrivals for the current round
	arrived int    // arrivals so far in the current round
	joins   int    // pending joins, applied at the next settlement
	exits   int    // pending exits, applied at the next settlement
}

// New returns a Barrier expecting parties arrivals per round.
func New(parties int) *Barrier {
	if parties < 0 {
		panic("barrier: negative parties")
	}
	return &Barrier{
		gen:     &generation{done: make(chan struct{})},
		parties: parties,
	}
}

// Arrive records the caller's arrival at the current round and blocks
// until the round settles. It returns the settled round number and the
// reason the round settled.
//
// If exitNext is true, the caller is deregistered effective next round:
// this Arrive is its last one. The exit is accounted exactly once, when
// the round settles, regardless of whether the round trips or is
// cancelled. A participant must not call Arrive again after passing
// exitNext=true.
func (b *Barrier) Arrive(exitNext bool) (round uint64, reason Reason) {
	b.mu.Lock()
	if exitNext {
		b.exits++
	}
	b.arrived++
	if b.arrived >= b.parties {
		gen := b.settleLocked(ReasonTripped)
		b.mu.Unlock()
		return gen.round, gen.reason
	}
	gen := b.gen
	b.mu.Unlock()
	<-gen.done
	return gen.round, gen.reason
}

// Join registers a new participant effective from the next round and
// returns the round in which it becomes active. Join blocks until the
// current round settles, guaranteeing the new participant can never
// arrive in a round that was already in progress. The join is applied
// even if the current round is cancelled.
//
// Join must not be called by a goroutine that still owes an arrival to
// the current round, or it may deadlock waiting for a settlement that
// depends on itself.
func (b *Barrier) Join() (activeRound uint64) {
	b.mu.Lock()
	b.joins++
	gen := b.gen
	b.mu.Unlock()
	<-gen.done
	return gen.round + 1
}

// Cancel aborts the current round, waking all of its waiters with
// ReasonCancelled, and opens the next round. Pending joins and exits
// declared for the cancelled round are applied exactly once. Cancel
// returns the number of the round it cancelled.
func (b *Barrier) Cancel() (cancelledRound uint64) {
	b.mu.Lock()
	gen := b.settleLocked(ReasonCancelled)
	b.mu.Unlock()
	return gen.round
}

// settleLocked settles the current round with the given reason, wakes
// every waiter, applies pending joins and exits exactly once, and opens
// the next round. It must be called with b.mu held.
func (b *Barrier) settleLocked(reason Reason) *generation {
	old := b.gen
	old.round = b.round
	old.reason = reason
	close(old.done)

	b.parties += b.joins - b.exits
	b.joins = 0
	b.exits = 0
	b.arrived = 0
	b.round++
	b.gen = &generation{done: make(chan struct{})}
	return old
}

// Snapshot returns the current round, the number of expected arrivals
// (parties) and the arrivals recorded so far in the current round.
// It is intended for monitoring and tests.
func (b *Barrier) Snapshot() (round uint64, parties, arrived int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.round, b.parties, b.arrived
}
