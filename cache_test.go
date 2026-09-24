package negcache

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newTestCache(src SourceFunc[string]) (*Cache[string], *FakeClock) {
	clock := NewFakeClock(t0)
	return New[string](Config[string]{
		Source:       src,
		Clock:        clock,
		Capacity:     16,
		PositiveTTL:  time.Minute,
		NotFoundTTL:  10 * time.Second,
		TransientTTL: time.Second,
		PermanentTTL: time.Hour,
		HardTTL:      5 * time.Minute,
		BackoffMax:   4 * time.Second,
	}), clock
}

func TestNotFoundCachedWithClassAndVersion(t *testing.T) {
	var calls atomic.Int32
	src := func(context.Context, string) Response[string] {
		calls.Add(1)
		return Response[string]{Version: 7, Err: NotFound(ErrNotFound)}
	}
	c, _ := newTestCache(src)
	defer c.Close()

	for i := 0; i < 3; i++ {
		_, err := c.Get(context.Background(), "t1", "k")
		var se *SourceError
		if !errors.As(err, &se) || se.Class != ClassNotFound {
			t.Fatalf("call %d: want cached not-found, got %v", i, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("source called %d times, want 1", calls.Load())
	}
	info, ok := c.Inspect("t1", "k")
	if !ok || !info.Negative || info.Class != ClassNotFound || info.Version != 7 {
		t.Fatalf("negative entry must carry class and source version, got %+v", info)
	}
}

func TestClassifyNetworkLikeErrorIsTransient(t *testing.T) {
	cases := []struct {
		err  error
		want Class
	}{
		{fmt.Errorf("dial tcp 10.0.0.1: connect: connection refused"), ClassTransient},
		{&net.DNSError{Err: "timeout", IsTimeout: true}, ClassTransient},
		{fmt.Errorf("wrap: %w", ErrNotFound), ClassNotFound},
		{context.Canceled, ClassCanceled},
		{context.DeadlineExceeded, ClassCanceled},
		{nil, ClassNone},
	}
	for _, tc := range cases {
		if got := Classify(tc.err); got != tc.want {
			t.Errorf("Classify(%v) = %s, want %s", tc.err, got, tc.want)
		}
	}
}

func TestTransientNetworkErrorNotCachedAsNotFound(t *testing.T) {
	src := func(context.Context, string) Response[string] {
		// Simulated network-style failure, no classification attached.
		return Response[string]{Version: 3, Err: Transient(fmt.Errorf("read tcp: connection reset by peer"))}
	}
	c, _ := newTestCache(src)
	defer c.Close()

	_, err := c.Get(context.Background(), "t1", "k")
	var se *SourceError
	if !errors.As(err, &se) || se.Class != ClassTransient {
		t.Fatalf("want transient, got %v", err)
	}
	info, _ := c.Inspect("t1", "k")
	if info.Class == ClassNotFound {
		t.Fatal("network-like error must never be cached as not-found")
	}
	if info.Class != ClassTransient {
		t.Fatalf("cached class = %s, want transient", info.Class)
	}
}

func TestRealSuccessOverridesNegative(t *testing.T) {
	clock := NewFakeClock(t0)
	var mode atomic.Int32 // 0 = not-found, 1 = success
	src := func(context.Context, string) Response[string] {
		if mode.Load() == 0 {
			return Response[string]{Version: 1, Err: NotFound(ErrNotFound)}
		}
		return Response[string]{Value: "real", Version: 2}
	}
	c := New[string](Config[string]{Source: src, Clock: clock, NotFoundTTL: 10 * time.Second})
	defer c.Close()

	if _, err := c.Get(context.Background(), "t1", "k"); err == nil {
		t.Fatal("want not-found first")
	}
	mode.Store(1)
	clock.Advance(11 * time.Second) // negative expired
	v, err := c.Get(context.Background(), "t1", "k")
	if err != nil || v != "real" {
		t.Fatalf("want success after negative expiry, got %q, %v", v, err)
	}
	info, _ := c.Inspect("t1", "k")
	if info.Negative || info.Version != 2 {
		t.Fatalf("success must override negative, got %+v", info)
	}
}

func TestCancellationNotCachedNotShared(t *testing.T) {
	clock := NewFakeClock(t0)
	started := make(chan struct{})
	gate := make(chan struct{})
	var calls atomic.Int32
	src := func(context.Context, string) Response[string] {
		calls.Add(1)
		close(started)
		<-gate
		return Response[string]{Value: "v", Version: 1}
	}
	c := New[string](Config[string]{Source: src, Clock: clock})
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Get(ctx, "t1", "k")
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; Classify(err) != ClassCanceled {
		t.Fatalf("caller wants canceled, got %v", err)
	}
	// The shared fetch survives the caller's cancellation.
	close(gate)
	c.Close()
	if _, err := c.Get(context.Background(), "t1", "k"); err != nil {
		t.Fatalf("success must be cached despite earlier cancellation: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("source called %d times, want 1", calls.Load())
	}
}

func TestManyCallersCancelDoesNotPolluteWaiters(t *testing.T) {
	started := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	src := func(context.Context, string) Response[string] {
		calls.Add(1)
		once.Do(func() { close(started) })
		<-gate
		return Response[string]{Value: "shared", Version: 9}
	}
	c, _ := newTestCache(src)
	defer c.Close()

	const n = 5
	ctxs := make([]context.Context, n)
	cancels := make([]context.CancelFunc, n)
	results := make(chan error, n)
	values := make(chan string, n)
	for i := 0; i < n; i++ {
		ctxs[i], cancels[i] = context.WithCancel(context.Background())
		i := i
		go func() {
			v, err := c.Get(ctxs[i], "t1", "k")
			results <- err
			values <- v
		}()
	}
	<-started
	// Wait until every caller is parked on the shared in-flight call,
	// so cancellation is deterministic.
	for c.Stats().Waiters != n {
		runtime.Gosched()
	}
	for i := 0; i < n-1; i++ {
		cancels[i]()
	}
	close(gate)
	success := 0
	for i := 0; i < n; i++ {
		err := <-results
		v := <-values
		switch {
		case err == nil:
			success++
			if v != "shared" {
				t.Fatalf("surviving waiter got %q", v)
			}
		case Classify(err) != ClassCanceled:
			t.Fatalf("unexpected waiter error: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("want exactly 1 surviving waiter, got %d", success)
	}
	if calls.Load() != 1 {
		t.Fatalf("source called %d times, want 1 (singleflight)", calls.Load())
	}
	// Cache holds the success; nobody's cancellation polluted it.
	if v, err := c.Get(context.Background(), "t1", "k"); err != nil || v != "shared" {
		t.Fatalf("cache polluted by cancellations: %q, %v", v, err)
	}
	if s := c.Stats(); s.Inflight != 0 || s.Waiters != 0 {
		t.Fatalf("inflight state leaked: %+v", s)
	}
}

func TestSourceCanceledResultNeverCached(t *testing.T) {
	var calls atomic.Int32
	src := func(context.Context, string) Response[string] {
		calls.Add(1)
		return Response[string]{Err: Canceled(context.Canceled)}
	}
	c, _ := newTestCache(src)
	defer c.Close()

	if _, err := c.Get(context.Background(), "t1", "k"); Classify(err) != ClassCanceled {
		t.Fatalf("want canceled, got %v", err)
	}
	if _, ok := c.Inspect("t1", "k"); ok {
		t.Fatal("canceled result must never be cached")
	}
	c.Get(context.Background(), "t1", "k")
	if calls.Load() != 2 {
		t.Fatalf("canceled result was cached/shared: %d calls", calls.Load())
	}
}

func TestTenantIsolation(t *testing.T) {
	var calls atomic.Int32
	src := func(_ context.Context, key string) Response[string] {
		calls.Add(1)
		return Response[string]{Version: 1, Err: NotFound(errors.New("no " + key))}
	}
	c, _ := newTestCache(src)
	defer c.Close()

	c.Get(context.Background(), "tenant-a", "k")
	c.Get(context.Background(), "tenant-a", "k")
	c.Get(context.Background(), "tenant-b", "k")
	if calls.Load() != 2 {
		t.Fatalf("negative leaked across tenants: %d source calls", calls.Load())
	}
	if _, ok := c.Inspect("tenant-b", "k"); !ok {
		t.Fatal("tenant-b must have its own negative entry")
	}
	c.Invalidate("tenant-a", "k")
	if _, ok := c.Inspect("tenant-b", "k"); !ok {
		t.Fatal("invalidating tenant-a must not touch tenant-b")
	}
}

func TestNegativeEntriesOccupyCapacity(t *testing.T) {
	clock := NewFakeClock(t0)
	src := func(_ context.Context, key string) Response[string] {
		if key == "pos" {
			return Response[string]{Value: "v", Version: 1}
		}
		return Response[string]{Version: 1, Err: NotFound(ErrNotFound)}
	}
	c := New[string](Config[string]{Source: src, Clock: clock, Capacity: 2})
	defer c.Close()

	c.Get(context.Background(), "t", "pos")  // positive
	c.Get(context.Background(), "t", "neg1") // negative, takes a slot
	c.Get(context.Background(), "t", "neg2") // negative, evicts LRU ("pos")
	if s := c.Stats(); s.Entries != 2 {
		t.Fatalf("entries = %d, want 2 (negatives count toward capacity)", s.Entries)
	}
	if _, ok := c.Inspect("t", "pos"); ok {
		t.Fatal("LRU positive should have been evicted by negative entries")
	}
	if _, ok := c.Inspect("t", "neg2"); !ok {
		t.Fatal("newest negative missing")
	}
}

func TestInvalidate(t *testing.T) {
	var calls atomic.Int32
	src := func(context.Context, string) Response[string] {
		return Response[string]{Value: fmt.Sprint(calls.Add(1)), Version: uint64(calls.Load())}
	}
	c, _ := newTestCache(src)
	defer c.Close()

	v1, _ := c.Get(context.Background(), "t", "k")
	c.Invalidate("t", "k")
	v2, _ := c.Get(context.Background(), "t", "k")
	if v1 == v2 {
		t.Fatal("invalidate must force a refetch")
	}
}

func TestCancellationNotLoggedAsSourceError(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	logger := LoggerFunc(func(f string, a ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(f, a...))
		mu.Unlock()
	})
	started := make(chan struct{})
	gate := make(chan struct{})
	src := func(context.Context, string) Response[string] {
		close(started)
		<-gate
		return Response[string]{Value: "v", Version: 1}
	}
	c := New[string](Config[string]{
		Source: src, Clock: NewFakeClock(t0), Logger: logger,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Get(ctx, "t", "k"); close(done) }()
	<-started
	cancel()
	<-done
	close(gate)
	c.Close()

	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "caller_canceled") {
		t.Fatalf("missing caller_canceled log, got:\n%s", joined)
	}
	for _, l := range lines {
		if strings.Contains(l, "commit_negative") && strings.Contains(l, "canceled") {
			t.Fatalf("cancellation logged as source error: %q", l)
		}
	}
}

func TestResourceRelease(t *testing.T) {
	baseline := runtime.NumGoroutine()
	gate := make(chan struct{})
	var started atomic.Int32
	src := func(context.Context, string) Response[string] {
		started.Add(1)
		<-gate
		return Response[string]{Value: "v", Version: 1}
	}
	c, _ := newTestCache(src)

	const keys = 8
	var wg sync.WaitGroup
	for i := 0; i < keys; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		for j := 0; j < 3; j++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				c.Get(ctx, "t", fmt.Sprint(i))
			}(i)
		}
		cancel() // all callers cancel; fetches still finish
	}
	for started.Load() < keys {
		runtime.Gosched()
	}
	close(gate)
	wg.Wait()
	c.Close()

	if s := c.Stats(); s.Inflight != 0 || s.Waiters != 0 {
		t.Fatalf("in-flight state leaked: %+v", s)
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > baseline {
		t.Fatalf("goroutines leaked: baseline %d, now %d", baseline, n)
	}
}
