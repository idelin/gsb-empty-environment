package swrcache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestExpiryAndStaleWhileRevalidate: fresh hits serve directly; after TTL a
// caller inside its staleness window gets the old value while exactly one
// background refresh runs; after it lands, fresh value is served.
func TestExpiryAndStaleWhileRevalidate(t *testing.T) {
	clock := NewFakeClock(time.Unix(1000, 0))
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls int32

	loader := func(ctx context.Context, key string) (interface{}, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return "v1", nil
		}
		started <- struct{}{}
		<-release
		return "v2", nil
	}
	c := New(loader, Options{TTL: time.Minute}, clock)
	ctx := context.Background()

	v, err := c.Get(ctx, "k", 30*time.Second)
	if err != nil || v != "v1" {
		t.Fatalf("first Get = %v, %v", v, err)
	}

	// Expired by 1s, caller window 30s: stale served, refresh kicked.
	clock.Advance(time.Minute + time.Second)
	v, err = c.Get(ctx, "k", 30*time.Second)
	if err != nil || v != "v1" {
		t.Fatalf("stale Get = %v, %v", v, err)
	}
	<-started // background refresh in flight

	// Concurrent arrivals during the refresh still get the stale value
	// and do not start a second refresh.
	for i := 0; i < 5; i++ {
		v, err = c.Get(ctx, "k", 30*time.Second)
		if err != nil || v != "v1" {
			t.Fatalf("adjacent stale Get = %v, %v", v, err)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("loader calls = %d, want 2 (single refresher)", n)
	}

	close(release)
	waitFor(t, "refresh to land", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return !c.entries["k"].refreshing && c.entries["k"].value == "v2"
	})

	v, err = c.Get(ctx, "k", 30*time.Second)
	if err != nil || v != "v2" {
		t.Fatalf("post-refresh Get = %v, %v", v, err)
	}
}

// TestInvalidateDiscardsInFlightRefresh: an invalidation while a refresh is
// in flight must prevent the old load from repopulating the entry.
func TestInvalidateDiscardsInFlightRefresh(t *testing.T) {
	clock := NewFakeClock(time.Unix(1000, 0))
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	loadDone := make(chan struct{}, 1)
	var calls int32

	loader := func(ctx context.Context, key string) (interface{}, error) {
		switch atomic.AddInt32(&calls, 1) {
		case 1:
			return "v1", nil
		case 2:
			started <- struct{}{}
			<-release
			return "v2-stale-generation", nil
		default:
			return "v3", nil
		}
	}
	c := New(loader, Options{TTL: time.Minute}, clock)
	c.onLoadDone = func() { loadDone <- struct{}{} }
	ctx := context.Background()

	if v, _ := c.Get(ctx, "k", time.Minute); v != "v1" {
		t.Fatalf("first Get = %v", v)
	}
	<-loadDone

	clock.Advance(2 * time.Minute) // expired beyond any window below
	if v, _ := c.Get(ctx, "k", 10*time.Minute); v != "v1" {
		t.Fatalf("stale Get = %v", v)
	}
	<-started // old-generation refresh in flight

	c.Invalidate("k")  // must block the old refresh from reloading
	close(release)     // old load now returns v2-stale-generation
	<-loadDone         // ... and is discarded

	c.mu.Lock()
	if e := c.entries["k"]; e != nil {
		c.mu.Unlock()
		t.Fatalf("entry repopulated after invalidation: %+v", e)
	}
	c.mu.Unlock()

	// Next Get loads the new generation synchronously.
	v, err := c.Get(ctx, "k", 0)
	if err != nil || v != "v3" {
		t.Fatalf("post-invalidation Get = %v, %v", v, err)
	}
	<-loadDone
}

// TestConcurrentMissSingleFlight: many callers arriving together on a miss
// (or beyond their staleness window) share one load.
func TestConcurrentMissSingleFlight(t *testing.T) {
	clock := NewFakeClock(time.Unix(1000, 0))
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls int32

	loader := func(ctx context.Context, key string) (interface{}, error) {
		atomic.AddInt32(&calls, 1)
		started <- struct{}{}
		<-release
		return "v1", nil
	}
	c := New(loader, Options{TTL: time.Minute}, clock)
	ctx := context.Background()

	const n = 8
	var wg sync.WaitGroup
	vals := make([]interface{}, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			vals[i], errs[i] = c.Get(ctx, "k", 0)
		}(i)
	}

	<-started // exactly one load in flight
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("loader calls = %d, want 1", got)
	}
	close(release)
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil || vals[i] != "v1" {
			t.Fatalf("caller %d = %v, %v", i, vals[i], errs[i])
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("loader calls = %d, want 1", got)
	}
}

// TestFailureBackoff: a failing refresh backs off; during backoff stale
// serving continues without new attempts and out-of-window callers get
// ErrRefreshBackoff; after the backoff elapses a retry is allowed.
func TestFailureBackoff(t *testing.T) {
	clock := NewFakeClock(time.Unix(1000, 0))
	var calls int32

	loader := func(ctx context.Context, key string) (interface{}, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return "v1", nil
		}
		return nil, errBoom
	}
	c := New(loader, Options{
		TTL:            time.Minute,
		InitialBackoff: 10 * time.Second,
		MaxBackoff:     time.Minute,
	}, clock)
	ctx := context.Background()

	if v, _ := c.Get(ctx, "k", time.Minute); v != "v1" {
		t.Fatalf("first Get = %v", v)
	}

	// Expire beyond any caller window: sync refresh fails.
	clock.Advance(2 * time.Minute)
	if _, err := c.Get(ctx, "k", 0); err != errBoom {
		t.Fatalf("failing refresh err = %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("calls = %d, want 2", n)
	}

	// Inside backoff: out-of-window caller gets best-effort stale + backoff.
	v, err := c.Get(ctx, "k", 0)
	if err != ErrRefreshBackoff || v != "v1" {
		t.Fatalf("backoff Get = %v, %v", v, err)
	}
	// Inside backoff: in-window caller is served stale without a retry.
	v, err = c.Get(ctx, "k", 10*time.Minute)
	if err != nil || v != "v1" {
		t.Fatalf("stale-during-backoff Get = %v, %v", v, err)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("calls during backoff = %d, want 2", n)
	}

	// Backoff elapsed: a retry is attempted (and fails again, doubling).
	clock.Advance(10 * time.Second)
	if _, err := c.Get(ctx, "k", 0); err != errBoom {
		t.Fatalf("retry err = %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Fatalf("calls after backoff = %d, want 3", n)
	}

	// Second failure backs off for 20s; 10s later still suppressed.
	clock.Advance(10 * time.Second)
	if _, err := c.Get(ctx, "k", 0); err != ErrRefreshBackoff {
		t.Fatalf("doubled backoff err = %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Fatalf("calls in doubled backoff = %d, want 3", n)
	}
}

// TestPerCallerStalenessWindows: the same expired entry is acceptable to one
// caller and not to another; the stricter caller waits for the refresh.
func TestPerCallerStalenessWindows(t *testing.T) {
	clock := NewFakeClock(time.Unix(1000, 0))
	var calls int32
	loader := func(ctx context.Context, key string) (interface{}, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return "v1", nil
		}
		return "v2", nil
	}
	c := New(loader, Options{TTL: time.Minute}, clock)
	ctx := context.Background()

	if v, _ := c.Get(ctx, "k", time.Minute); v != "v1" {
		t.Fatalf("first Get = %v", v)
	}

	clock.Advance(time.Minute + 30*time.Second) // stale by 30s

	// Lenient caller (window 1m): stale value immediately.
	v, err := c.Get(ctx, "k", time.Minute)
	if err != nil || v != "v1" {
		t.Fatalf("lenient Get = %v, %v", v, err)
	}
	// Strict caller (window 10s): must wait for / join the refresh.
	v, err = c.Get(ctx, "k", 10*time.Second)
	if err != nil || v != "v2" {
		t.Fatalf("strict Get = %v, %v", v, err)
	}
}
