// Package vtimer provides one-shot virtual timers driven by an explicitly
// stepped clock. No goroutines and no sleeping: time advances only when
// Clock.Advance is called, and expired timers are delivered by Clock.Next.
//
// Linearization points (all operations take the clock's mutex):
//   - NewTimer / Reset / Stop linearize at lock acquisition; each bumps the
//     timer's generation, so any event of an older generation is invalid from
//     that instant on — even if it already sits in the ready queue.
//   - Advance linearizes expiry: pending entries with deadline <= now move to
//     the ready queue in (deadline, creationSeq) order.
//   - Next linearizes delivery: it pops the ready queue, skips stale
//     generations, and delivers each live event exactly once.
package vtimer

import (
	"container/heap"
	"sync"
	"time"
)

// Event is a single firing of a one-shot timer, claimed from the ready queue.
type Event struct {
	Timer    *Timer    // the timer that fired
	Deadline time.Time // the deadline that was reached
	Gen      uint64    // the generation that was delivered
}

// entry is one scheduled firing of a timer, identified by (timer, gen).
type entry struct {
	deadline time.Time
	seq      uint64 // creation sequence of the owning timer (stable across resets)
	gen      uint64 // generation this entry belongs to
	timer    *Timer
	stale    bool // superseded by Reset/Stop; lazily discarded
}

// pendingHeap orders scheduled entries by (deadline, creationSeq), so timers
// expiring at the same instant fire in creation order.
type pendingHeap []*entry

func (h pendingHeap) Len() int { return len(h) }

func (h pendingHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if !a.deadline.Equal(b.deadline) {
		return a.deadline.Before(b.deadline)
	}
	return a.seq < b.seq
}

func (h pendingHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *pendingHeap) Push(x interface{}) { *h = append(*h, x.(*entry)) }

func (h *pendingHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return e
}

// Clock is a manually stepped virtual clock that owns a set of timers.
type Clock struct {
	mu      sync.Mutex
	now     time.Time
	seq     uint64      // timer creation counter
	pending pendingHeap // armed entries, may contain stale ones
	ready   []*entry    // expired entries awaiting claim, FIFO
}

// NewClock returns a virtual clock positioned at start.
func NewClock(start time.Time) *Clock {
	return &Clock{now: start}
}

// Now reports the current virtual time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the virtual clock forward by d and expires every timer whose
// deadline falls at or before the new time. Expired events become claimable
// via Next, ordered by (deadline, creation sequence). It returns the new time.
func (c *Clock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for len(c.pending) > 0 && !c.pending[0].deadline.After(c.now) {
		e := heap.Pop(&c.pending).(*entry)
		if e.stale || e.gen != e.timer.gen {
			continue // superseded before expiry: never becomes ready
		}
		c.ready = append(c.ready, e)
	}
	return c.now
}

// Next claims the next live event from the ready queue. Events whose
// generation was invalidated by Reset or Stop after they became ready are
// discarded here, at the delivery linearization point. The second return
// value is false when no live event is waiting.
func (c *Clock) Next() (Event, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.ready) > 0 {
		e := c.ready[0]
		c.ready = c.ready[1:]
		t := e.timer
		if e.stale || e.gen != t.gen || !t.active {
			continue // invalidated after enqueue: must not be delivered
		}
		t.active = false // one-shot: this generation is consumed
		if t.ent == e {
			t.ent = nil
		}
		return Event{Timer: t, Deadline: e.deadline, Gen: e.gen}, true
	}
	return Event{}, false
}

// NewTimer arms a one-shot timer that fires d after the current virtual time.
func (c *Clock) NewTimer(d time.Duration) *Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	t := &Timer{clock: c, seq: c.seq, active: true}
	t.ent = c.scheduleLocked(t, d)
	return t
}

func (c *Clock) scheduleLocked(t *Timer, d time.Duration) *entry {
	e := &entry{deadline: c.now.Add(d), seq: t.seq, gen: t.gen, timer: t}
	heap.Push(&c.pending, e)
	return e
}

// Timer is a one-shot virtual timer. It keeps its creation sequence number
// across resets, so after a Reset it still ties with equal deadlines in
// original creation order.
type Timer struct {
	clock  *Clock
	seq    uint64 // creation sequence, assigned once at NewTimer
	gen    uint64 // bumped by Reset and Stop: the valid generation
	active bool   // false after Stop or after the event is claimed
	ent    *entry // latest scheduled entry (pending or ready), nil if none
}

// Reset re-arms the timer to fire d after the current virtual time. Any event
// of a previous generation — including one already sitting in the ready queue
// — is invalidated at this linearization point and will never be delivered.
// It reports whether the timer was still active (armed and not yet claimed).
func (t *Timer) Reset(d time.Duration) bool {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()
	wasActive := t.active
	t.gen++
	if t.ent != nil {
		t.ent.stale = true
		t.ent = nil
	}
	t.active = true
	t.ent = c.scheduleLocked(t, d)
	return wasActive
}

// Stop disarms the timer and invalidates any event of the current generation,
// including one already waiting in the ready queue. It reports whether the
// call prevented a delivery, i.e. whether the timer was still active.
func (t *Timer) Stop() bool {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()
	if !t.active {
		return false
	}
	t.active = false
	t.gen++
	if t.ent != nil {
		t.ent.stale = true
		t.ent = nil
	}
	return true
}

// Gen returns the timer's current generation. Exported for observability and
// testing; Event.Gen records the generation that was actually delivered.
func (t *Timer) Gen() uint64 {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	return t.gen
}
