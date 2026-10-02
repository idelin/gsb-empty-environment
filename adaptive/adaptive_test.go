package adaptive

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

func testOptions() Options {
	return Options{
		Min: 2, Max: 64, Initial: 32,
		HighWater: 0.8, LowWater: 0.4,
		Beta: 0.5, Step: 4, Alpha: 0.5,
		Cooldown: time.Second,
		Now:      time.Now,
	}
}

func TestMultiplicativeDecreaseUnderPressure(t *testing.T) {
	c := NewController(testOptions())
	defer c.Close()
	if got := c.Sample(Metrics{Load: 0.95}); got != 16 {
		t.Fatalf("first decrease: limit=%d, want 16", got)
	}
}

func TestDecreaseCooldownPreventsAvalanche(t *testing.T) {
	now := time.Now()
	opts := testOptions()
	opts.Now = func() time.Time { return now }
	c := NewController(opts)
	defer c.Close()

	c.Sample(Metrics{Load: 0.95}) // decrease to 16, cooldown starts
	for i := 0; i < 5; i++ {
		now = now.Add(100 * time.Millisecond) // inside 1s cooldown
		if got := c.Sample(Metrics{Load: 0.95}); got != 16 {
			t.Fatalf("cooldown violated: limit=%d, want pinned 16", got)
		}
	}
	now = now.Add(time.Second) // cooldown elapsed
	if got := c.Sample(Metrics{Load: 0.95}); got != 8 {
		t.Fatalf("post-cooldown decrease: limit=%d, want 8", got)
	}
}

func TestDecreaseClampsAtFloor(t *testing.T) {
	opts := testOptions()
	opts.Cooldown = 0
	c := NewController(opts)
	defer c.Close()
	for i := 0; i < 20; i++ {
		c.Sample(Metrics{Load: 1.0})
	}
	if got := c.Limit(); got != opts.Min {
		t.Fatalf("limit=%d, want floor %d", got, opts.Min)
	}
}

func TestAdditiveIncreaseWhenIdle(t *testing.T) {
	opts := testOptions()
	opts.Initial = 8
	c := NewController(opts)
	defer c.Close()
	if got := c.Sample(Metrics{Load: 0.1}); got != 12 {
		t.Fatalf("increase step: limit=%d, want 12", got)
	}
	if got := c.Sample(Metrics{Load: 0.1}); got != 16 {
		t.Fatalf("second step: limit=%d, want 16", got)
	}
}

func TestIncreaseClampsAtCeiling(t *testing.T) {
	opts := testOptions()
	opts.Initial = 62
	c := NewController(opts)
	defer c.Close()
	c.Sample(Metrics{Load: 0.0})
	if got := c.Limit(); got != opts.Max {
		t.Fatalf("limit=%d, want ceiling %d", got, opts.Max)
	}
}

func TestHysteresisBandHoldsThreshold(t *testing.T) {
	c := NewController(testOptions())
	defer c.Close()
	for _, load := range []float64{0.5, 0.6, 0.79, 0.41} {
		if got := c.Sample(Metrics{Load: load}); got != 32 {
			t.Fatalf("load %.2f inside band moved threshold to %d", load, got)
		}
	}
}

func TestEWMASmoothing(t *testing.T) {
	opts := testOptions()
	opts.Alpha = 0.5
	c := NewController(opts)
	defer c.Close()
	c.Sample(Metrics{Load: 1.0}) // first sample seeds EWMA at 1.0
	c.Sample(Metrics{Load: 0.0}) // EWMA -> 0.5
	if got := c.Load(); got != 0.5 {
		t.Fatalf("ewma=%v, want 0.5", got)
	}
	c.Sample(Metrics{Load: 0.0}) // EWMA -> 0.25
	if got := c.Load(); got != 0.25 {
		t.Fatalf("ewma=%v, want 0.25", got)
	}
}

func TestLoadClampedToUnitInterval(t *testing.T) {
	c := NewController(testOptions())
	defer c.Close()
	c.Sample(Metrics{Load: 7.5})
	if got := c.Load(); got != 1.0 {
		t.Fatalf("ewma=%v, want clamped 1.0", got)
	}
	c2 := NewController(testOptions())
	defer c2.Close()
	c2.Sample(Metrics{Load: -3})
	if got := c2.Load(); got != 0.0 {
		t.Fatalf("ewma=%v, want clamped 0.0", got)
	}
}

func TestOptionsNormalize(t *testing.T) {
	c := NewController(Options{})
	defer c.Close()
	if c.opts.Min < 1 || c.opts.Max < c.opts.Min || c.opts.Step < 1 {
		t.Fatalf("options not normalized: %+v", c.opts)
	}
	if got := c.Limit(); got < c.opts.Min || got > c.opts.Max {
		t.Fatalf("initial limit %d outside [%d,%d]", got, c.opts.Min, c.opts.Max)
	}
}

func TestTryAcquireRelease(t *testing.T) {
	opts := testOptions()
	opts.Initial = 2
	c := NewController(opts)
	defer c.Close()
	if !c.TryAcquire() || !c.TryAcquire() {
		t.Fatal("first two acquisitions must succeed")
	}
	if c.TryAcquire() {
		t.Fatal("third acquisition exceeds threshold and must fail")
	}
	c.Release()
	if !c.TryAcquire() {
		t.Fatal("acquisition after release must succeed")
	}
	// Releasing past zero is a safe no-op.
	for i := 0; i < 10; i++ {
		c.Release()
	}
	if got := c.Inflight(); got != 0 {
		t.Fatalf("inflight=%d, want 0", got)
	}
}

func TestConcurrentSafety(t *testing.T) {
	c := NewController(testOptions())
	defer c.Close()
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				c.Sample(Metrics{Load: float64((g+i)%100) / 100})
				c.Limit()
				c.Load()
				if c.TryAcquire() {
					c.Release()
				}
			}
		}(g)
	}
	wg.Wait()
	if got := c.Limit(); got < c.opts.Min || got > c.opts.Max {
		t.Fatalf("limit %d escaped bounds [%d,%d]", got, c.opts.Min, c.opts.Max)
	}
	if got := c.Inflight(); got != 0 {
		t.Fatalf("inflight leaked: %d", got)
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		c := NewController(testOptions())
		ctx, cancel := context.WithCancel(context.Background())
		c.Start(ctx, time.Millisecond, func() Metrics { return Metrics{Load: 0.5} })
		cancel()
		c.Close()
	}
	// Also controllers closed without Start must not linger.
	for i := 0; i < 20; i++ {
		c := NewController(testOptions())
		c.Close()
		c.Close() // idempotent
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

func TestBackgroundSamplerAdjusts(t *testing.T) {
	opts := testOptions()
	opts.Cooldown = 0
	c := NewController(opts)
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx, 5*time.Millisecond, func() Metrics { return Metrics{Load: 1.0} })
	deadline := time.Now().Add(2 * time.Second)
	for c.Limit() != opts.Min && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := c.Limit(); got != opts.Min {
		t.Fatalf("background sampler failed to shrink to floor, limit=%d", got)
	}
}

func TestContextCancelStopsSampler(t *testing.T) {
	c := NewController(testOptions())
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx, time.Millisecond, func() Metrics { return Metrics{Load: 0.9} })
	cancel()
	select {
	case <-c.doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("sampler goroutine did not exit after context cancel")
	}
}
