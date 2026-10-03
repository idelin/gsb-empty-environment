package adaptive

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

// mockClock is a manually advanced clock for deterministic tests.
type mockClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMockClock() *mockClock {
	return &mockClock{now: time.Unix(1700000000, 0)}
}

func (c *mockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mockClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func testConfig(clk *mockClock) Config {
	return Config{
		MinLimit:         2,
		MaxLimit:         16,
		Initial:          4,
		IncreaseStep:     2,
		DecreaseFactor:   0.5,
		LoadSensitivity:  0.5,
		Smoothing:        0.5,
		LatencyTolerance: 2.0,
		Cooldown:         time.Second,
		Now:              clk.Now,
	}
}

func TestDefaultsAreApplied(t *testing.T) {
	l := NewLimiter(Config{})
	defer l.Close()
	st := l.Stats()
	if st.Limit != 1 {
		t.Fatalf("default limit = %d want 1", st.Limit)
	}
	if st.EffectiveLimit != 1 {
		t.Fatalf("default effective = %d want 1", st.EffectiveLimit)
	}
}

func TestInitialClampedToMax(t *testing.T) {
	l := NewLimiter(Config{MinLimit: 1, MaxLimit: 5, Initial: 100})
	defer l.Close()
	if got := l.Stats().Limit; got != 5 {
		t.Fatalf("limit = %d want 5", got)
	}
}

func TestTryAcquireSaturation(t *testing.T) {
	clk := newMockClock()
	l := NewLimiter(testConfig(clk))
	defer l.Close()

	permits := make([]*Permit, 0, 4)
	for i := 0; i < 4; i++ {
		p, err := l.TryAcquire()
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		permits = append(permits, p)
	}
	if _, err := l.TryAcquire(); err != ErrLimitExceeded {
		t.Fatalf("saturation: got %v want ErrLimitExceeded", err)
	}
	if got := l.Stats().Rejected; got != 1 {
		t.Fatalf("rejected = %d want 1", got)
	}
	// Release one slot (healthy latency) and acquire again.
	clk.Advance(10 * time.Millisecond)
	permits[0].Release()
	if _, err := l.TryAcquire(); err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	for _, p := range permits[1:] {
		p.Release()
	}
}

func TestLoadAwareShedding(t *testing.T) {
	clk := newMockClock()
	cfg := testConfig(clk)
	cfg.Initial = 10
	cfg.MaxLimit = 10
	l := NewLimiter(cfg)
	defer l.Close()

	if got := l.Stats().EffectiveLimit; got != 10 {
		t.Fatalf("no load: effective = %d want 10", got)
	}
	l.SetLoad(1.0) // full pressure, sensitivity 0.5 -> 50% shedding
	if got := l.Stats().EffectiveLimit; got != 5 {
		t.Fatalf("full load: effective = %d want 5", got)
	}
	l.SetLoad(0.5)
	if got := l.Stats().EffectiveLimit; got != 7 {
		t.Fatalf("half load: effective = %d want 7", got)
	}
	// Even under full load the floor holds.
	cfg2 := testConfig(clk)
	cfg2.LoadSensitivity = 1.0
	l2 := NewLimiter(cfg2)
	defer l2.Close()
	l2.SetLoad(1.0)
	if got := l2.Stats().EffectiveLimit; got != 2 {
		t.Fatalf("floor: effective = %d want MinLimit 2", got)
	}
}

func TestSetLoadClamped(t *testing.T) {
	l := NewLimiter(Config{})
	defer l.Close()
	l.SetLoad(42)
	if got := l.Stats().Load; got != 1 {
		t.Fatalf("load = %v want 1", got)
	}
	l.SetLoad(-3)
	if got := l.Stats().Load; got != 0 {
		t.Fatalf("load = %v want 0", got)
	}
}

func TestCongestionDecreaseAndCooldown(t *testing.T) {
	clk := newMockClock()
	l := NewLimiter(testConfig(clk))
	defer l.Close()

	// Establish a healthy baseline: 10ms RTT.
	for i := 0; i < 4; i++ {
		p, _ := l.TryAcquire()
		clk.Advance(10 * time.Millisecond)
		p.Release()
	}
	base := l.Stats().Limit

	// Congestion: RTT jumps to 100ms (avg 55ms > min 10ms * 2.0).
	p, _ := l.TryAcquire()
	clk.Advance(100 * time.Millisecond)
	p.Release()
	after := l.Stats().Limit
	if after >= base {
		t.Fatalf("expected decrease: base %d after %d", base, after)
	}

	// Within cooldown a second congested sample must NOT decrease again.
	p, _ = l.TryAcquire()
	clk.Advance(100 * time.Millisecond)
	p.Release()
	if got := l.Stats().Limit; got != after {
		t.Fatalf("cooldown violated: %d -> %d", after, got)
	}

	// After cooldown the decrease applies again.
	clk.Advance(2 * time.Second)
	p, _ = l.TryAcquire()
	clk.Advance(100 * time.Millisecond)
	p.Release()
	if got := l.Stats().Limit; got >= after {
		t.Fatalf("post-cooldown decrease missing: %d vs %d", got, after)
	}
}

func TestDropForcesDecrease(t *testing.T) {
	clk := newMockClock()
	l := NewLimiter(testConfig(clk))
	defer l.Close()
	p, _ := l.TryAcquire()
	clk.Advance(time.Millisecond)
	p.Drop()
	if got := l.Stats().Limit; got != 2 { // 4 * 0.5
		t.Fatalf("limit = %d want 2 after drop", got)
	}
}

func TestRecoveryIncreaseRespectsMaxAndJitterBound(t *testing.T) {
	clk := newMockClock()
	cfg := testConfig(clk)
	cfg.Initial = 4
	l := NewLimiter(cfg)
	defer l.Close()

	prev := l.Stats().Limit
	for i := 0; i < 100; i++ {
		p, _ := l.TryAcquire()
		clk.Advance(time.Millisecond) // healthy 1ms RTT
		p.Release()
		cur := l.Stats().Limit
		// Jittered step must never exceed IncreaseStep per sample.
		if delta := cur - prev; delta > cfg.IncreaseStep {
			t.Fatalf("increase %d exceeds step %d", delta, cfg.IncreaseStep)
		}
		prev = cur
		clk.Advance(2 * time.Second) // stay out of cooldown semantics
	}
	if got := l.Stats().Limit; got != cfg.MaxLimit {
		t.Fatalf("limit = %d want ceiling %d", got, cfg.MaxLimit)
	}
}

func TestDecreaseRespectsMinFloor(t *testing.T) {
	clk := newMockClock()
	l := NewLimiter(testConfig(clk))
	defer l.Close()
	for i := 0; i < 20; i++ {
		p, _ := l.TryAcquire()
		clk.Advance(time.Millisecond)
		p.Drop()
		clk.Advance(2 * time.Second) // pass cooldown
	}
	if got := l.Stats().Limit; got != 2 {
		t.Fatalf("limit = %d want floor 2", got)
	}
}

func TestAcquireContextCancel(t *testing.T) {
	clk := newMockClock()
	cfg := testConfig(clk)
	cfg.Initial = 1
	cfg.MinLimit = 1
	cfg.MaxLimit = 1
	l := NewLimiter(cfg)
	defer l.Close()

	if _, err := l.TryAcquire(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := l.Acquire(ctx); err != context.DeadlineExceeded {
		t.Fatalf("got %v want DeadlineExceeded", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("Acquire did not honor context deadline")
	}
}

func TestAcquireUnblocksAfterRelease(t *testing.T) {
	clk := newMockClock()
	cfg := testConfig(clk)
	cfg.Initial = 1
	cfg.MinLimit = 1
	cfg.MaxLimit = 1
	l := NewLimiter(cfg)
	defer l.Close()

	p, err := l.TryAcquire()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		q, err := l.Acquire(context.Background())
		if err != nil {
			t.Errorf("Acquire: %v", err)
			return
		}
		q.Release()
	}()
	time.Sleep(50 * time.Millisecond)
	clk.Advance(time.Millisecond)
	p.Release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked Acquire never observed the release")
	}
}

func TestPermitDoubleReleaseIdempotent(t *testing.T) {
	clk := newMockClock()
	l := NewLimiter(testConfig(clk))
	defer l.Close()
	p, _ := l.TryAcquire()
	clk.Advance(time.Millisecond)
	p.Release()
	p.Release() // must not decrement twice
	p.Drop()    // must be a no-op after Release
	if got := l.Stats().InFlight; got != 0 {
		t.Fatalf("inflight = %d want 0", got)
	}
}

func TestConcurrentSafety(t *testing.T) {
	clk := newMockClock()
	cfg := testConfig(clk)
	cfg.Initial = 8
	cfg.MaxLimit = 64
	l := NewLimiter(cfg)
	defer l.Close()

	const workers = 64
	const rounds = 200
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				p, err := l.Acquire(context.Background())
				if err != nil {
					t.Errorf("worker %d: %v", id, err)
					return
				}
				clk.Advance(time.Microsecond)
				if (id+i)%17 == 0 {
					p.Drop()
				} else {
					p.Release()
				}
			}
		}(w)
	}
	wg.Wait()

	st := l.Stats()
	if st.InFlight != 0 {
		t.Fatalf("inflight leak: %d", st.InFlight)
	}
	want := uint64(workers * rounds)
	if st.Admitted != want {
		t.Fatalf("admitted = %d want %d", st.Admitted, want)
	}
	if st.Limit < 2 || st.Limit > 64 {
		t.Fatalf("limit %d escaped bounds [2,64]", st.Limit)
	}
}

func TestMetricsSamplerAndNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	var mu sync.Mutex
	load := 0.0
	l := NewLimiter(Config{
		MetricsFunc: func() float64 {
			mu.Lock()
			defer mu.Unlock()
			return load
		},
		SampleInterval: 5 * time.Millisecond,
	})
	mu.Lock()
	load = 0.9
	mu.Unlock()

	deadline := time.Now().Add(2 * time.Second)
	for l.Stats().Load != 0.9 {
		if time.Now().After(deadline) {
			t.Fatal("sampler never propagated load")
		}
		time.Sleep(2 * time.Millisecond)
	}
	l.Close()
	l.Close() // idempotent

	// Allow goroutine scheduler to settle, then assert no leak.
	for i := 0; i < 100; i++ {
		if runtime.NumGoroutine() <= before {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

func TestClosedLimiterRejects(t *testing.T) {
	l := NewLimiter(Config{})
	l.Close()
	if _, err := l.TryAcquire(); err == nil || err == ErrLimitExceeded {
		t.Fatalf("closed limiter: got %v", err)
	}
}
