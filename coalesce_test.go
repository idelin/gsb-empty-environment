package coalesce

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---- injected data source helpers ----

type fakeSource struct {
	mu       sync.Mutex
	calls    int64
	keyCalls map[string]int
	fail     bool
	versions map[string]uint64
	gate     chan struct{}
	entered  chan struct{}
}

func newFakeSource() *fakeSource {
	return &fakeSource{keyCalls: map[string]int{}, versions: map[string]uint64{}}
}

func (f *fakeSource) load(ctx context.Context, keys []string) (map[string]LoadResult[string], error) {
	if f.gate != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
		select {
		case <-f.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	atomic.AddInt64(&f.calls, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, errors.New("source unavailable")
	}
	out := make(map[string]LoadResult[string], len(keys))
	for _, k := range keys {
		f.keyCalls[k]++
		v := f.versions[k]
		out[k] = LoadResult[string]{Value: "v-" + k, Version: v}
	}
	return out, nil
}

func (f *fakeSource) callCount() int { return int(atomic.LoadInt64(&f.calls)) }

func (f *fakeSource) callsForKey(k string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.keyCalls[k]
}

func testCfg(src *fakeSource) (Config[string, string], *ManualClock) {
	clk := NewManualClock()
	return Config[string, string]{
		Load:          src.load,
		Clock:         clk,
		Interval:      time.Millisecond,
		BatchSize:     4,
		QueueCapacity: 8,
	}, clk
}

// waitFor polls cond with the real wall clock while the worker does its work;
// correctness is driven deterministically by ManualClock ticks.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	return cond()
}

// Repeated invalidation of one key a thousand times must coalesce into one
// load, not a thousand round trips.
func TestCoalesceThousandInvalidations(t *testing.T) {
	src := newFakeSource()
	cfg, clk := testCfg(src)
	c := New(cfg)
	defer c.Close()
	src.versions["k"] = 1

	for i := 0; i < 1000; i++ {
		if err := c.Invalidate("k", 1); err != nil {
			t.Fatalf("invalidate %d: %v", i, err)
		}
	}
	if got := c.Snapshot().QueuedNormal; got != 1 {
		t.Fatalf("queued distinct keys = %d, want 1 (merged)", got)
	}
	clk.Advance(time.Millisecond)
	if !waitFor(2*time.Second, func() bool {
		_, _, err := c.Get("k")
		return err == nil
	}) {
		t.Fatal("key never became valid after one coalesced load")
	}
	if got := src.callCount(); got != 1 {
		t.Fatalf("data source calls = %d, want 1 (1000 invalidations merged)", got)
	}
	if got := src.callsForKey("k"); got != 1 {
		t.Fatalf("loads for k = %d, want 1", got)
	}
	if v, ver, err := c.Get("k"); err != nil || v != "v-k" || ver != 1 {
		t.Fatalf("Get = (%q,%d,%v), want (v-k,1,nil)", v, ver, err)
	}
}

// While a load is in flight, another thousand invalidations must not trigger
// extra loads: the in-flight generation is superseded, so its response is not
// confirmed and exactly one more load covers the latest invalidation.
func TestCoalesceDuringFlight(t *testing.T) {
	src := newFakeSource()
	src.gate = make(chan struct{})
	src.entered = make(chan struct{}, 1)
	cfg, clk := testCfg(src)
	c := New(cfg)
	defer c.Close()

	src.versions["k"] = 7
	if err := c.Invalidate("k", 7); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Millisecond)
	<-src.entered // first (generation 1) load is in flight

	for i := 0; i < 1000; i++ {
		if err := c.Invalidate("k", 7); err != nil {
			t.Fatal(err)
		}
	}
	close(src.gate)
	clk.Advance(time.Millisecond) // requeued latest-generation load

	if !waitFor(2*time.Second, func() bool {
		v, ver, err := c.Get("k")
		return err == nil && v == "v-k" && ver == 7
	}) {
		t.Fatal("key never refreshed to latest generation")
	}
	if got := src.callCount(); got != 2 {
		t.Fatalf("data source calls = %d, want 2 (in-flight + one merged retry)", got)
	}
}

// When the bounded queue is full, a new invalidation must NOT leave the old
// value serving as valid. Once capacity frees up the deferred key is admitted
// and refreshed automatically.
func TestQueueFullConservativeInvalid(t *testing.T) {
	src := newFakeSource()
	cfg, clk := testCfg(src) // capacity 8, batch 4
	c := New(cfg)
	defer c.Close()

	// Prime "seed" to a known-valid value.
	src.versions["seed"] = 1
	if err := c.Invalidate("seed", 1); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Millisecond)
	if !waitFor(time.Second, func() bool {
		_, _, err := c.Get("seed")
		return err == nil
	}) {
		t.Fatal("seed never primed")
	}

	// Fill all 8 queue slots with distinct keys; seed keeps its old value.
	for i := 0; i < 8; i++ {
		k := "fill" + strconv.Itoa(i)
		src.versions[k] = 1
		if err := c.Invalidate(k, 1); err != nil {
			t.Fatal(err)
		}
	}
	if s := c.Snapshot(); s.QueuedNormal != 8 || s.Deferred != 0 {
		t.Fatalf("snapshot = %+v, want 8 queued / 0 deferred", s)
	}

	// The 9th invalidation hits a full queue: the old value must be gone.
	if err := c.Invalidate("seed", 2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Get("seed"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Get(seed) after full-queue invalidate = %v, want ErrInvalid (old value must not survive)", err)
	}
	if s := c.Snapshot(); s.Deferred != 1 {
		t.Fatalf("deferred = %d, want 1", s.Deferred)
	}

	// Source now serves the new seed version.
	src.mu.Lock()
	src.versions["seed"] = 2
	src.mu.Unlock()

	// Drain: batches of 4 free the queue, seed is then admitted and loaded.
	clk.Advance(time.Millisecond)
	if !waitFor(time.Second, func() bool {
		return src.callCount() == 2
	}) {
		t.Fatalf("first post-fill batch not served, calls=%d", src.callCount())
	}
	clk.Advance(time.Millisecond)
	clk.Advance(time.Millisecond)
	if !waitFor(2*time.Second, func() bool {
		v, ver, err := c.Get("seed")
		return err == nil && v == "v-seed" && ver == 2
	}) {
		t.Fatalf("deferred seed never recovered: calls=%d snapshot=%+v", src.callCount(), c.Snapshot())
	}
	if s := c.Snapshot(); s.QueuedNormal != 0 || s.Deferred != 0 {
		t.Fatalf("leftover queue state: %+v", s)
	}
}

// A successful load confirms only the generation it actually covered. If the
// key is invalidated again while it is in flight, the response must be
// discarded and a newer load must run.
func TestStaleGenerationNotConfirmed(t *testing.T) {
	src := newFakeSource()
	src.gate = make(chan struct{})
	src.entered = make(chan struct{}, 1)
	cfg, clk := testCfg(src)
	c := New(cfg)
	defer c.Close()

	src.versions["k"] = 1
	if err := c.Invalidate("k", 1); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Millisecond)
	<-src.entered

	// New invalidation at version 2 while generation 1 is still in flight.
	if err := c.Invalidate("k", 2); err != nil {
		t.Fatal(err)
	}
	src.mu.Lock()
	src.versions["k"] = 2 // data source catches up to the new watermark
	src.mu.Unlock()
	if _, _, err := c.Get("k"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Get during flight = %v, want ErrInvalid", err)
	}
	close(src.gate) // releases the stale generation-1 load

	clk.Advance(time.Millisecond)
	if !waitFor(2*time.Second, func() bool {
		_, ver, err := c.Get("k")
		return err == nil && ver == 2
	}) {
		t.Fatalf("current generation never confirmed: calls=%d snapshot=%+v", src.callCount(), c.Snapshot())
	}
	if src.callsForKey("k") != 2 {
		t.Fatalf("loads for k = %d, want 2 (stale one not confirmed)", src.callsForKey("k"))
	}
}

// A data source response below the recorded version watermark must not be
// accepted, even if its generation matches.
func TestVersionWatermarkRejectsStaleSource(t *testing.T) {
	src := newFakeSource()
	cfg, clk := testCfg(src)
	c := New(cfg)
	defer c.Close()

	// Source keeps serving version 5 while invalidation demands version 9.
	src.versions["k"] = 5
	if err := c.Invalidate("k", 9); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Millisecond)
	waitFor(500*time.Millisecond, func() bool { return src.callsForKey("k") >= 2 })
	if _, _, err := c.Get("k"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Get with stale source version = %v, want ErrInvalid", err)
	}

	// Source catches up to the watermark: now it is accepted.
	src.mu.Lock()
	src.versions["k"] = 9
	src.mu.Unlock()
	clk.Advance(time.Millisecond)
	if !waitFor(2*time.Second, func() bool {
		v, ver, err := c.Get("k")
		return err == nil && v == "v-k" && ver == 9
	}) {
		t.Fatal("key never accepted once source reached the watermark")
	}
}

// Critical keys jump ahead of the ordinary lane, within rate-limited batches.
func TestCriticalKeysFirst(t *testing.T) {
	src := newFakeSource()
	cfg, clk := testCfg(src) // batch size 4

	var mu sync.Mutex
	var firstBatch []string
	wrapped := func(ctx context.Context, keys []string) (map[string]LoadResult[string], error) {
		mu.Lock()
		if firstBatch == nil {
			firstBatch = append([]string(nil), keys...)
		}
		mu.Unlock()
		return src.load(ctx, keys)
	}
	cfg.Load = wrapped
	c := New(cfg)
	defer c.Close()

	// Enqueue 4 normal keys first, then 3 critical ones.
	for i := 0; i < 4; i++ {
		k := "n" + strconv.Itoa(i)
		src.versions[k] = 1
		if err := c.Invalidate(k, 1); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		k := "p" + strconv.Itoa(i)
		src.versions[k] = 1
		if err := c.InvalidateCritical(k, 1); err != nil {
			t.Fatal(err)
		}
	}

	clk.Advance(time.Millisecond)
	if !waitFor(time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(firstBatch) == 4
	}) {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("first batch not observed: %v", firstBatch)
	}
	mu.Lock()
	got := append([]string(nil), firstBatch...)
	mu.Unlock()
	for i, k := range got {
		isPri := k[0] == 'p'
		if i < 3 && !isPri {
			t.Fatalf("batch %v: critical keys must precede normal keys", got)
		}
		if i == 3 && isPri {
			t.Fatalf("batch %v: 4th slot should be a normal key", got)
		}
	}
}

// Rate limiting: with 9 keys queued and batch size 4, exactly one batch is
// dispatched per tick — no burst loads.
func TestRateLimitedBatches(t *testing.T) {
	src := newFakeSource()
	cfg, clk := testCfg(src) // interval 1ms, batch 4
	c := New(cfg)
	defer c.Close()

	for i := 0; i < 9; i++ {
		k := "k" + strconv.Itoa(i)
		src.versions[k] = 1
		if err := c.Invalidate(k, 1); err != nil {
			t.Fatal(err)
		}
	}
	clk.Advance(time.Millisecond)
	waitFor(time.Second, func() bool { return src.callCount() == 1 })
	time.Sleep(30 * time.Millisecond) // no further ticks -> no further loads
	if got := src.callCount(); got != 1 {
		t.Fatalf("calls after one tick = %d, want 1 (rate limited)", got)
	}
	clk.Advance(time.Millisecond)
	waitFor(time.Second, func() bool { return src.callCount() == 2 })
	clk.Advance(time.Millisecond)
	if !waitFor(time.Second, func() bool { return src.callCount() == 3 }) {
		t.Fatalf("calls = %d, want 3 after three ticks", src.callCount())
	}
	// All nine keys valid after three batches (4+4+1).
	for i := 0; i < 9; i++ {
		k := "k" + strconv.Itoa(i)
		if _, _, err := c.Get(k); err != nil {
			t.Fatalf("Get(%s) = %v, want valid", k, err)
		}
	}
}

// A failed load must not confirm anything; keys are re-queued and retried on
// the next tick, and the injected OnError sink observes the failure.
func TestFailureInjectionRetry(t *testing.T) {
	src := newFakeSource()
	src.fail = true
	var errs atomic.Int64
	cfg, clk := testCfg(src)
	cfg.OnError = func(keys []string, err error) { errs.Add(1) }
	c := New(cfg)
	defer c.Close()

	src.versions["k"] = 1
	if err := c.Invalidate("k", 1); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Millisecond)
	waitFor(time.Second, func() bool { return errs.Load() == 1 })
	if _, _, err := c.Get("k"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Get after failed load = %v, want ErrInvalid", err)
	}
	if s := c.Snapshot(); s.QueuedNormal != 1 {
		t.Fatalf("failed key not re-queued: %+v", s)
	}

	src.mu.Lock()
	src.fail = false
	src.mu.Unlock()
	clk.Advance(time.Millisecond)
	if !waitFor(2*time.Second, func() bool {
		v, ver, err := c.Get("k")
		return err == nil && v == "v-k" && ver == 1
	}) {
		t.Fatal("retry after failure never succeeded")
	}
}

// Close must release the worker goroutine and the ticker; further operations
// fail with ErrClosed, and Close is idempotent.
func TestCloseReleasesResources(t *testing.T) {
	src := newFakeSource()
	cfg, clk := testCfg(src)
	c := New(cfg)

	if err := c.Invalidate("k", 1); err != nil {
		t.Fatal(err)
	}
	before := runtime.NumGoroutine()
	c.Close()
	c.Close() // idempotent

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runtime.NumGoroutine() > before {
		time.Sleep(5 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Fatalf("goroutines after Close = %d, want <= %d (worker leaked)", got, before)
	}
	if got := len(clk.ticks); got != 0 {
		t.Fatalf("live tickers after Close = %d, want 0 (ticker leaked)", got)
	}
	if err := c.Invalidate("k", 2); !errors.Is(err, ErrClosed) {
		t.Fatalf("Invalidate after Close = %v, want ErrClosed", err)
	}
	if _, _, err := c.Get("k"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get after Close = %v, want ErrClosed", err)
	}
	// Advancing the clock after Close must not dispatch anything.
	clk.Advance(time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	if got := src.callCount(); got != 0 {
		t.Fatalf("calls after Close = %d, want 0", got)
	}
}

// An in-flight load cancelled by Close must not block shutdown and must not
// confirm values afterwards.
func TestCloseCancelsInFlightLoad(t *testing.T) {
	src := newFakeSource()
	src.gate = make(chan struct{})
	src.entered = make(chan struct{}, 1)
	cfg, clk := testCfg(src)
	c := New(cfg)

	src.versions["k"] = 1
	if err := c.Invalidate("k", 1); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Millisecond)
	<-src.entered

	done := make(chan struct{})
	go func() {
		c.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked on in-flight load")
	}
	if _, _, err := c.Get("k"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get after Close = %v, want ErrClosed", err)
	}
}
