package netsec

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced clock for deterministic token-bucket tests.
// Timers fire when advance moves the fake time past their deadline.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	deadline time.Time
	ch       chan time.Time
	stopped  bool
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_000_000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(d time.Duration) (<-chan time.Time, func() bool) {
	c.mu.Lock()
	t := &fakeTimer{
		deadline: c.now.Add(d),
		ch:       make(chan time.Time, 1),
	}
	c.timers = append(c.timers, t)
	c.mu.Unlock()

	stop := func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		if t.stopped {
			return false
		}
		t.stopped = true
		return true
	}
	return t.ch, stop
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	due := make([]*fakeTimer, 0)
	kept := c.timers[:0]
	for _, t := range c.timers {
		if t.stopped {
			continue
		}
		if !t.deadline.After(c.now) {
			due = append(due, t)
		} else {
			kept = append(kept, t)
		}
	}
	c.timers = kept
	c.mu.Unlock()

	for _, t := range due {
		t.ch <- t.deadline
	}
}

func TestTokenBucketBurstAndRefill(t *testing.T) {
	clock := newFakeClock()
	// 10 events/sec, burst 2: two immediate tokens, then one per 100ms.
	tb := NewTokenBucket(10, 2, clock)

	if !tb.Allow() || !tb.Allow() {
		t.Fatal("expected both burst tokens to be available")
	}
	if tb.Allow() {
		t.Fatal("third Allow must block until refill")
	}

	clock.advance(50 * time.Millisecond)
	if tb.Allow() {
		t.Fatal("half a refill interval must not yield a token")
	}
	clock.advance(60 * time.Millisecond)
	if !tb.Allow() {
		t.Fatal("expected one refilled token after 110ms")
	}
	if tb.Allow() {
		t.Fatal("refill must not exceed one token")
	}

	clock.advance(time.Second)
	if !tb.Allow() || !tb.Allow() {
		t.Fatal("refill is capped at burst size")
	}
	if tb.Allow() {
		t.Fatal("refill must never accumulate beyond burst")
	}
}

func TestTokenBucketWaitHonorsContext(t *testing.T) {
	tb := NewTokenBucket(10, 1, nil)
	ctx, cancel := context.WithCancel(context.Background())

	if err := tb.Wait(ctx); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	cancel()
	if err := tb.Wait(ctx); err != context.Canceled {
		t.Fatalf("Wait after cancel = %v, want context.Canceled", err)
	}
}

func TestUnlimitedLimiterNeverBlocks(t *testing.T) {
	tb := NewTokenBucket(0, 0, newFakeClock())
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		if err := tb.Wait(ctx); err != nil {
			t.Fatalf("unlimited Wait %d: %v", i, err)
		}
	}
}
