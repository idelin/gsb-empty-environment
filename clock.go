package objstore

import (
	"sync"
	"time"
)

// Clock abstracts time so the store can be driven deterministically in tests.
type Clock interface {
	Now() time.Time
	// AfterFunc runs f once the clock reaches now+d. Implementations must
	// guarantee f is never invoked concurrently with itself.
	AfterFunc(d time.Duration, f func())
}

// VirtualClock is a manually advanced Clock. Timers fire synchronously from
// Advance, in deadline order, so tests can reproduce exact boundary races
// between time-driven cleanup and reference-ledger operations.
type VirtualClock struct {
	mu     sync.Mutex
	now    time.Time
	seq    uint64
	timers []timerEntry
}

type timerEntry struct {
	deadline time.Time
	seq      uint64
	f        func()
}

// NewVirtualClock returns a VirtualClock starting at start.
func NewVirtualClock(start time.Time) *VirtualClock {
	return &VirtualClock{now: start}
}

// Now returns the current virtual time.
func (c *VirtualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// AfterFunc schedules f to run when the clock reaches now+d.
func (c *VirtualClock) AfterFunc(d time.Duration, f func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	c.timers = append(c.timers, timerEntry{deadline: c.now.Add(d), seq: c.seq, f: f})
}

// Advance moves the clock forward by d, firing every timer whose deadline
// falls within the advanced interval. While a timer callback runs, Now
// reports that timer's deadline, so a callback observing Now sees a time
// that is never before its own deadline. Timers scheduled by callbacks with
// a deadline at or before the target also fire within the same Advance.
func (c *VirtualClock) Advance(d time.Duration) {
	if d < 0 {
		panic("objstore: virtual clock cannot move backwards")
	}
	c.mu.Lock()
	target := c.now.Add(d)
	for {
		idx := -1
		for i := range c.timers {
			if c.timers[i].deadline.After(target) {
				continue
			}
			if idx == -1 ||
				c.timers[i].deadline.Before(c.timers[idx].deadline) ||
				(c.timers[i].deadline.Equal(c.timers[idx].deadline) && c.timers[i].seq < c.timers[idx].seq) {
				idx = i
			}
		}
		if idx == -1 {
			break
		}
		t := c.timers[idx]
		c.timers = append(c.timers[:idx], c.timers[idx+1:]...)
		if t.deadline.After(c.now) {
			c.now = t.deadline
		}
		c.mu.Unlock()
		t.f()
		c.mu.Lock()
	}
	c.now = target
	c.mu.Unlock()
}

// Set jumps the clock to t without firing any timers. It simulates lost
// timers (e.g. a process restart); callers are expected to run Store.Reconcile
// afterwards to sweep deletions that became overdue during the gap.
func (c *VirtualClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.Before(c.now) {
		panic("objstore: virtual clock cannot move backwards")
	}
	c.now = t
}

// Pending reports how many scheduled timers have not fired yet.
func (c *VirtualClock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) AfterFunc(d time.Duration, f func()) { time.AfterFunc(d, f) }

// NewRealClock returns a Clock backed by the wall clock, for production use.
func NewRealClock() Clock { return realClock{} }
