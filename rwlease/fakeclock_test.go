package rwlease

import (
	"sync"
	"time"
)

// fakeClock is a deterministic Clock for tests. After(d) registers a timer
// whose channel is closed by Advance.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
	seq    uint64
}

type fakeTimer struct {
	at    time.Time
	ch    chan time.Time
	fired bool
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(0, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.timers = append(c.timers, &fakeTimer{at: c.now.Add(d), ch: ch})
	return ch
}

// Advance moves the clock forward and fires every timer at or before the new
// instant. Each timer channel is buffered and fires at most once.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []*fakeTimer
	remaining := c.timers[:0]
	for _, t := range c.timers {
		if !t.at.After(c.now) {
			due = append(due, t)
		} else {
			remaining = append(remaining, t)
		}
	}
	c.timers = remaining
	c.mu.Unlock()
	for _, t := range due {
		if !t.fired {
			t.fired = true
			t.ch <- c.Now()
		}
	}
}
