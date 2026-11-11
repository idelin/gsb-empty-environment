package relay

import (
	"container/heap"
	"sync"
	"time"
)

// Timer is a cancellable scheduled callback.
type Timer interface {
	Stop() bool
}

// Clock abstracts time so the pool can be driven deterministically in tests.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, fn func()) Timer
}

// RealClock implements Clock with the standard library.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) AfterFunc(d time.Duration, fn func()) Timer {
	return time.AfterFunc(d, fn)
}

type vEvent struct {
	deadline time.Time
	seq      int
	fn       func()
	canceled bool
	index    int
}

func (e *vEvent) Stop() bool {
	if e.canceled {
		return false
	}
	e.canceled = true
	return true
}

type eventHeap []*vEvent

func (h eventHeap) Len() int { return len(h) }

func (h eventHeap) Less(i, j int) bool {
	if h[i].deadline.Equal(h[j].deadline) {
		return h[i].seq < h[j].seq
	}
	return h[i].deadline.Before(h[j].deadline)
}

func (h eventHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *eventHeap) Push(x any) {
	e := x.(*vEvent)
	e.index = len(*h)
	*h = append(*h, e)
}

func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.index = -1
	*h = old[:n-1]
	return e
}

// VirtualClock is a deterministic Clock for tests. Time only moves when
// Advance is called; due callbacks run in deadline order.
type VirtualClock struct {
	mu     sync.Mutex
	now    time.Time
	seq    int
	events eventHeap
}

func NewVirtualClock(start time.Time) *VirtualClock {
	return &VirtualClock{now: start}
}

func (c *VirtualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *VirtualClock) AfterFunc(d time.Duration, fn func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := &vEvent{deadline: c.now.Add(d), seq: c.seq, fn: fn}
	c.seq++
	heap.Push(&c.events, e)
	return e
}

// Advance moves the clock forward by d, running every callback whose
// deadline falls within the advanced window, in chronological order.
// Callbacks may schedule new events; those are also run if due.
func (c *VirtualClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	for {
		if len(c.events) == 0 || c.events[0].deadline.After(target) {
			break
		}
		e := heap.Pop(&c.events).(*vEvent)
		if e.canceled {
			continue
		}
		c.now = e.deadline
		fn := e.fn
		c.mu.Unlock()
		fn()
		c.mu.Lock()
	}
	c.now = target
	c.mu.Unlock()
}
