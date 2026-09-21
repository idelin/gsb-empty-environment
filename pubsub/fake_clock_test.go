package pubsub

import (
	"sync"
	"time"
)

// fakeClock 是确定性假时钟：定时器不随墙钟触发，只由测试调用 Advance 推进。
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*fakeTimer]struct{}
}

type fakeTimer struct {
	clock   *fakeClock
	mu      sync.Mutex
	ch      chan time.Time
	fireAt  time.Time
	fired   bool
	stopped bool
}

func newFakeClock() *fakeClock {
	return &fakeClock{
		now:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		timers: make(map[*fakeTimer]struct{}),
	}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(d time.Duration) Timer {
	t := &fakeTimer{
		clock: c,
		ch:    make(chan time.Time, 1),
	}
	c.mu.Lock()
	t.fireAt = c.now.Add(d)
	c.timers[t] = struct{}{}
	c.mu.Unlock()
	return t
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	t.mu.Lock()
	defer t.mu.Unlock()
	wasActive := !t.fired && !t.stopped
	t.stopped = true
	delete(t.clock.timers, t)
	return wasActive
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	t.mu.Lock()
	defer t.mu.Unlock()
	wasActive := !t.fired && !t.stopped
	t.stopped = false
	t.fired = false
	t.fireAt = t.clock.now.Add(d)
	t.clock.timers[t] = struct{}{}
	return wasActive
}

// Advance 将假时钟向前推进 d，触发所有到期定时器。
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	due := make([]*fakeTimer, 0)
	for t := range c.timers {
		t.mu.Lock()
		if !t.fired && !t.stopped && !t.fireAt.After(c.now) {
			due = append(due, t)
		}
		t.mu.Unlock()
	}
	for _, t := range due {
		t.mu.Lock()
		if t.fired || t.stopped {
			t.mu.Unlock()
			continue
		}
		t.fired = true
		delete(c.timers, t)
		fireAt := t.fireAt
		t.mu.Unlock()
		select {
		case t.ch <- fireAt:
		default:
		}
	}
	c.mu.Unlock()
}
