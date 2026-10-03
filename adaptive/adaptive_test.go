package adaptive

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// testClock is a manually advanced clock for deterministic tests.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock { return &testClock{now: time.Unix(1_700_000_000, 0)} }
func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.Cooldown = 10 * time.Millisecond
	cfg.BreakerDuration = 100 * time.Millisecond
	return cfg
}

func newTestController(clk *testClock) *Controller {
	c := NewController(testConfig())
	c.SetClock(clk.Now)
	return c
}

func goodMetrics() Metrics { return Metrics{Load: 0.1, QueueDepth: 0, RTT: time.Millisecond} }

func TestStartsAtMinimalLimits(t *testing.T) {
	clk := newTestClock()
	c := newTestController(clk)
	cfg := testConfig()
	l := c.Snapshot()
	if l.MaxInFlight != cfg.MinInFlight || l.BatchSize != cfg.MinBatch {
		t.Fatalf("must start minimal, got %+v", l)
	}
	if l.LeaseDuration < cfg.MinLease || l.LeaseDuration > cfg.MaxLease {
		t.Fatalf("lease out of bounds: %v", l.LeaseDuration)
	}
}

func TestScaleUpUnderLowLoadClampsAtMax(t *testing.T) {
	clk := newTestClock()
	c := newTestController(clk)
	cfg := testConfig()

	prev := c.Snapshot()
	for i := 0; i < 1000; i++ {
		clk.Advance(cfg.Cooldown + time.Millisecond)
		l := c.Update(goodMetrics())
		if l.MaxInFlight < prev.MaxInFlight {
			t.Fatalf("in-flight shrank under low load: %d -> %d", prev.MaxInFlight, l.MaxInFlight)
		}
		if l.MaxInFlight > cfg.MaxInFlight || l.BatchSize > cfg.MaxBatch {
			t.Fatalf("exceeded max bounds: %+v", l)
		}
		prev = l
	}
	if prev.MaxInFlight != cfg.MaxInFlight || prev.BatchSize != cfg.MaxBatch {
		t.Fatalf("did not converge to max bounds: %+v", prev)
	}
}

func TestScaleDownUnderHighLoadClampsAtMin(t *testing.T) {
	clk := newTestClock()
	c := newTestController(clk)
	cfg := testConfig()

	// Scale up first.
	for i := 0; i < 20; i++ {
		clk.Advance(cfg.Cooldown + time.Millisecond)
		c.Update(goodMetrics())
	}
	hot := Metrics{Load: 0.99, QueueDepth: 0, RTT: time.Millisecond}
	prev := c.Snapshot()
	for i := 0; i < 1000; i++ {
		clk.Advance(cfg.Cooldown + time.Millisecond)
		l := c.Update(hot)
		if l.MaxInFlight > prev.MaxInFlight {
			t.Fatalf("in-flight grew under high load: %d -> %d", prev.MaxInFlight, l.MaxInFlight)
		}
		if l.MaxInFlight < cfg.MinInFlight || l.BatchSize < cfg.MinBatch {
			t.Fatalf("below min bounds: %+v", l)
		}
		prev = l
	}
	if prev.MaxInFlight != cfg.MinInFlight || prev.BatchSize != cfg.MinBatch {
		t.Fatalf("did not converge to min bounds: %+v", prev)
	}
}

// Boundary: load exactly at the watermarks must trigger no change.
func TestExactWatermarkBoundaries(t *testing.T) {
	clk := newTestClock()
	c := newTestController(clk)
	cfg := testConfig()

	clk.Advance(cfg.Cooldown + time.Millisecond)
	c.Update(goodMetrics()) // move off the floor
	before := c.Snapshot()

	clk.Advance(cfg.Cooldown + time.Millisecond)
	l := c.Update(Metrics{Load: cfg.HighLoad, QueueDepth: 1}) // == high: no decrease
	if l.MaxInFlight != before.MaxInFlight {
		t.Fatalf("load == HighLoad must not decrease: %d -> %d", before.MaxInFlight, l.MaxInFlight)
	}
	clk.Advance(cfg.Cooldown + time.Millisecond)
	l = c.Update(Metrics{Load: cfg.LowLoad, QueueDepth: 0}) // == low: no increase
	if l.MaxInFlight != before.MaxInFlight {
		t.Fatalf("load == LowLoad must not increase: %d -> %d", before.MaxInFlight, l.MaxInFlight)
	}
	// Just past the watermark: must decrease.
	clk.Advance(cfg.Cooldown + time.Millisecond)
	l = c.Update(Metrics{Load: cfg.HighLoad + 0.001})
	if l.MaxInFlight >= before.MaxInFlight {
		t.Fatalf("load > HighLoad must decrease: %d -> %d", before.MaxInFlight, l.MaxInFlight)
	}
}

// Boundary: zero and saturated metrics must not panic or break invariants.
func TestZeroAndExtremeMetrics(t *testing.T) {
	clk := newTestClock()
	c := newTestController(clk)
	cfg := testConfig()

	clk.Advance(cfg.Cooldown + time.Millisecond)
	l := c.Update(Metrics{}) // all zero
	if l.MaxInFlight < cfg.MinInFlight {
		t.Fatalf("zero metrics broke bounds: %+v", l)
	}
	clk.Advance(cfg.Cooldown + time.Millisecond)
	l = c.Update(Metrics{Load: 1.0, QueueDepth: 1 << 30, RTT: time.Hour, ErrorRate: 0})
	if l.MaxInFlight < cfg.MinInFlight || l.LeaseDuration > cfg.MaxLease {
		t.Fatalf("extreme metrics broke bounds: %+v", l)
	}
}

// Cooldown hysteresis: a second sample inside the cooldown window must not
// change limits, even with an opposite signal.
func TestCooldownHysteresis(t *testing.T) {
	clk := newTestClock()
	c := newTestController(clk)
	cfg := testConfig()

	clk.Advance(cfg.Cooldown + time.Millisecond)
	up := c.Update(goodMetrics())
	// Immediately hammer with high load: still inside cooldown.
	l := c.Update(Metrics{Load: 0.99})
	if l.MaxInFlight != up.MaxInFlight {
		t.Fatalf("cooldown violated: %d -> %d", up.MaxInFlight, l.MaxInFlight)
	}
	clk.Advance(cfg.Cooldown + time.Millisecond)
	l = c.Update(Metrics{Load: 0.99})
	if l.MaxInFlight >= up.MaxInFlight {
		t.Fatal("decrease must apply after cooldown expires")
	}
}

// Breaker: error rate at/above threshold trips the breaker, pins limits,
// and recovery only happens after the breaker window expires.
func TestCircuitBreakerLifecycle(t *testing.T) {
	clk := newTestClock()
	c := newTestController(clk)
	cfg := testConfig()

	for i := 0; i < 5; i++ { // scale up first
		clk.Advance(cfg.Cooldown + time.Millisecond)
		c.Update(goodMetrics())
	}
	scaled := c.Snapshot()

	clk.Advance(cfg.Cooldown + time.Millisecond)
	l := c.Update(Metrics{ErrorRate: cfg.ErrorThreshold}) // == threshold trips
	if !c.BreakerOpen() {
		t.Fatal("breaker must open at error threshold")
	}
	if l.MaxInFlight >= scaled.MaxInFlight || l.LeaseDuration != cfg.MinLease {
		t.Fatalf("breaker must cut limits: %+v vs %+v", l, scaled)
	}
	// During the window, even perfect metrics change nothing.
	clk.Advance(cfg.BreakerDuration / 2)
	if got := c.Update(goodMetrics()); got != l {
		t.Fatalf("limits moved while breaker open: %+v -> %+v", l, got)
	}
	// After the window the breaker half-opens and AIMD resumes.
	clk.Advance(cfg.BreakerDuration)
	c.Update(goodMetrics())
	if c.BreakerOpen() {
		t.Fatal("breaker must close after its window expires")
	}
	clk.Advance(cfg.Cooldown + time.Millisecond)
	if got := c.Update(goodMetrics()); got.MaxInFlight <= l.MaxInFlight {
		t.Fatalf("recovery must scale back up: %+v -> %+v", l, got)
	}
}

// Breaker while already at minimum must never go below the floor.
func TestBreakerAtFloor(t *testing.T) {
	clk := newTestClock()
	c := newTestController(clk)
	cfg := testConfig()
	for i := 0; i < 10; i++ {
		clk.Advance(cfg.Cooldown + time.Millisecond)
		l := c.Update(Metrics{ErrorRate: 1.0})
		if l.MaxInFlight < cfg.MinInFlight || l.BatchSize < cfg.MinBatch {
			t.Fatalf("below floor: %+v", l)
		}
	}
}

// Lease must track RTT and stay clamped under extreme RTT.
func TestLeaseTracksRTTClamped(t *testing.T) {
	clk := newTestClock()
	c := newTestController(clk)
	cfg := testConfig()

	clk.Advance(cfg.Cooldown + time.Millisecond)
	low := c.Update(Metrics{Load: 0.5, RTT: time.Millisecond})
	clk.Advance(cfg.Cooldown + time.Millisecond)
	high := c.Update(Metrics{Load: 0.5, RTT: 100 * time.Millisecond})
	if high.LeaseDuration <= low.LeaseDuration {
		t.Fatalf("lease must grow with RTT: %v vs %v", low.LeaseDuration, high.LeaseDuration)
	}
	clk.Advance(cfg.Cooldown + time.Millisecond)
	huge := c.Update(Metrics{Load: 0.5, RTT: time.Hour})
	if huge.LeaseDuration != cfg.MaxLease {
		t.Fatalf("lease must clamp at MaxLease, got %v", huge.LeaseDuration)
	}
}

// Concurrency safety: hammer Update/Snapshot from many goroutines and
// verify invariants hold at all times (run with -race).
func TestConcurrentSafety(t *testing.T) {
	c := NewController(DefaultConfig())
	cfg := DefaultConfig()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				select {
				case <-stop:
					return
				default:
				}
				m := Metrics{
					Load:       float64((g+i)%10) / 10,
					QueueDepth: (g * i) % 64,
					RTT:        time.Duration(g%5) * time.Millisecond,
				}
				if i%97 == 0 {
					m.ErrorRate = 0.5
				}
				l := c.Update(m)
				assertLimitsValid(t, l, cfg)
			}
		}(g)
	}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				assertLimitsValid(t, c.Snapshot(), cfg)
				_ = c.BreakerOpen()
			}
		}()
	}
	wg.Wait()
	close(stop)
}

func assertLimitsValid(t *testing.T, l Limits, cfg Config) {
	t.Helper()
	if l.MaxInFlight < cfg.MinInFlight || l.MaxInFlight > cfg.MaxInFlight {
		t.Errorf("MaxInFlight out of bounds: %d", l.MaxInFlight)
	}
	if l.BatchSize < cfg.MinBatch || l.BatchSize > cfg.MaxBatch {
		t.Errorf("BatchSize out of bounds: %d", l.BatchSize)
	}
	if l.LeaseDuration < cfg.MinLease || l.LeaseDuration > cfg.MaxLease {
		t.Errorf("LeaseDuration out of bounds: %v", l.LeaseDuration)
	}
}

// Strict no-leak assertion: the controller spawns no goroutines.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		c := NewController(DefaultConfig())
		for j := 0; j < 1000; j++ {
			c.Update(Metrics{Load: float64(j%10) / 10, QueueDepth: j % 32})
		}
		_ = c.Snapshot()
	}
	runtime.GC()
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
