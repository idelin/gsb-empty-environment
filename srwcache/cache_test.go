package srwcache

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

// result is one scripted loader outcome.
type result struct {
	val interface{}
	err error
}

// controllableLoader blocks each call until the test releases a result,
// letting tests place invalidation / expiry / concurrent Gets exactly
// "adjacent" to a refresh in progress.
type controllableLoader struct {
	mu      sync.Mutex
	calls   int
	entered chan int // buffered; receives the call index on each entry
	release chan result
}

func newControllableLoader() *controllableLoader {
	return &controllableLoader{
		entered: make(chan int, 64),
		release: make(chan result),
	}
}

func (l *controllableLoader) load(ctx context.Context, key string) (interface{}, error) {
	l.mu.Lock()
	l.calls++
	n := l.calls
	l.mu.Unlock()
	l.entered <- n
	r := <-l.release
	return r.val, r.err
}

func (l *controllableLoader) callCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

func waitFor(t *testing.T, what string, cond func() bool) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (c *Cache) storedValue(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	return e.value, true
}

func (c *Cache) failureCount(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failures[key]
}

var start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// TestStaleWindowsDifferPerCaller: after expiry, a caller with a wide
// staleness window gets the old value (plus a background refresh), while a
// caller with a tight window gets the refreshed value synchronously.
func TestStaleWindowsDifferPerCaller(t *testing.T) {
	clock := NewFakeClock(start)
	var mu sync.Mutex
	current := "v1"
	calls := 0
	c := New(Options{
		TTL:   10 * time.Second,
		Clock: clock,
		Loader: func(ctx context.Context, key string) (interface{}, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			return current, nil
		},
	})
	ctx := context.Background()

	if v, err := c.Get(ctx, "k", 5*time.Second); err != nil || v != "v1" {
		t.Fatalf("initial Get = %v, %v", v, err)
	}

	// Expire: age 12s, TTL 10s.
	clock.Advance(12 * time.Second)

	// Wide-window caller: stale v1 served, background refresh triggered.
	if v, err := c.Get(ctx, "k", 5*time.Second); err != nil || v != "v1" {
		t.Fatalf("stale Get = %v, %v, want stale v1", v, err)
	}
	mu.Lock()
	current = "v2"
	mu.Unlock()
	waitFor(t, "background refresh to store v2", func() bool {
		v, ok := c.storedValue("k")
		return ok && v == "v2"
	})

	// Expire again; tight-window caller must not see the stale value.
	clock.Advance(12 * time.Second)
	mu.Lock()
	current = "v3"
	mu.Unlock()
	if v, err := c.Get(ctx, "k", time.Second); err != nil || v != "v3" {
		t.Fatalf("tight-window Get = %v, %v, want fresh v3", v, err)
	}
}

// TestSingleFlightAdjacentArrivals: many Gets arriving together on an
// expired key share exactly one refresh.
func TestSingleFlightAdjacentArrivals(t *testing.T) {
	clock := NewFakeClock(start)
	loader := newControllableLoader()
	c := New(Options{TTL: 10 * time.Second, Clock: clock, Loader: loader.load})
	ctx := context.Background()

	go func() {
		if v, err := c.Get(ctx, "k", 0); err != nil || v != "v1" {
			t.Errorf("initial Get = %v, %v", v, err)
		}
	}()
	if n := <-loader.entered; n != 1 {
		t.Fatalf("loader call #%d, want 1", n)
	}
	loader.release <- result{val: "v1"}
	waitFor(t, "v1 stored", func() bool {
		v, ok := c.storedValue("k")
		return ok && v == "v1"
	})

	// Expire beyond any staleness window so Gets must refresh synchronously.
	clock.Advance(time.Hour)

	const callers = 5
	var wg sync.WaitGroup
	vals := make([]interface{}, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			vals[i], errs[i] = c.Get(ctx, "k", 0)
		}(i)
	}

	// Exactly one refresh starts despite the concurrent arrivals.
	if n := <-loader.entered; n != 2 {
		t.Fatalf("loader call #%d, want 2", n)
	}
	// Give the other goroutines a chance to run; none may start a refresh.
	time.Sleep(50 * time.Millisecond)
	if got := loader.callCount(); got != 2 {
		t.Fatalf("loader calls = %d, want 2 (single-flight violated)", got)
	}

	loader.release <- result{val: "v2"}
	wg.Wait()
	for i := 0; i < callers; i++ {
		if errs[i] != nil || vals[i] != "v2" {
			t.Fatalf("caller %d = %v, %v, want v2", i, vals[i], errs[i])
		}
	}
	if got := loader.callCount(); got != 2 {
		t.Fatalf("loader calls = %d, want 2", got)
	}
}

// TestInvalidateDiscardsInflightRefresh: an invalidation arriving while a
// refresh is in flight must prevent that refresh from re-storing its result.
func TestInvalidateDiscardsInflightRefresh(t *testing.T) {
	clock := NewFakeClock(start)
	loader := newControllableLoader()
	c := New(Options{TTL: 10 * time.Second, Clock: clock, Loader: loader.load})
	ctx := context.Background()

	go func() { c.Get(ctx, "k", 0) }()
	<-loader.entered
	loader.release <- result{val: "v1"}
	waitFor(t, "v1 stored", func() bool {
		v, ok := c.storedValue("k")
		return ok && v == "v1"
	})

	clock.Advance(time.Hour) // fully expired

	// A Get starts a refresh; while the loader is blocked, invalidate.
	resCh := make(chan interface{}, 1)
	errCh := make(chan error, 1)
	go func() {
		v, err := c.Get(ctx, "k", 0)
		resCh <- v
		errCh <- err
	}()
	if n := <-loader.entered; n != 2 {
		t.Fatalf("loader call #%d, want 2", n)
	}
	c.Invalidate("k")
	if _, ok := c.storedValue("k"); ok {
		t.Fatal("entry still present after Invalidate")
	}

	// The in-flight refresh completes with a now-stale result: discarded.
	loader.release <- result{val: "v2-stale"}
	// The waiting Get must not observe v2-stale; it re-refreshes.
	if n := <-loader.entered; n != 3 {
		t.Fatalf("loader call #%d, want 3 (discarded refresh must be retried)", n)
	}
	loader.release <- result{val: "v3"}

	if err := <-errCh; err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if v := <-resCh; v != "v3" {
		t.Fatalf("Get = %v, want v3 (invalidated refresh leaked %v)", v, "v2-stale")
	}
	if v, _ := c.storedValue("k"); v != "v3" {
		t.Fatalf("stored = %v, want v3", v)
	}
}

// TestInvalidateBlocksStaleServing: after invalidation even a caller with a
// huge staleness window must not receive the deleted value.
func TestInvalidateBlocksStaleServing(t *testing.T) {
	clock := NewFakeClock(start)
	loader := newControllableLoader()
	c := New(Options{TTL: 10 * time.Second, Clock: clock, Loader: loader.load})
	ctx := context.Background()

	go func() { c.Get(ctx, "k", 0) }()
	<-loader.entered
	loader.release <- result{val: "v1"}
	waitFor(t, "v1 stored", func() bool {
		_, ok := c.storedValue("k")
		return ok
	})

	clock.Advance(12 * time.Second) // stale but within a 1h window
	c.Invalidate("k")

	resCh := make(chan interface{}, 1)
	go func() {
		v, err := c.Get(ctx, "k", time.Hour)
		if err != nil {
			t.Errorf("Get error: %v", err)
		}
		resCh <- v
	}()
	// No stale serving: the loader must be consulted again.
	if n := <-loader.entered; n != 2 {
		t.Fatalf("loader call #%d, want 2", n)
	}
	loader.release <- result{val: "v2"}
	if v := <-resCh; v != "v2" {
		t.Fatalf("Get = %v, want v2", v)
	}
}

// TestFailureBackoff: consecutive failures suppress the loader with
// exponential backoff; a success resets it. Stale values remain servable
// during backoff.
func TestFailureBackoff(t *testing.T) {
	clock := NewFakeClock(start)
	loader := newControllableLoader()
	c := New(Options{
		TTL:         10 * time.Second,
		Clock:       clock,
		Loader:      loader.load,
		BackoffBase: time.Second,
		BackoffMax:  4 * time.Second,
	})
	ctx := context.Background()

	// Seed a value, then expire it just inside the stale window.
	go func() { c.Get(ctx, "k", 0) }()
	<-loader.entered
	loader.release <- result{val: "v1"}
	waitFor(t, "v1 stored", func() bool {
		_, ok := c.storedValue("k")
		return ok
	})
	clock.Advance(12 * time.Second)

	// Stale Get serves v1 and triggers a background refresh, which fails.
	if v, err := c.Get(ctx, "k", 5*time.Second); err != nil || v != "v1" {
		t.Fatalf("stale Get = %v, %v", v, err)
	}
	if n := <-loader.entered; n != 2 {
		t.Fatalf("loader call #%d, want 2", n)
	}
	loader.release <- result{err: errBoom}
	waitFor(t, "failure recorded", func() bool { return c.failureCount("k") == 1 })

	// During backoff the stale value is served without hitting the loader.
	if v, err := c.Get(ctx, "k", 5*time.Second); err != nil || v != "v1" {
		t.Fatalf("backoff stale Get = %v, %v", v, err)
	}
	if got := loader.callCount(); got != 2 {
		t.Fatalf("loader calls = %d, want 2 (backoff not honored)", got)
	}

	// A caller that cannot accept staleness gets the last error.
	if _, err := c.Get(ctx, "k", 0); err != errBoom {
		t.Fatalf("Get during backoff err = %v, want errBoom", err)
	}
	if got := loader.callCount(); got != 2 {
		t.Fatalf("loader calls = %d, want 2", got)
	}

	// Advance past the 1s backoff: the loader is retried and fails again,
	// doubling the backoff to 2s.
	clock.Advance(1500 * time.Millisecond)
	go func() { c.Get(ctx, "k", 0) }()
	if n := <-loader.entered; n != 3 {
		t.Fatalf("loader call #%d, want 3 after backoff elapsed", n)
	}
	loader.release <- result{err: errBoom}
	waitFor(t, "second failure recorded", func() bool { return c.failureCount("k") == 2 })

	clock.Advance(1500 * time.Millisecond) // inside the 2s backoff
	if _, err := c.Get(ctx, "k", 0); err != errBoom {
		t.Fatalf("Get in doubled backoff err = %v, want errBoom", err)
	}
	if got := loader.callCount(); got != 3 {
		t.Fatalf("loader calls = %d, want 3", got)
	}

	// Past the doubled backoff the refresh succeeds and resets failures.
	clock.Advance(time.Second)
	go func() { c.Get(ctx, "k", 0) }()
	if n := <-loader.entered; n != 4 {
		t.Fatalf("loader call #%d, want 4", n)
	}
	loader.release <- result{val: "v2"}
	waitFor(t, "v2 stored", func() bool {
		v, ok := c.storedValue("k")
		return ok && v == "v2"
	})
	if got := c.failureCount("k"); got != 0 {
		t.Fatalf("failures = %d, want reset to 0", got)
	}
	if v, err := c.Get(ctx, "k", 0); err != nil || v != "v2" {
		t.Fatalf("post-recovery Get = %v, %v", v, err)
	}
	if got := loader.callCount(); got != 4 {
		t.Fatalf("loader calls = %d, want 4", got)
	}
}

// TestInvalidateAfterRefreshSuccess: an invalidation arriving right after a
// refresh stores its value deletes it; the next Get reloads.
func TestInvalidateAfterRefreshSuccess(t *testing.T) {
	clock := NewFakeClock(start)
	loader := newControllableLoader()
	c := New(Options{TTL: 10 * time.Second, Clock: clock, Loader: loader.load})
	ctx := context.Background()

	go func() { c.Get(ctx, "k", 0) }()
	<-loader.entered
	loader.release <- result{val: "v1"}
	waitFor(t, "v1 stored", func() bool {
		_, ok := c.storedValue("k")
		return ok
	})

	c.Invalidate("k")
	if _, ok := c.storedValue("k"); ok {
		t.Fatal("entry present after Invalidate")
	}

	// Even with a generous window there is nothing stale to serve.
	resCh := make(chan interface{}, 1)
	go func() {
		v, err := c.Get(ctx, "k", time.Hour)
		if err != nil {
			t.Errorf("Get error: %v", err)
		}
		resCh <- v
	}()
	if n := <-loader.entered; n != 2 {
		t.Fatalf("loader call #%d, want 2", n)
	}
	loader.release <- result{val: "v2"}
	if v := <-resCh; v != "v2" {
		t.Fatalf("Get = %v, want v2", v)
	}
}
