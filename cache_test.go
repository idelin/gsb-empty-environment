package ttlcache

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met: %s", msg)
}

func newTestCache(src Source[string, string], clock *ManualClock) *Cache[string, string] {
	return New(Config[string, string]{
		Source:  src,
		SoftTTL: 10 * time.Second,
		HardTTL: 60 * time.Second,
		Now:     clock.Now,
		Backoff: func(int) time.Duration { return time.Hour },
	})
}

// Soft window: ten concurrent reads share exactly one background refresh,
// and stale results carry the source version and usable-until deadline.
func TestSoftWindowTenReadsOneRefresh(t *testing.T) {
	clock := NewManualClock(time.Unix(1000, 0))
	start := clock.Now()
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	src := func(context.Context, string) (string, error) {
		n := calls.Add(1)
		if n == 2 {
			once.Do(func() { close(entered) })
			<-release // hold the refresh open while readers pile on
		}
		return fmt.Sprintf("v%d", n), nil
	}
	c := newTestCache(src, clock)
	defer c.Close()

	r, err := c.Get(context.Background(), "k")
	if err != nil || r.Value != "v1" || r.Stale || r.Version != 1 {
		t.Fatalf("first Get = %+v, %v", r, err)
	}
	if !r.UsableUntil.Equal(start.Add(60 * time.Second)) {
		t.Fatalf("UsableUntil = %v", r.UsableUntil)
	}
	for i := 0; i < 3; i++ {
		if _, err := c.Get(context.Background(), "k"); err != nil {
			t.Fatal(err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("fresh reads triggered refresh: calls=%d", n)
	}

	clock.Advance(11 * time.Second) // soft-expired, hard-valid

	// Kick off the shared refresh and wait until it is inside the source.
	r, err = c.Get(context.Background(), "k")
	if err != nil || !r.Stale || r.Value != "v1" {
		t.Fatalf("soft Get = %+v, %v", r, err)
	}
	<-entered

	const readers = 10
	results := make([]Result[string], readers)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := c.Get(context.Background(), "k")
			if err != nil {
				t.Error(err)
			}
			results[i] = r
		}(i)
	}
	wg.Wait()
	close(release)
	for i, r := range results {
		if !r.Stale || r.Value != "v1" || r.Version != 1 {
			t.Errorf("reader %d got %+v, want stale v1 version 1", i, r)
		}
		if !r.UsableUntil.Equal(start.Add(60 * time.Second)) {
			t.Errorf("reader %d UsableUntil = %v", i, r.UsableUntil)
		}
	}

	eventually(t, func() bool { return c.InFlight() == 0 }, "refresh finished")
	if n := calls.Load(); n != 2 {
		t.Fatalf("ten soft-window reads caused %d source calls, want 2", n)
	}

	r, err = c.Get(context.Background(), "k")
	if err != nil || r.Value != "v2" || r.Stale || r.Version != 2 {
		t.Fatalf("post-refresh Get = %+v, %v", r, err)
	}
}

// Hard expiry: stale values are never served past the hard deadline, and
// failure backoff never extends it — a hard-expired read forces a refresh
// even while backoff is active.
func TestFailureBackoffDoesNotExtendHardExpiry(t *testing.T) {
	clock := NewManualClock(time.Unix(1000, 0))
	var calls atomic.Int32
	src := func(context.Context, string) (string, error) {
		n := calls.Add(1)
		if n == 2 {
			return "", errors.New("boom")
		}
		return fmt.Sprintf("v%d", n), nil
	}
	c := New(Config[string, string]{
		Source:  src,
		SoftTTL: 10 * time.Second,
		HardTTL: 20 * time.Second,
		Now:     clock.Now,
		Backoff: func(int) time.Duration { return time.Hour },
	})
	defer c.Close()

	if _, err := c.Get(context.Background(), "k"); err != nil { // v1
		t.Fatal(err)
	}
	clock.Advance(11 * time.Second) // soft window
	r, err := c.Get(context.Background(), "k")
	if err != nil || !r.Stale || r.Value != "v1" {
		t.Fatalf("soft Get = %+v, %v", r, err)
	}
	eventually(t, func() bool { return calls.Load() == 2 }, "refresh attempted")
	eventually(t, func() bool { return c.InFlight() == 0 }, "refresh failed")

	clock.Advance(4 * time.Second) // t=15s: backoff active, still hard-valid
	r, err = c.Get(context.Background(), "k")
	if err != nil || !r.Stale || r.Value != "v1" {
		t.Fatalf("backoff Get = %+v, %v", r, err)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("backoff did not throttle soft-window refresh: calls=%d", n)
	}

	clock.Advance(6 * time.Second) // t=21s: hard-expired, backoff still active
	r, err = c.Get(context.Background(), "k")
	if err != nil {
		t.Fatalf("hard-expired Get did not wait for fresh value: %v", err)
	}
	if r.Stale || r.Value != "v3" || r.Version != 2 {
		t.Fatalf("hard-expired Get = %+v, want fresh v3 version 2", r)
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("calls=%d, want 3", n)
	}
}

// A refresh that started before a Set must not overwrite the newer value.
func TestLateRefreshDoesNotOverwriteSet(t *testing.T) {
	clock := NewManualClock(time.Unix(1000, 0))
	var calls atomic.Int32
	release := make(chan struct{})
	src := func(context.Context, string) (string, error) {
		n := calls.Add(1)
		if n == 2 {
			<-release
			return "late", nil
		}
		return fmt.Sprintf("v%d", n), nil
	}
	c := newTestCache(src, clock)
	defer c.Close()

	if _, err := c.Get(context.Background(), "k"); err != nil { // v1
		t.Fatal(err)
	}
	clock.Advance(11 * time.Second)
	if _, err := c.Get(context.Background(), "k"); err != nil { // stale, refresh starts
		t.Fatal(err)
	}
	eventually(t, func() bool { return c.InFlight() == 1 }, "refresh in flight")

	c.Set("k", "manual")
	close(release)
	eventually(t, func() bool { return c.InFlight() == 0 }, "refresh drained")

	r, err := c.Get(context.Background(), "k")
	if err != nil || r.Value != "manual" || r.Stale || r.Version != 2 {
		t.Fatalf("Get after late refresh = %+v, %v; want manual version 2", r, err)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("calls=%d, want 2", n)
	}
}

// A refresh that started before a Delete must not resurrect the key.
func TestLateRefreshDoesNotResurrectDeletedKey(t *testing.T) {
	clock := NewManualClock(time.Unix(1000, 0))
	var calls atomic.Int32
	release := make(chan struct{})
	src := func(context.Context, string) (string, error) {
		n := calls.Add(1)
		if n == 2 {
			<-release
			return "zombie", nil
		}
		return fmt.Sprintf("v%d", n), nil
	}
	c := newTestCache(src, clock)
	defer c.Close()

	if _, err := c.Get(context.Background(), "k"); err != nil { // v1
		t.Fatal(err)
	}
	clock.Advance(11 * time.Second)
	if _, err := c.Get(context.Background(), "k"); err != nil { // stale, refresh starts
		t.Fatal(err)
	}
	eventually(t, func() bool { return c.InFlight() == 1 }, "refresh in flight")

	c.Delete("k")
	close(release)
	eventually(t, func() bool { return c.InFlight() == 0 }, "refresh drained")

	r, err := c.Get(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	if r.Value != "v3" || r.Version != 1 {
		t.Fatalf("Get = %+v; want freshly reloaded v3 version 1, not the zombie", r)
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("calls=%d, want 3", n)
	}
}

// Reader cancellation unblocks only that reader; the shared refresh keeps
// running and its resource is released on Close.
func TestReaderCancellationKeepsSharedRefresh(t *testing.T) {
	clock := NewManualClock(time.Unix(1000, 0))
	var once sync.Once
	started := make(chan struct{})
	src := func(ctx context.Context, _ string) (string, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return "", ctx.Err()
	}
	c := newTestCache(src, clock)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := c.Get(ctx, "k")
		errCh <- err
	}()
	<-started
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled reader got %v", err)
	}
	if n := c.InFlight(); n != 1 {
		t.Fatalf("reader cancellation killed shared refresh: InFlight=%d", n)
	}
	c.Close()
	if n := c.InFlight(); n != 0 {
		t.Fatalf("resource leaked after Close: InFlight=%d", n)
	}
	if _, err := c.Get(context.Background(), "k"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get after Close = %v", err)
	}
}

// Close cancels an in-flight refresh and waits for its resource to be released.
func TestCloseCancelsAndReleases(t *testing.T) {
	clock := NewManualClock(time.Unix(1000, 0))
	var calls atomic.Int32
	src := func(ctx context.Context, _ string) (string, error) {
		if calls.Add(1) == 1 {
			return "v1", nil
		}
		<-ctx.Done()
		return "", ctx.Err()
	}
	c := newTestCache(src, clock)

	if _, err := c.Get(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(11 * time.Second)
	r, err := c.Get(context.Background(), "k") // stale; refresh blocks in source
	if err != nil || !r.Stale {
		t.Fatalf("soft Get = %+v, %v", r, err)
	}
	eventually(t, func() bool { return c.InFlight() == 1 }, "refresh in flight")

	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not release the in-flight refresh")
	}
	if n := c.InFlight(); n != 0 {
		t.Fatalf("resource leaked after Close: InFlight=%d", n)
	}
	if _, err := c.Get(context.Background(), "k"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get after Close = %v", err)
	}
}

// Controllable-clock state exploration: random interleavings of concurrent
// reads, clock advances, Sets and Deletes must preserve the invariants —
// at most one shared refresh per key generation, no stale value served past
// its hard deadline, and all borrowed resources released at Close.
func TestStateExploration(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	clock := NewManualClock(time.Unix(1000, 0))

	var mu sync.Mutex
	inSource := map[string]int{}
	maxConcurrent := map[string]int{}
	var calls atomic.Int64

	src := func(ctx context.Context, key string) (string, error) {
		n := calls.Add(1)
		mu.Lock()
		inSource[key]++
		if inSource[key] > maxConcurrent[key] {
			maxConcurrent[key] = inSource[key]
		}
		mu.Unlock()
		defer func() {
			mu.Lock()
			inSource[key]--
			mu.Unlock()
		}()
		time.Sleep(time.Millisecond) // widen the overlap window
		if n%7 == 0 {
			return "", errors.New("injected failure")
		}
		return fmt.Sprintf("v%d", n), nil
	}

	c := New(Config[string, string]{
		Source:  src,
		SoftTTL: 10 * time.Second,
		HardTTL: 30 * time.Second,
		Now:     clock.Now,
		Backoff: func(f int) time.Duration { return time.Duration(f) * time.Second },
	})

	keys := []string{"a", "b", "c"}
	// Phase 1: reads + clock advances only — strict single-flight per key.
	for i := 0; i < 200; i++ {
		switch rng.Intn(3) {
		case 0, 1:
			readers := 1 + rng.Intn(4)
			var wg sync.WaitGroup
			for j := 0; j < readers; j++ {
				wg.Add(1)
				go func(key string, before time.Time) {
					defer wg.Done()
					r, err := c.Get(context.Background(), key)
					if err != nil {
						t.Errorf("Get(%s): %v", key, err)
						return
					}
					if !r.UsableUntil.After(before) {
						t.Errorf("stale beyond hard deadline: %+v read at %v", r, before)
					}
				}(keys[rng.Intn(len(keys))], clock.Now())
			}
			wg.Wait()
		case 2:
			clock.Advance(time.Duration(rng.Intn(40)) * time.Second)
		}
	}
	eventually(t, func() bool { return c.InFlight() == 0 }, "phase 1 drained")
	for k, m := range maxConcurrent {
		if m > 1 {
			t.Fatalf("key %s had %d concurrent refreshes, want at most 1", k, m)
		}
	}

	// Phase 2: add Set/Delete churn — deadlines must still hold.
	for i := 0; i < 200; i++ {
		switch rng.Intn(5) {
		case 0, 1:
			var wg sync.WaitGroup
			for j := 0; j < 1+rng.Intn(3); j++ {
				wg.Add(1)
				go func(key string, before time.Time) {
					defer wg.Done()
					if r, err := c.Get(context.Background(), key); err == nil && !r.UsableUntil.After(before) {
						t.Errorf("stale beyond hard deadline: %+v read at %v", r, before)
					}
				}(keys[rng.Intn(len(keys))], clock.Now())
			}
			wg.Wait()
		case 2:
			clock.Advance(time.Duration(rng.Intn(40)) * time.Second)
		case 3:
			c.Set(keys[rng.Intn(len(keys))], "manual")
		case 4:
			c.Delete(keys[rng.Intn(len(keys))])
		}
	}

	c.Close()
	if n := c.InFlight(); n != 0 {
		t.Fatalf("borrowed resources not released at Close: InFlight=%d", n)
	}
}
