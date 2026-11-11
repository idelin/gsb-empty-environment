package llhls

import (
	"container/heap"
	"sync"
	"time"
)

// Clock abstracts time so the origin can be driven deterministically in tests.
//
// A real process injects RealClock and everything behaves like the standard
// time package. Tests inject VirtualClock and advance simulated time by hand,
// which fires the same timers that blocking requests and encoders wait on.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer is the subset of *time.Timer used by the package.
type Timer interface {
	// C returns the channel on which the firing time is delivered.
	C() <-chan time.Time
	// Stop prevents the timer from firing. It reports whether the call stopped
	// the timer before it was due.
	Stop() bool
}

// RealClock delegates to the wall clock.
type RealClock struct{}

// NewRealClock returns a clock backed by the real time package.
func NewRealClock() RealClock { return RealClock{} }

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) NewTimer(d time.Duration) Timer { return &realTimer{t: time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (r *realTimer) C() <-chan time.Time { return r.t.C }
func (r *realTimer) Stop() bool          { return r.t.Stop() }

// vtEntry is one scheduled timer in the virtual clock heap.
type vtEntry struct {
	deadline time.Time
	id       uint64
	ch       chan time.Time
	fired    bool
}

type vtHeap []*vtEntry

func (h vtHeap) Len() int           { return len(h) }
func (h vtHeap) Less(i, j int) bool { return h[i].deadline.Before(h[j].deadline) }
func (h vtHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *vtHeap) Push(x any)        { *h = append(*h, x.(*vtEntry)) }
func (h *vtHeap) Pop() any {
	old := *h
	n := len(old)
	entry := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return entry
}

// VirtualClock is a manually advanced clock. Timers never fire on their own:
// they fire synchronously inside Advance, in deadline order. This makes
// encoder output, blocking playlist requests and their timeouts fully
// reproducible without sleeping in real time.
type VirtualClock struct {
	mu      sync.Mutex
	now     time.Time
	timers  vtHeap
	stopped map[uint64]bool
	nextID  uint64
}

// NewVirtualClock starts the simulated clock at epoch.
func NewVirtualClock() *VirtualClock {
	c := &VirtualClock{
		now:     time.Unix(0, 0).UTC(),
		stopped: make(map[uint64]bool),
	}
	heap.Init(&c.timers)
	return c
}

// Now returns the current simulated time.
func (c *VirtualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// PendingCount reports how many live (not stopped) timers are scheduled.
// Tests use it to synchronize goroutines that arm timers before calling
// Advance, eliminating scheduling races on simulated time.
func (c *VirtualClock) PendingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.timers.Len() - len(c.stopped)
}

// NewTimer schedules a timer for now+d on the virtual timeline.
func (c *VirtualClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	entry := &vtEntry{
		deadline: c.now.Add(d),
		id:       c.nextID,
		// Buffered: Advance delivers without waiting for a receiver, so
		// stopping after the deadline is still observable correctly.
		ch: make(chan time.Time, 1),
	}
	heap.Push(&c.timers, entry)
	return &virtualTimer{clock: c, entry: entry}
}

// Advance moves simulated time forward by d and fires every due timer
// (including timers whose deadline equals the new time) once, in deadline
// order. New timers scheduled by those callbacks at or before the new time
// fire during the same Advance.
func (c *VirtualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	for c.timers.Len() > 0 {
		entry := c.timers[0]
		if entry.deadline.After(c.now) {
			break
		}
		heap.Pop(&c.timers)
		if c.stopped[entry.id] {
			delete(c.stopped, entry.id)
			continue
		}
		entry.ch <- entry.deadline
		entry.fired = true
	}
	c.mu.Unlock()
}

type virtualTimer struct {
	clock *VirtualClock
	entry *vtEntry
}

func (v *virtualTimer) C() <-chan time.Time { return v.entry.ch }

func (v *virtualTimer) Stop() bool {
	v.clock.mu.Lock()
	defer v.clock.mu.Unlock()
	if v.entry.fired {
		return false
	}
	if v.clock.stopped[v.entry.id] {
		return false
	}
	v.clock.stopped[v.entry.id] = true
	return true
}
