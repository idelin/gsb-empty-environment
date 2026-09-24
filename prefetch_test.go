package prefetch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// gateSource is an injectable, fully controlled Source. Each key's load
// blocks until the test opens its gate (or the context is canceled).
type gateSource struct {
	mu      sync.Mutex
	gates   map[string]chan struct{}
	started map[string]int
	done    map[string]int
	ver     map[string]uint64
}

func newGateSource() *gateSource {
	return &gateSource{
		gates:   make(map[string]chan struct{}),
		started: make(map[string]int),
		done:    make(map[string]int),
		ver:     make(map[string]uint64),
	}
}

var closedCh = func() chan struct{} { c := make(chan struct{}); close(c); return c }()

func (g *gateSource) Load(ctx context.Context, key string) (Value, error) {
	g.mu.Lock()
	ch, ok := g.gates[key]
	if !ok {
		ch = make(chan struct{})
		g.gates[key] = ch
	}
	g.started[key]++
	g.mu.Unlock()
	select {
	case <-ch:
	case <-ctx.Done():
		return Value{}, ctx.Err()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.done[key]++
	v := g.ver[key]
	if v == 0 {
		v = 1
	}
	return Value{Data: "val-" + key, Version: v}, nil
}

func (g *gateSource) open(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ch, ok := g.gates[key]
	if !ok {
		g.gates[key] = closedCh
		return
	}
	select {
	case <-ch:
	default:
		close(ch)
	}
}

func (g *gateSource) startedCount(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.started[key]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func taskID(s *Scheduler, key string) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[key]; ok {
		return t.id, true
	}
	return 0, false
}

// A real request for a key with an in-flight prefetch joins the same
// task: identity preserved, exactly one load, priority promoted.
func TestSameKeyMergePromotionIdentity(t *testing.T) {
	gs := newGateSource()
	c := NewCache(RealClock(), 4, 4, 0)
	s := NewScheduler(gs, c, 2, 1)
	defer s.Close()

	s.Prefetch(0, "x")
	waitFor(t, "prefetch x started", func() bool { return gs.startedCount("x") == 1 })
	idBefore, ok := taskID(s, "x")
	if !ok {
		t.Fatal("no task for x")
	}

	type got struct {
		v   Value
		err error
	}
	res := make(chan got, 1)
	go func() {
		v, err := s.Get(context.Background(), "x")
		res <- got{v, err}
	}()
	waitFor(t, "real waiter attached", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.tasks["x"].waiters) == 1
	})
	idAfter, ok := taskID(s, "x")
	if !ok || idAfter != idBefore {
		t.Fatalf("promotion must keep task identity: %d -> %d", idBefore, idAfter)
	}
	st := s.Stats()
	if st.FGRunning != 1 || st.PFRunning != 0 {
		t.Fatalf("promoted task should be foreground-classed, got %+v", st)
	}

	gs.open("x")
	select {
	case r := <-res:
		if r.err != nil {
			t.Fatalf("Get: %v", r.err)
		}
		if r.v.Data != "val-x" {
			t.Fatalf("unexpected value %v", r.v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter not served")
	}
	if n := gs.startedCount("x"); n != 1 {
		t.Fatalf("duplicate load: started %d times", n)
	}
	if _, ok := c.Get("x"); !ok {
		t.Fatal("promoted result should be published to cache")
	}
}

// Prefetch may occupy at most maxTotal-fgReserve slots; a foreground
// request starts immediately on a reserved slot even while prefetch is
// in flight and more prefetch is queued.
func TestPrefetchDoesNotStarveForeground(t *testing.T) {
	gs := newGateSource()
	c := NewCache(RealClock(), 8, 8, 0)
	s := NewScheduler(gs, c, 2, 1) // prefetch cap = 1
	defer s.Close()

	s.Prefetch(0, "p1", "p2")
	waitFor(t, "p1 started", func() bool { return gs.startedCount("p1") == 1 })
	if n := gs.startedCount("p2"); n != 0 {
		t.Fatalf("prefetch exceeded its budget: p2 started %d times", n)
	}

	fgDone := make(chan struct{})
	go func() {
		if _, err := s.Get(context.Background(), "f1"); err != nil {
			t.Errorf("Get f1: %v", err)
		}
		close(fgDone)
	}()
	// No gate opened, yet f1 must start: the reserved slot guarantees it.
	waitFor(t, "f1 started despite in-flight prefetch", func() bool { return gs.startedCount("f1") == 1 })
	if n := gs.startedCount("p2"); n != 0 {
		t.Fatal("prefetch p2 must still be capped")
	}

	gs.open("f1")
	<-fgDone
	gs.open("p1")
	waitFor(t, "p2 started after slots freed", func() bool { return gs.startedCount("p2") == 1 })
	gs.open("p2")
	waitFor(t, "drained", func() bool { return s.Stats().Tasks == 0 })
}

// Advancing the generation removes prefetch subscriptions but never
// disturbs real waiters on the same key.
func TestGenerationCancelKeepsRealWaiters(t *testing.T) {
	gs := newGateSource()
	c := NewCache(RealClock(), 8, 8, 0)
	s := NewScheduler(gs, c, 3, 1)
	defer s.Close()

	s.Prefetch(0, "y", "z")
	waitFor(t, "y and z started", func() bool {
		return gs.startedCount("y") == 1 && gs.startedCount("z") == 1
	})

	res := make(chan Value, 1)
	go func() {
		v, err := s.Get(context.Background(), "y")
		if err != nil {
			t.Errorf("Get y: %v", err)
			return
		}
		res <- v
	}()
	waitFor(t, "real waiter attached to y", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.tasks["y"].waiters) == 1
	})

	s.AdvanceGeneration() // cancels stale prefetch subscriptions
	waitFor(t, "z canceled and detached", func() bool { return s.Stats().Tasks == 1 })
	if _, ok := taskID(s, "z"); ok {
		t.Fatal("z should be canceled")
	}

	gs.open("y") // real waiter still completes
	select {
	case v := <-res:
		if v.Data != "val-y" {
			t.Fatalf("unexpected %v", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("real waiter starved by generation cancel")
	}
	if _, ok := c.Get("y"); !ok {
		t.Fatal("y should be published for its real waiter")
	}
	if _, ok := c.Get("z"); ok {
		t.Fatal("canceled prefetch z must not be published")
	}
}

// A stale in-flight prefetch result is rejected by the cache version
// watermark: invalidated values are never resurrected.
func TestStaleResultRejectedOnPublish(t *testing.T) {
	gs := newGateSource()
	gs.open("a")
	c := NewCache(RealClock(), 8, 8, 0)
	s := NewScheduler(gs, c, 2, 1)
	defer s.Close()

	gs.ver["a"] = 1
	if _, err := s.Get(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	c.Invalidate("a", 1) // everything with version <= 1 is stale now

	s.Prefetch(1, "a") // in-flight load will return the stale v1
	waitFor(t, "stale prefetch finished", func() bool {
		return gs.startedCount("a") == 2 && s.Stats().Tasks == 0
	})
	if _, ok := c.Get("a"); ok {
		t.Fatal("stale v1 resurrected an invalidated key")
	}

	gs.ver["a"] = 2 // fresh data satisfies the watermark
	v, err := s.Get(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != 2 {
		t.Fatalf("want v2, got %v", v)
	}
	if got, ok := c.Get("a"); !ok || got.Version != 2 {
		t.Fatal("v2 should be cached")
	}
}

// Mispredictions are confined to the prefetch segment and can never
// evict foreground entries.
func TestMispredictionsDontPolluteForeground(t *testing.T) {
	var mu sync.Mutex
	loads := 0
	src := SourceFunc(func(ctx context.Context, key string) (Value, error) {
		mu.Lock()
		loads++
		mu.Unlock()
		return Value{Data: key, Version: 1}, nil
	})
	c := NewCache(RealClock(), 2, 2, 0)
	s := NewScheduler(src, c, 4, 1)
	defer s.Close()

	s.Prefetch(0, "m1", "m2", "m3", "m4", "m5")
	waitFor(t, "mispredictions loaded", func() bool { mu.Lock(); defer mu.Unlock(); return loads == 5 })
	if fg, pf := c.Len(); fg != 0 || pf != 2 {
		t.Fatalf("prefetch segment should cap at 2, got fg=%d pf=%d", fg, pf)
	}

	for _, k := range []string{"k1", "k2"} {
		if _, err := s.Get(context.Background(), k); err != nil {
			t.Fatal(err)
		}
	}
	s.AdvanceGeneration()
	s.Prefetch(1, "m6", "m7", "m8")
	waitFor(t, "more mispredictions loaded", func() bool { mu.Lock(); defer mu.Unlock(); return loads >= 10 })

	mu.Lock()
	before := loads
	mu.Unlock()
	for _, k := range []string{"k1", "k2"} { // still cached: no reload
		if _, err := s.Get(context.Background(), k); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if loads != before {
		t.Fatalf("foreground entries evicted by prefetch: loads %d -> %d", before, loads)
	}
}

// Canceling a generation releases every resource: contexts are canceled,
// slots freed, task table drained, and Close returns.
func TestResourceReleaseOnCancel(t *testing.T) {
	var mu sync.Mutex
	canceled := 0
	src := SourceFunc(func(ctx context.Context, key string) (Value, error) {
		<-ctx.Done()
		mu.Lock()
		canceled++
		mu.Unlock()
		return Value{}, ctx.Err()
	})
	c := NewCache(RealClock(), 8, 8, 0)
	s := NewScheduler(src, c, 2, 1)

	s.Prefetch(0, "r1", "r2", "r3") // r1 running, r2/r3 pending
	waitFor(t, "r1 running", func() bool { return s.Stats().PFRunning == 1 })
	s.AdvanceGeneration()
	waitFor(t, "all resources released", func() bool {
		st := s.Stats()
		return st.Tasks == 0 && st.PFRunning == 0 && st.Pending == 0
	})
	mu.Lock()
	if canceled != 1 {
		t.Fatalf("running load should observe cancel exactly once, got %d", canceled)
	}
	mu.Unlock()

	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked: goroutine leak")
	}
}

// Failures propagate to waiters, are not cached, and never poison the
// key: a later Get retries the load.
func TestFailureNotCached(t *testing.T) {
	boom := errors.New("boom")
	var mu sync.Mutex
	loads := 0
	src := SourceFunc(func(ctx context.Context, key string) (Value, error) {
		mu.Lock()
		loads++
		mu.Unlock()
		return Value{}, boom
	})
	c := NewCache(RealClock(), 4, 4, 0)
	s := NewScheduler(src, c, 2, 1)
	defer s.Close()

	if _, err := s.Get(context.Background(), "bad"); !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if fg, pf := c.Len(); fg != 0 || pf != 0 {
		t.Fatal("failed load must not be cached")
	}
	if _, err := s.Get(context.Background(), "bad"); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if loads != 2 {
		t.Fatalf("key should be retried, loads=%d", loads)
	}
}

// Prefetch entries expire by the injected clock.
func TestPrefetchTTLExpiry(t *testing.T) {
	mc := NewManualClock(time.Now())
	var mu sync.Mutex
	loads := 0
	src := SourceFunc(func(ctx context.Context, key string) (Value, error) {
		mu.Lock()
		loads++
		mu.Unlock()
		return Value{Data: key, Version: 1}, nil
	})
	c := NewCache(mc, 4, 4, time.Minute)
	s := NewScheduler(src, c, 2, 1)
	defer s.Close()

	s.Prefetch(0, "k")
	waitFor(t, "prefetched", func() bool { mu.Lock(); defer mu.Unlock(); return loads == 1 })
	if fg, pf := c.Len(); fg != 0 || pf != 1 {
		t.Fatalf("entry should sit in prefetch segment, got fg=%d pf=%d", fg, pf)
	}
	mc.Advance(2 * time.Minute)
	if _, ok := c.Get("k"); ok {
		t.Fatal("expired prefetch entry should miss")
	}
	if _, err := s.Get(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if loads != 2 {
		t.Fatalf("expired entry should be reloaded, loads=%d", loads)
	}
}

// Multiple predictors share one per-round prefetch budget.
func TestSharedBudgetAcrossPredictors(t *testing.T) {
	src := SourceFunc(func(ctx context.Context, key string) (Value, error) {
		return Value{Data: key, Version: 1}, nil
	})
	c := NewCache(RealClock(), 8, 8, 0)
	s := NewScheduler(src, c, 4, 1)
	defer s.Close()

	p1 := PredictorFunc(func(seq []string) []string { return []string{"a", "b", "c"} })
	p2 := PredictorFunc(func(seq []string) []string { return []string{"d", "e", "f"} })
	e := NewEngine(s, 2, p1, p2) // shared budget: 2 predictions per round

	if _, err := e.Access(context.Background(), "real"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "prefetch settled", func() bool { return s.Stats().Tasks == 0 })
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a should be prefetched")
	}
	if _, ok := c.Get("b"); !ok {
		t.Fatal("b should be prefetched")
	}
	for _, k := range []string{"c", "d", "e", "f"} {
		if _, ok := c.Get(k); ok {
			t.Fatalf("%s exceeds the shared budget", k)
		}
	}
}
