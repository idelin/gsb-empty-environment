package netsec

import (
	"sync"
	"time"
)

// wallClock adapts the standard library time package to [Clock].
type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

func (wallClock) AfterFunc(d time.Duration, fn func()) Timer {
	return &wallTimer{t: time.AfterFunc(d, fn)}
}

type wallTimer struct{ t *time.Timer }

func (w *wallTimer) Stop() bool                 { return w.t.Stop() }
func (w *wallTimer) Reset(d time.Duration) bool { return w.t.Reset(d) }

// pendingEntry pairs a timer callback with its simulated fire time.
type pendingEntry struct {
	timer  *fakeTimer
	fireAt time.Time
}

// fakeTimer is a scheduled callback owned by [FakeClock].
type fakeTimer struct {
	clock *FakeClock
	id    uint64
	fn    func()
}

// Stop removes the timer if it has not fired yet. It reports whether the timer
// was still pending, matching time.Timer semantics closely enough for tests.
func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if _, ok := t.clock.pending[t.id]; !ok {
		return false
	}
	delete(t.clock.pending, t.id)
	return true
}

// Reset stops the timer and reschedules it to fire d after the fake clock's
// current time, mirroring time.Timer.Reset. A new internal slot is used so any
// stale schedule referencing the old slot cannot fire this callback.
func (t *fakeTimer) Reset(d time.Duration) bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	_, wasActive := t.clock.pending[t.id]
	delete(t.clock.pending, t.id)
	t.clock.nextID++
	t.id = t.clock.nextID
	t.clock.pending[t.id] = pendingEntry{timer: t, fireAt: t.clock.now.Add(d)}
	return wasActive
}

// FakeClock is a manually advanced [Clock]. Timers never fire on their own;
// they run when [FakeClock.Advance] reaches their deadline. The zero value is
// not usable; construct one with [NewFakeClock].
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	nextID  uint64
	pending map[uint64]pendingEntry
}

// NewFakeClock returns a fake clock starting at start (use time.Unix(0, 0) or
// any fixed instant).
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start, pending: make(map[uint64]pendingEntry)}
}

// Now reports the simulated current time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// AfterFunc schedules fn to run once d simulated time units in the future.
func (c *FakeClock) AfterFunc(d time.Duration, fn func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	t := &fakeTimer{clock: c, id: c.nextID, fn: fn}
	c.pending[t.id] = pendingEntry{timer: t, fireAt: c.now.Add(d)}
	return t
}

// Advance moves the simulated clock forward by d and synchronously runs every
// timer whose deadline falls within the interval, in deadline order. Callbacks
// run without the clock lock held so they may safely call back into the clock
// (for example to schedule a retry timer). Timers scheduled during Advance for
// a deadline still inside the same interval are fired in the same call.
func (c *FakeClock) Advance(d time.Duration) {
	end := c.peekNow().Add(d)
	for {
		entry := c.takeDue(end)
		if entry == nil {
			return
		}
		c.mu.Lock()
		if entry.fireAt.After(c.now) {
			c.now = entry.fireAt
		}
		c.mu.Unlock()
		entry.timer.fn()
	}
}

func (c *FakeClock) peekNow() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// takeDue removes and returns the earliest pending entry due by end, or nil.
func (c *FakeClock) takeDue(end time.Time) *pendingEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	var chosen *pendingEntry
	for _, e := range c.pending {
		if e.fireAt.After(end) {
			continue
		}
		if chosen == nil || e.fireAt.Before(chosen.fireAt) {
			entry := e
			chosen = &entry
		}
	}
	if chosen == nil {
		// Nothing left to fire within the interval: commit the full jump.
		c.now = end
		return nil
	}
	delete(c.pending, chosen.timer.id)
	return chosen
}

// Pending reports how many timers are currently scheduled.
func (c *FakeClock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

// earliestAt returns the next scheduled fire time, for tests/debugging.
func (c *FakeClock) earliestAt() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var at time.Time
	found := false
	for _, p := range c.pending {
		if !found || p.fireAt.Before(at) {
			at, found = p.fireAt, true
		}
	}
	return at, found
}
