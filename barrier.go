// Package rendezvous implements a multi-party, round-based rendezvous
// (a cyclic barrier) with dynamic membership and failure propagation.
//
// A Barrier synchronizes a group of participants into rounds: a round
// completes only when every expected participant has arrived, at which
// point all of them are released simultaneously and the next round
// begins. Participants may leave at any time and new participants may
// join starting from the next round. A failure injected into a round is
// propagated to every participant of that round.
//
// Arrive returns exactly at round boundaries. Because a participant's
// Arrive never returns before its round rolls over, a follow-up arrival
// can never be miscounted into a stale round, and a late arrival can
// never leak into a fresh one.
package rendezvous

import (
	"errors"
	"sync"
)

// ErrBroken is returned by Arrive when a round is broken without an
// explicit cause.
var ErrBroken = errors.New("rendezvous: round broken")

// Barrier is a round-based rendezvous point for a dynamic set of
// participants. The zero value is not usable; use New.
type Barrier struct {
	mu           sync.Mutex
	cond         sync.Cond
	parties      int    // participants expected in the current round
	arrived      int    // arrivals observed in the current round
	round        uint64 // generation counter, bumped on every release
	brokenRound  int64  // round that failed, -1 if none
	err          error  // failure of brokenRound, propagated to that whole round
	pendingJoins int    // members taking effect at the next round boundary
}

// New returns a Barrier expecting parties arrivals per round.
func New(parties int) *Barrier {
	if parties < 1 {
		panic("rendezvous: parties must be >= 1")
	}
	b := &Barrier{parties: parties, brokenRound: -1}
	b.cond.L = &b.mu
	return b
}

// Arrive blocks until every expected participant of the current round
// has arrived. It returns nil for a successful round, or the round's
// error to every participant of a broken round. Arrivals are tagged
// with the round they occurred in, so a late arrival belonging to a
// previous round can never leak into a new one.
func (b *Barrier) Arrive() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	round := b.round
	b.arrived++
	if b.arrived >= b.parties {
		// Last expected arrival completes the round. A broken round
		// still reports its error, then state resets so the next
		// round starts clean.
		err := b.roundErrLocked(round)
		b.releaseLocked()
		return err
	}
	for b.round == round {
		b.cond.Wait()
	}
	return b.roundErrLocked(round)
}

// Run executes task and then arrives at the barrier. A nil task is a
// plain arrival. If task returns an error, the current round is broken
// with that error, the error is propagated to every participant of the
// round, and Run returns it.
func (b *Barrier) Run(task func() error) error {
	if task != nil {
		if err := task(); err != nil {
			b.Break(err)
			// Count this participant as arrived so the broken round
			// can still complete and roll over; nobody waits forever.
			b.Arrive()
			return err
		}
	}
	return b.Arrive()
}

// Break fails the current round with err. Every participant of this
// round observes the error from Arrive once the round completes. The
// round completes when all remaining expected participants have
// arrived (Leave may reduce that count); the next round starts clean.
// Breaking an already broken round is a no-op.
func (b *Barrier) Break(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.brokenRound == int64(b.round) {
		return
	}
	b.brokenRound = int64(b.round)
	b.err = err
	if b.err == nil {
		b.err = ErrBroken
	}
}

// Join registers the caller as a participant starting from the next
// round and blocks until that round begins. Joining never affects the
// participant count of the round currently in progress.
func (b *Barrier) Join() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pendingJoins++
	round := b.round
	for b.round == round {
		b.cond.Wait()
	}
}

// Leave removes one participant from the barrier, effective
// immediately. If the remaining arrivals already satisfy the reduced
// count, the current round is released at once; otherwise the round
// simply expects one fewer arrival, so nobody waits for the departed
// participant.
func (b *Barrier) Leave() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.parties > 0 {
		b.parties--
	}
	if b.arrived >= b.parties {
		b.releaseLocked()
	}
}

// Parties reports the number of participants expected in the current
// round.
func (b *Barrier) Parties() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.parties
}

// Round reports the current round (generation) number, starting at 0.
func (b *Barrier) Round() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.round
}

// roundErrLocked returns the error of the caller's round if that round
// was broken, and nil otherwise. The broken round is tracked by number,
// so the error survives the round boundary until every participant of
// the broken round has collected it.
func (b *Barrier) roundErrLocked(round uint64) error {
	if b.brokenRound == int64(round) {
		return b.err
	}
	return nil
}

// releaseLocked ends the current round: every waiter is woken, the
// round counter advances, per-round state is reset, and pending joins
// take effect. brokenRound intentionally survives: it refers to the
// previous round and lets its waiters still collect the error.
func (b *Barrier) releaseLocked() {
	b.round++
	b.arrived = 0
	b.parties += b.pendingJoins
	b.pendingJoins = 0
	b.cond.Broadcast()
}
