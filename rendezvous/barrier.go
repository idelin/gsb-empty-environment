// Package rendezvous implements a cyclic barrier (round-based rendezvous)
// for a dynamic set of participants.
//
// Participants arrive each round with Arrive and are released together once
// the expected number of arrivals is reached. Participants may leave at any
// time (Leave, or cancelling the context passed to Arrive) and new
// participants may be added with Add; additions take effect on the next
// round. If the injected round action fails, or Fail is called, the error is
// propagated to every waiter of the current round.
package rendezvous

import (
	"context"
	"sync"
)

// round is the per-generation state. err is written before ch is closed, so
// it is safe to read err after receiving from ch.
type round struct {
	ch         chan struct{}
	err        error
	completing bool
}

// Barrier is a cyclic barrier with a dynamic participant count.
type Barrier struct {
	mu      sync.Mutex
	action  func(context.Context) error
	parties int // arrivals expected in the current round
	arrived int // arrivals counted in the current round
	pending int // joins applied when the current round completes
	cur     *round
	gen     uint64 // number of completed rounds
}

// New returns a Barrier expecting parties arrivals per round. action is
// injected by the caller and runs exactly once per round (by the goroutine
// whose arrival or departure completes the round) before waiters are
// released; a non-nil error returned by action fails the round and is
// delivered to all of its waiters. action may be nil.
func New(parties int, action func(context.Context) error) *Barrier {
	if parties < 1 {
		panic("rendezvous: parties must be >= 1")
	}
	return &Barrier{
		action:  action,
		parties: parties,
		cur:     &round{ch: make(chan struct{})},
	}
}

// Arrive announces one arrival in the current round and blocks until the
// round completes. It returns the round's error (nil on success), or
// ctx.Err() if ctx is cancelled first.
//
// Cancelling ctx means the caller leaves the barrier: its expected count is
// decremented for this and future rounds, and if the remaining arrivals
// already satisfy the (reduced) requirement the round is released.
//
// An arrival that reaches the barrier while the previous round is still
// finishing waits for the next round and is never counted toward the
// finishing one.
func (b *Barrier) Arrive(ctx context.Context) error {
	for {
		b.mu.Lock()
		r := b.cur
		if r.completing {
			ch := r.ch
			b.mu.Unlock()
			select {
			case <-ch:
				continue
			case <-ctx.Done():
				b.Leave()
				return ctx.Err()
			}
		}
		b.arrived++
		trigger := b.arrived == b.parties
		if trigger {
			r.completing = true
		}
		ch := r.ch
		b.mu.Unlock()

		if trigger {
			b.runAction(r, ctx)
		}

		select {
		case <-ch:
			return r.err
		case <-ctx.Done():
			b.mu.Lock()
			if b.cur == r && !r.completing {
				b.arrived--
			}
			t := b.exitLocked()
			b.mu.Unlock()
			if t != nil {
				b.runAction(t, context.Background())
			}
			return ctx.Err()
		}
	}
}

// Leave removes one participant that is not currently waiting in Arrive. If
// the departure makes the remaining arrivals satisfy the requirement, the
// current round is released. It never blocks.
func (b *Barrier) Leave() {
	b.mu.Lock()
	t := b.exitLocked()
	b.mu.Unlock()
	if t != nil {
		b.runAction(t, context.Background())
	}
}

// Add schedules n additional participants to join starting from the next
// round; the current round's expectation is unchanged. If the barrier has
// been drained to zero participants, they take effect immediately.
func (b *Barrier) Add(n int) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	if b.parties == 0 {
		b.parties += n
	} else {
		b.pending += n
	}
	b.mu.Unlock()
}

// Fail aborts the current round: every waiter is released with err. It
// reports whether the call won the race to finish the round; a round that is
// already completing is left untouched.
func (b *Barrier) Fail(err error) bool {
	b.mu.Lock()
	r := b.cur
	if r.completing {
		b.mu.Unlock()
		return false
	}
	r.completing = true
	b.completeLocked(r, err)
	b.mu.Unlock()
	return true
}

// Parties returns the number of arrivals expected in the current round.
func (b *Barrier) Parties() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.parties
}

// Generation returns the number of completed rounds.
func (b *Barrier) Generation() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.gen
}

// exitLocked removes one expected participant. If the remaining arrivals now
// satisfy the requirement it marks the round completing and returns it so
// the caller can run the round action outside the lock.
func (b *Barrier) exitLocked() *round {
	if b.parties > 0 {
		b.parties--
	}
	r := b.cur
	if !r.completing && b.parties > 0 && b.arrived == b.parties {
		r.completing = true
		return r
	}
	return nil
}

// runAction executes the round action and completes the round with its
// result. The caller must have marked r completing while holding the lock,
// which guarantees no other goroutine completes r concurrently.
func (b *Barrier) runAction(r *round, ctx context.Context) {
	var err error
	if b.action != nil {
		err = b.action(ctx)
	}
	b.mu.Lock()
	b.completeLocked(r, err)
	b.mu.Unlock()
}

// completeLocked publishes the round result, wakes its waiters, and resets
// the state so no state leaks into the next round.
func (b *Barrier) completeLocked(r *round, err error) {
	r.err = err
	close(r.ch)
	b.cur = &round{ch: make(chan struct{})}
	b.arrived = 0
	b.parties += b.pending
	b.pending = 0
	b.gen++
}

// Arrived returns the number of arrivals counted in the current round.
func (b *Barrier) Arrived() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.arrived
}
