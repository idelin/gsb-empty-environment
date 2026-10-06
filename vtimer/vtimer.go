// Package vtimer provides one-shot timers driven by an explicitly stepped
// virtual clock. No goroutines and no sleeping: time advances only through
// Clock.Advance, which moves due events into a ready queue for claiming.
//
// Linearization and generations:
//
// All operations (NewTimer, Reset, Stop, Claim, Next, Advance) serialize on
// the clock's mutex; each takes effect at that linearization point. Every
// Timer has a generation counter, incremented by Reset. Each scheduled event
// carries the generation that created it. An event is deliverable only while
// it is the timer's current generation and the timer is neither stopped nor
// already delivered for that generation. Consequently, if an old-generation
// event has already been moved into the ready queue but not yet claimed, a
// Reset invalidates it in place: it can never be delivered. Each generation
// is delivered at most once.
//
// Ordering: events are ordered by (expiry, creation sequence). Events with
// identical expiry fire in the order they were created (NewTimer / Reset
// calls), because each event receives a monotonically increasing sequence
// number at creation.
package vtimer

import (
	"container/heap"
	"sync"
	"time"
)

// event is a single scheduled firing of one generation of a Timer.
type event struct {
	expiry time.Duration // virtual time at which it becomes ready
	seq    uint64        // creation order, breaks expiry ties
	gen    uint64        // generation of the timer that created it
	timer  *Timer
}

// eventHeap is a min-heap ordered by (expiry, seq).
type eventHeap []*event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].expiry != h[j].expiry {
		return h[i].expiry < h[j].expiry
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) {
	*h = append(*h, x.(*event))
}
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	ev := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return ev
}

// Clock is a virtual clock. The zero value is not usable; use NewClock.
type Clock struct {
	mu    sync.Mutex
	now   time.Duration
	seq   uint64
	pend  eventHeap // scheduled events with expiry > now
	ready []*event  // due events (expiry <= now), in (expiry, seq) order
}

// NewClock returns a virtual clock at time zero.
func NewClock() *Clock { return &Clock{} }

// Now reports the current virtual time.
func (c *Clock) Now() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance steps the clock forward by d, moving every event with
// expiry <= new-now from the pending heap into the ready queue, preserving
// (expiry, seq) order. Events already in the ready queue stay queued.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += d
	for len(c.pend) > 0 && c.pend[0].expiry <= c.now {
		ev := heap.Pop(&c.pend).(*event)
		c.ready = append(c.ready, ev)
	}
}

// NewTimer returns a one-shot Timer that becomes ready d from now.
func (c *Clock) NewTimer(d time.Duration) *Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &Timer{clock: c}
	c.scheduleLocked(t, d)
	return t
}

// scheduleLocked creates the event for the timer's current generation.
// Caller must hold c.mu.
func (c *Clock) scheduleLocked(t *Timer, d time.Duration) {
	c.seq++
	heap.Push(&c.pend, &event{
		expiry: c.now + d,
		seq:    c.seq,
		gen:    t.gen,
		timer:  t,
	})
	t.active = true
	t.stopped = false
	t.delivered = false
}

// Next claims the next deliverable event from the ready queue in
// (expiry, seq) order, skipping stale entries (superseded generations,
// stopped timers, already-delivered generations). It returns the owning
// timer and the event's expiry. ok is false when nothing deliverable
// remains queued.
func (c *Clock) Next() (tm *Timer, expiry time.Duration, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.ready) > 0 {
		ev := c.ready[0]
		c.ready = c.ready[1:]
		t := ev.timer
		if t.stopped || t.delivered || ev.gen != t.gen {
			continue // stale: invalidated by Reset/Stop or already delivered
		}
		t.delivered = true
		t.active = false
		return t, ev.expiry, true
	}
	return nil, 0, false
}

// Timer is a one-shot virtual timer. Use Reset to re-arm and Stop to cancel.
type Timer struct {
	clock     *Clock
	gen       uint64 // current generation; bumped by Reset
	active    bool   // current generation has a scheduled, undelivered event
	stopped   bool   // Stop linearized after the last Reset
	delivered bool   // current generation already claimed
}

// Reset re-arms the timer to become ready d from now, starting a new
// generation. Any event from a previous generation — including one already
// sitting in the ready queue — is invalidated and will never be delivered.
// It reports whether the timer was active (armed and not yet delivered or
// stopped) at the linearization point.
func (t *Timer) Reset(d time.Duration) bool {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()
	wasActive := t.active && !t.stopped && !t.delivered
	t.gen++
	c.scheduleLocked(t, d)
	return wasActive
}

// Stop cancels the timer's current generation. A queued-but-unclaimed event
// becomes undeliverable. It reports whether the timer was active at the
// linearization point. The timer may be re-armed with Reset.
func (t *Timer) Stop() bool {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()
	wasActive := t.active && !t.stopped && !t.delivered
	t.stopped = true
	t.active = false
	return wasActive
}

// Claim delivers this timer's due event if its current generation has one
// sitting in the ready queue. Each generation yields exactly one successful
// Claim; afterwards Claim returns false until the next Reset.
func (t *Timer) Claim() (expiry time.Duration, ok bool) {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.stopped || t.delivered || !t.active {
		return 0, false
	}
	for i, ev := range c.ready {
		if ev.timer == t && ev.gen == t.gen {
			c.ready = append(c.ready[:i], c.ready[i+1:]...)
			t.delivered = true
			t.active = false
			return ev.expiry, true
		}
	}
	return 0, false
}

// Gen returns the timer's current generation (number of Resets so far).
func (t *Timer) Gen() uint64 {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()
	return t.gen
}
