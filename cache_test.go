package negcache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func testConfig(src Source, clock *FakeClock) Config {
	return Config{
		Source:       src,
		Now:          clock.Now,
		NotFoundTTL:  10 * time.Second,
		TempBaseTTL:  1 * time.Second,
		TempMaxTTL:   4 * time.Second,
		PermanentTTL: 30 * time.Second,
		HardTTL:      60 * time.Second,
		Capacity:     16,
	}
}

func TestNotFoundCachedWithVersionAndClass(t *testing.T) {
	clock := NewFakeClock(t0)
	calls := 0
	c := New(testConfig(func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
		calls++
		return nil, 7, ErrNotFound
	}, clock))
	defer c.Close()

	r, err := c.Get(context.Background(), "t1", "k")
	if err != nil || !r.Negative || r.Class != ClassNotFound || r.SourceVersion != 7 {
		t.Fatalf("first get: %+v err=%v", r, err)
	}
	r, err = c.Get(context.Background(), "t1", "k")
	if err != nil || !r.Negative || !r.FromCache || r.Class != ClassNotFound || r.SourceVersion != 7 {
		t.Fatalf("cached get: %+v err=%v", r, err)
	}
	if calls != 1 {
		t.Fatalf("source called %d times, want 1", calls)
	}
}

func TestSuccessOverwritesNegative(t *testing.T) {
	clock := NewFakeClock(t0)
	var fail atomic.Bool
	fail.Store(true)
	c := New(testConfig(func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
		if fail.Load() {
			return nil, 1, ErrNotFound
		}
		return []byte("v"), 2, nil
	}, clock))
	defer c.Close()

	r, _ := c.Get(context.Background(), "t1", "k")
	if !r.Negative {
		t.Fatalf("want negative, got %+v", r)
	}
	fail.Store(false)
	clock.Advance(11 * time.Second) // negative TTL expired
	r, err := c.Get(context.Background(), "t1", "k")
	if err != nil || r.Negative || string(r.Value) != "v" {
		t.Fatalf("success did not overwrite negative: %+v err=%v", r, err)
	}
	r, _ = c.Get(context.Background(), "t1", "k")
	if !r.FromCache || r.Negative || string(r.Value) != "v" {
		t.Fatalf("cached success: %+v", r)
	}
}

func TestTemporaryBackoff(t *testing.T) {
	clock := NewFakeClock(t0)
	var ttls []time.Duration
	cfg := testConfig(func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
		return nil, 0, &TempError{Msg: "dial timeout"}
	}, clock)
	cfg.OnStore = func(prev *EntrySnapshot, next EntrySnapshot) {
		ttls = append(ttls, next.ExpiresAt.Sub(next.StoredAt))
	}
	c := New(cfg)
	defer c.Close()

	for i := 0; i < 4; i++ {
		r, _ := c.Get(context.Background(), "t1", "k")
		if !r.Negative || r.Class != ClassTemporary {
			t.Fatalf("iter %d: %+v", i, r)
		}
		if i < 3 {
			clock.Advance(ttls[i] + time.Millisecond) // let the entry expire
		}
	}
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second}
	for i := range want {
		if ttls[i] != want[i] {
			t.Fatalf("backoff %v, want %v", ttls, want)
		}
	}
}

func TestPermanentErrorTTL(t *testing.T) {
	clock := NewFakeClock(t0)
	calls := 0
	c := New(testConfig(func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
		calls++
		return nil, 3, errors.New("schema mismatch")
	}, clock))
	defer c.Close()

	r, _ := c.Get(context.Background(), "t1", "k")
	if !r.Negative || r.Class != ClassPermanent {
		t.Fatalf("%+v", r)
	}
	clock.Advance(29 * time.Second)
	c.Get(context.Background(), "t1", "k")
	if calls != 1 {
		t.Fatalf("permanent error not cached for its TTL: calls=%d", calls)
	}
	clock.Advance(2 * time.Second) // past PermanentTTL=30s
	c.Get(context.Background(), "t1", "k")
	if calls != 2 {
		t.Fatalf("permanent entry should expire: calls=%d", calls)
	}
}

func TestCancellationNotCachedNorLoggedAsSourceError(t *testing.T) {
	clock := NewFakeClock(t0)
	var mu sync.Mutex
	var logs []LogEntry
	cfg := testConfig(func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
		<-ctx.Done()
		return nil, 0, ctx.Err()
	}, clock)
	cfg.Log = func(le LogEntry) { mu.Lock(); logs = append(logs, le); mu.Unlock() }
	c := New(cfg)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Get(ctx, "t1", "k"); done <- err }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	time.Sleep(20 * time.Millisecond) // let fill finish
	if c.Len() != 0 {
		t.Fatalf("canceled error must not be cached, len=%d", c.Len())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) == 0 {
		t.Fatal("expected cancellation to be logged")
	}
	for _, le := range logs {
		if le.Cancellation && le.SourceErr {
			t.Fatalf("cancellation misreported as source error: %+v", le)
		}
	}
}

func TestOneCallerCancelDoesNotPolluteOthers(t *testing.T) {
	clock := NewFakeClock(t0)
	release := make(chan struct{})
	started := make(chan struct{})
	var calls atomic.Int32
	c := New(testConfig(func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return []byte("shared"), 9, nil
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}, clock))
	defer c.Close()

	ctx1, cancel1 := context.WithCancel(context.Background())
	r1 := make(chan error, 1)
	go func() { _, err := c.Get(ctx1, "t1", "k"); r1 <- err }()
	<-started
	r2 := make(chan Result, 1)
	go func() { r, _ := c.Get(context.Background(), "t1", "k"); r2 <- r }()
	time.Sleep(20 * time.Millisecond) // both waiters attached

	cancel1()
	if err := <-r1; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller1: %v", err)
	}
	close(release)
	got := <-r2
	if got.Negative || string(got.Value) != "shared" {
		t.Fatalf("caller2 polluted by caller1 cancel: %+v", got)
	}
	r, _ := c.Get(context.Background(), "t1", "k")
	if !r.FromCache || string(r.Value) != "shared" {
		t.Fatalf("shared result not cached: %+v", r)
	}
	if calls.Load() != 1 {
		t.Fatalf("source called %d times, want 1 (singleflight)", calls.Load())
	}
}

func TestStaleErrorCannotOverwriteSuccess(t *testing.T) {
	clock := NewFakeClock(t0)
	checker := NewChecker(60*time.Second, 4*time.Second)
	slowStarted := make(chan struct{})
	slowRelease := make(chan struct{})
	var step atomic.Int32
	cfg := testConfig(func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
		if step.Add(1) == 1 {
			close(slowStarted)
			<-slowRelease // late not-found, v1
			return nil, 1, ErrNotFound
		}
		return []byte("fresh"), 2, nil // success, v2
	}, clock)
	cfg.OnStore = checker.OnStore
	cfg.Log = checker.Logger(nil)
	c := New(cfg)
	defer c.Close()

	slow := make(chan Result, 1)
	go func() { r, _ := c.Get(context.Background(), "t1", "k"); slow <- r }()
	<-slowStarted

	c.Invalidate("t1", "k") // bumps generation: slow fill is now stale
	r, err := c.Get(context.Background(), "t1", "k")
	if err != nil || r.Negative || string(r.Value) != "fresh" {
		t.Fatalf("fresh fill: %+v err=%v", r, err)
	}
	close(slowRelease)
	if r := <-slow; !r.Negative || r.Class != ClassNotFound {
		t.Fatalf("stale waiter should still get its answer: %+v", r)
	}
	r, _ = c.Get(context.Background(), "t1", "k")
	if r.Negative || string(r.Value) != "fresh" {
		t.Fatalf("late not-found overwrote success: %+v", r)
	}
	if v := checker.Violations(); len(v) != 0 {
		t.Fatalf("checker violations: %v", v)
	}
}

func TestStaleNegativeVersionGuard(t *testing.T) {
	clock := NewFakeClock(t0)
	c := New(testConfig(nil, clock))
	defer c.Close()
	k := ckey{"t1", "k"}

	c.storeLocked(k, EntrySnapshot{Tenant: "t1", Key: "k", Success: true,
		Value: []byte("v"), SourceVersion: 5}, clock.Now(), time.Minute)
	c.storeNegativeLocked(k, 3, ClassNotFound, clock.Now(), time.Minute) // older: rejected
	if e := c.entries[k]; !e.snap.Success {
		t.Fatal("older not-found pushed out a newer success")
	}
	c.storeNegativeLocked(k, 7, ClassNotFound, clock.Now(), time.Minute) // newer: accepted
	if e := c.entries[k]; e.snap.Success || e.snap.SourceVersion != 7 {
		t.Fatal("newer not-found should replace the success")
	}
}

func TestTenantIsolation(t *testing.T) {
	clock := NewFakeClock(t0)
	var calls atomic.Int32
	c := New(testConfig(func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
		calls.Add(1)
		if tenant == "alice" {
			return nil, 1, ErrNotFound
		}
		return []byte("bob-data"), 1, nil
	}, clock))
	defer c.Close()

	ra, _ := c.Get(context.Background(), "alice", "k")
	if !ra.Negative {
		t.Fatalf("alice: %+v", ra)
	}
	rb, _ := c.Get(context.Background(), "bob", "k")
	if rb.Negative || string(rb.Value) != "bob-data" {
		t.Fatalf("bob saw alice's negative: %+v", rb)
	}
	if calls.Load() != 2 {
		t.Fatalf("negative shared across tenants: calls=%d", calls.Load())
	}
}

func TestCapacityCountsNegatives(t *testing.T) {
	clock := NewFakeClock(t0)
	cfg := testConfig(func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
		return nil, 1, ErrNotFound
	}, clock)
	cfg.Capacity = 3
	c := New(cfg)
	defer c.Close()

	for _, k := range []string{"a", "b", "c"} {
		c.Get(context.Background(), "t1", k)
	}
	if c.Len() != 3 {
		t.Fatalf("len=%d, want 3", c.Len())
	}
	c.Get(context.Background(), "t1", "d") // evicts LRU "a"
	if c.Len() != 3 {
		t.Fatalf("negative entries must count toward capacity: len=%d", c.Len())
	}
	var calls atomic.Int32
	c.cfg.Source = func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
		calls.Add(1)
		return nil, 1, ErrNotFound
	}
	c.Get(context.Background(), "t1", "a")
	if calls.Load() != 1 {
		t.Fatal("evicted negative entry was still served from cache")
	}
	c.Get(context.Background(), "t1", "d")
	if calls.Load() != 1 {
		t.Fatal("non-evicted negative entry should be a cache hit")
	}
}

func TestTempNetErrorNotTreatedAsNotFound(t *testing.T) {
	if got := DefaultClassifier(&TempError{Msg: "i/o timeout"}); got != ClassTemporary {
		t.Fatalf("net-style temp error classified as %v", got)
	}
	clock := NewFakeClock(t0)
	c := New(testConfig(func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
		return nil, 0, &TempError{Msg: "connection reset"}
	}, clock))
	defer c.Close()
	r, _ := c.Get(context.Background(), "t1", "k")
	if r.Class != ClassTemporary {
		t.Fatalf("temporary network error handled as %v", r.Class)
	}
}

func TestHardTTL(t *testing.T) {
	clock := NewFakeClock(t0)
	cfg := testConfig(func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
		return []byte("v"), 1, nil
	}, clock)
	cfg.HardTTL = 5 * time.Second
	var snap EntrySnapshot
	cfg.OnStore = func(prev *EntrySnapshot, next EntrySnapshot) { snap = next }
	c := New(cfg)
	defer c.Close()

	c.Get(context.Background(), "t1", "k")
	if !snap.ExpiresAt.Equal(snap.StoredAt.Add(5 * time.Second)) {
		t.Fatalf("success entry must be capped by hard TTL: %+v", snap)
	}
	var calls atomic.Int32
	c.cfg.Source = func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
		calls.Add(1)
		return []byte("v"), 2, nil
	}
	clock.Advance(6 * time.Second)
	c.Get(context.Background(), "t1", "k")
	if calls.Load() != 1 {
		t.Fatal("entry outlived its hard TTL")
	}
}

func TestResourceReleaseOnClose(t *testing.T) {
	clock := NewFakeClock(t0)
	c := New(testConfig(func(ctx context.Context, tenant, key string) ([]byte, uint64, error) {
		<-ctx.Done()
		return nil, 0, ctx.Err()
	}, clock))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res := make(chan error, 1)
	go func() { _, err := c.Get(ctx, "t1", "k"); res <- err }()
	time.Sleep(20 * time.Millisecond)

	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not release in-flight resources")
	}
	if err := <-res; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter after Close: %v", err)
	}
	c.mu.Lock()
	n := len(c.flights)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d flights leaked after Close", n)
	}
	if _, err := c.Get(context.Background(), "t1", "k"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get after Close: %v", err)
	}
}
