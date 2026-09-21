package scheduler

import (
	"context"
	"sync"
	"time"
)

// fakeClock is a manually advanced Clock for tests.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters map[*waiter]struct{}
}

type waiter struct {
	target time.Time
	fired  chan struct{}
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start, waiters: map[*waiter]struct{}{}}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward, firing waiters whose target passed.
func (c *fakeClock) Advance(d time.Duration) { c.Set(c.Now().Add(d)) }

// Set moves the clock to t, including backwards (clock rewind).
func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
	for w := range c.waiters {
		if !c.now.Before(w.target) {
			close(w.fired)
			delete(c.waiters, w)
		}
	}
}

func (c *fakeClock) WaitUntil(ctx context.Context, t time.Time) error {
	c.mu.Lock()
	if !c.now.Before(t) {
		c.mu.Unlock()
		return nil
	}
	w := &waiter{target: t, fired: make(chan struct{})}
	c.waiters[w] = struct{}{}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.waiters, w)
		c.mu.Unlock()
		return ctx.Err()
	case <-w.fired:
		return nil
	}
}

func (c *fakeClock) pendingWaiters() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters)
}
