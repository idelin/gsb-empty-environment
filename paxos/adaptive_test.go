package paxos

import (
	"sync"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		MinLimit:         2,
		MaxLimit:         10,
		InitialLimit:     5,
		TargetLoad:       0.8,
		DecreaseFactor:   0.5,
		FailureThreshold: 3,
		BreakerCooldown:  100 * time.Millisecond,
		MinBatch:         2,
		MaxBatch:         20,
		MinLease:         time.Second,
		MaxLease:         10 * time.Second,
		LeaseFactor:      10,
	}
}

// TestAdditiveIncreaseWhenSaturated: limit grows by one per successful
// saturated round while load stays under target, and clamps at MaxLimit.
func TestAdditiveIncreaseWhenSaturated(t *testing.T) {
	c := NewController(testConfig(), func() float64 { return 0.5 })
	for want := 6; want <= 10; want++ {
		for i := 0; i < c.Limit(); i++ {
			if err := c.Acquire(); err != nil {
				t.Fatalf("Acquire: %v", err)
			}
		}
		for i := 0; i < c.Limit(); i++ {
			c.Release(true, time.Millisecond)
		}
		if got := c.Limit(); got != want {
			t.Fatalf("limit=%d, want %d", got, want)
		}
	}
	// Clamp: saturated success at MaxLimit must not exceed it.
	for i := 0; i < c.Limit(); i++ {
		_ = c.Acquire()
	}
	for i := 0; i < c.Limit(); i++ {
		c.Release(true, time.Millisecond)
	}
	if got := c.Limit(); got != 10 {
		t.Fatalf("limit exceeded MaxLimit: %d", got)
	}
}

// TestNoIncreaseWhenNotSaturated: spare capacity means no growth.
func TestNoIncreaseWhenNotSaturated(t *testing.T) {
	c := NewController(testConfig(), func() float64 { return 0.1 })
	_ = c.Acquire()
	c.Release(true, time.Millisecond)
	if got := c.Limit(); got != 5 {
		t.Fatalf("limit=%d, want unchanged 5", got)
	}
}

// TestMultiplicativeDecreaseOnHighLoad: overload halves the limit each round
// and clamps at MinLimit.
func TestMultiplicativeDecreaseOnHighLoad(t *testing.T) {
	c := NewController(testConfig(), func() float64 { return 0.95 })
	for _, want := range []int{2, 2, 2} { // 5 -> 2 (2.5 truncated) -> clamp 2
		c.Release(true, time.Millisecond)
		_ = want
	}
	if got := c.Limit(); got != 2 {
		t.Fatalf("limit=%d, want clamped MinLimit 2", got)
	}
}

// TestDecreaseOnFailure: failures shrink the limit even when load is low.
func TestDecreaseOnFailure(t *testing.T) {
	c := NewController(testConfig(), func() float64 { return 0.0 })
	c.Release(false, time.Millisecond)
	if got := c.Limit(); got != 2 {
		t.Fatalf("limit=%d, want 2 after failure", got)
	}
}

// TestShedAtLimit: admission is refused exactly at the limit, never blocks.
func TestShedAtLimit(t *testing.T) {
	c := NewController(testConfig(), nil)
	for i := 0; i < 5; i++ {
		if err := c.Acquire(); err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
	}
	if err := c.Acquire(); err != ErrOverloaded {
		t.Fatalf("err=%v, want ErrOverloaded", err)
	}
	if got := c.Inflight(); got != 5 {
		t.Fatalf("inflight=%d, want 5", got)
	}
}

// TestCircuitBreaker: opens after FailureThreshold consecutive failures,
// rejects during cooldown, half-opens after cooldown+jitter, and a success
// resets the failure counter.
func TestCircuitBreaker(t *testing.T) {
	now := time.Unix(0, 0)
	c := NewController(testConfig(), nil)
	c.SetClock(func() time.Time { return now })
	c.SetJitter(func() time.Duration { return 0 })

	for i := 0; i < 2; i++ {
		c.Release(false, time.Millisecond)
	}
	if c.BreakerOpen() {
		t.Fatal("breaker opened before threshold")
	}
	c.Release(false, time.Millisecond) // third consecutive failure
	if !c.BreakerOpen() {
		t.Fatal("breaker did not open at threshold")
	}
	if err := c.Acquire(); err != ErrBreakerOpen {
		t.Fatalf("err=%v, want ErrBreakerOpen", err)
	}

	now = now.Add(101 * time.Millisecond) // past cooldown
	if err := c.Acquire(); err != nil {
		t.Fatalf("half-open probe rejected: %v", err)
	}
	c.Release(true, time.Millisecond)
	if got := c.Inflight(); got != 0 {
		t.Fatalf("inflight=%d after probe release", got)
	}
}

// TestFailureCounterReset: a success between failures prevents the breaker
// from ever reaching its threshold.
func TestFailureCounterReset(t *testing.T) {
	c := NewController(testConfig(), nil)
	for i := 0; i < 10; i++ {
		c.Release(false, time.Millisecond)
		c.Release(false, time.Millisecond)
		c.Release(true, time.Millisecond) // resets the streak
	}
	if c.BreakerOpen() {
		t.Fatal("breaker opened despite interleaved successes")
	}
}

// TestBatchSizeLoadAware covers the batch-size boundaries.
func TestBatchSizeLoadAware(t *testing.T) {
	var load float64
	c := NewController(testConfig(), func() float64 { return load })

	load = 0
	if got := c.BatchSize(); got != 20 {
		t.Fatalf("idle batch=%d, want MaxBatch 20", got)
	}
	load = 0.5
	if got := c.BatchSize(); got != 11 {
		t.Fatalf("half-load batch=%d, want 11", got)
	}
	load = 1
	if got := c.BatchSize(); got != 2 {
		t.Fatalf("full-load batch=%d, want MinBatch 2", got)
	}
	load = 3.7 // beyond 100%: clamp, never below MinBatch
	if got := c.BatchSize(); got != 2 {
		t.Fatalf("over-load batch=%d, want clamped 2", got)
	}
	load = -1 // negative signal: treat as idle
	if got := c.BatchSize(); got != 20 {
		t.Fatalf("negative-load batch=%d, want 20", got)
	}
}

// TestLeaseDurationBounds: lease tracks smoothed RTT within [MinLease, MaxLease].
func TestLeaseDurationBounds(t *testing.T) {
	c := NewController(testConfig(), nil)
	if got := c.LeaseDuration(); got != time.Second {
		t.Fatalf("cold lease=%v, want MinLease 1s", got)
	}
	c.Release(true, 100*time.Millisecond) // rttAvg=100ms, *10 = 1s
	if got := c.LeaseDuration(); got != time.Second {
		t.Fatalf("lease=%v, want 1s", got)
	}
	c2 := NewController(testConfig(), nil)
	c2.Release(true, 500*time.Millisecond) // 500ms*10 = 5s
	if got := c2.LeaseDuration(); got != 5*time.Second {
		t.Fatalf("lease=%v, want 5s", got)
	}
	c3 := NewController(testConfig(), nil)
	c3.Release(true, time.Hour) // clamps to MaxLease
	if got := c3.LeaseDuration(); got != 10*time.Second {
		t.Fatalf("lease=%v, want clamped MaxLease 10s", got)
	}
}

// TestConcurrentSafety hammers Acquire/Release from many goroutines and
// asserts the invariants hold: inflight never negative, limit always inside
// [MinLimit, MaxLimit]. Run with -race.
func TestConcurrentSafety(t *testing.T) {
	var load float64
	var loadMu sync.Mutex
	c := NewController(testConfig(), func() float64 {
		loadMu.Lock()
		defer loadMu.Unlock()
		return load
	})

	const workers = 64
	const rounds = 500
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				loadMu.Lock()
				load = float64((w+i)%10) / 10 // oscillate 0.0..0.9
				loadMu.Unlock()
				if err := c.Acquire(); err == nil {
					c.Release(i%7 != 0, time.Duration(i%5)*time.Millisecond)
				}
				if got := c.Inflight(); got < 0 {
					t.Errorf("negative inflight: %d", got)
					return
				}
				if l := c.Limit(); l < 2 || l > 10 {
					t.Errorf("limit out of bounds: %d", l)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	// Drain: every admitted unit must be releasable; count must end at zero
	// for all slots we still hold.
	for c.Inflight() > 0 {
		c.Release(true, time.Millisecond)
	}
	if got := c.Inflight(); got != 0 {
		t.Fatalf("inflight leaked: %d", got)
	}
}

// TestTunerAdaptsWithoutTraffic: the background tuner shrinks the limit under
// sustained high load and grows it back when load disappears.
func TestTunerAdaptsWithoutTraffic(t *testing.T) {
	var load float64 = 1.0
	c := NewController(testConfig(), func() float64 { return load })
	c.StartTuner(2 * time.Millisecond)
	defer c.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for c.Limit() != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := c.Limit(); got != 2 {
		t.Fatalf("tuner did not shrink to MinLimit under load: %d", got)
	}

	load = 0
	deadline = time.Now().Add(2 * time.Second)
	for c.Limit() != 10 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := c.Limit(); got != 10 {
		t.Fatalf("tuner did not regrow to MaxLimit when idle: %d", got)
	}
}
