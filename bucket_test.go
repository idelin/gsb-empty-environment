package tokenbucket

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a manually advanced, goroutine-safe monotonic clock.
type fakeClock struct{ ns atomic.Int64 }

func (c *fakeClock) now() int64              { return c.ns.Load() }
func (c *fakeClock) advance(d time.Duration) { c.ns.Add(int64(d)) }

// runWorkers starts workers goroutines, each calling Acquire perWorker times,
// and returns the total number of granted tokens.
func runWorkers(b *Bucket, workers, perWorker int) int64 {
	var granted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				if b.Acquire() {
					granted.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	return granted.Load()
}

// TestConcurrentAcquireExact is the acceptance test: 100 concurrent workers
// hammer Acquire with zero mutexes involved; the total number of granted
// tokens must match the theoretical supply exactly — no lost tokens, no
// double-granted tokens, no deadlock.
func TestConcurrentAcquireExact(t *testing.T) {
	clock := &fakeClock{}
	// 1000 tokens/s => 1 token per millisecond tick. Starts full at 5000.
	b := newBucket(1000, 5000, clock.now)

	const workers = 100
	const perWorker = 100 // total demand 10000 > supply

	// Phase 1: drain the initial 5000 tokens under maximal contention.
	if got := runWorkers(b, workers, perWorker); got != 5000 {
		t.Fatalf("phase 1: granted %d tokens, want exactly 5000", got)
	}
	if got := b.Available(); got != 0 {
		t.Fatalf("phase 1: bucket should be empty, has %d", got)
	}

	// Phase 2: advance the clock 2s => exactly 2000 new tokens; demand
	// (10000) again exceeds supply, so exactly 2000 may be granted.
	clock.advance(2 * time.Second)
	if got := runWorkers(b, workers, perWorker); got != 2000 {
		t.Fatalf("phase 2: granted %d tokens, want exactly 2000", got)
	}

	// Phase 3: partial refill — 1.5s => exactly 1500 tokens.
	clock.advance(1500 * time.Millisecond)
	if got := runWorkers(b, workers, perWorker); got != 1500 {
		t.Fatalf("phase 3: granted %d tokens, want exactly 1500", got)
	}
}

// TestSingleTokenHighContention: a single token contested by 100 workers
// must be granted to exactly one of them.
func TestSingleTokenHighContention(t *testing.T) {
	clock := &fakeClock{}
	b := newBucket(1, 1, clock.now) // 1 token/s, capacity 1, starts full

	if got := runWorkers(b, 100, 10); got != 1 {
		t.Fatalf("granted %d tokens, want exactly 1", got)
	}
}

// TestRefillMatchesTheory verifies refill arithmetic sequentially with a
// deterministic clock.
func TestRefillMatchesTheory(t *testing.T) {
	clock := &fakeClock{}
	b := newBucket(1000, 10, clock.now) // 1 token per ms, capacity 10

	// Drain the initial 10 tokens.
	for i := 0; i < 10; i++ {
		if !b.Acquire() {
			t.Fatalf("acquire %d of initial capacity failed", i+1)
		}
	}
	if b.Acquire() {
		t.Fatal("bucket should be empty after draining capacity")
	}

	// 5ms => exactly 5 tokens.
	clock.advance(5 * time.Millisecond)
	for i := 0; i < 5; i++ {
		if !b.Acquire() {
			t.Fatalf("acquire %d of 5 refilled tokens failed", i+1)
		}
	}
	if b.Acquire() {
		t.Fatal("only 5 tokens should have been refilled")
	}

	// A huge jump refills up to capacity only.
	clock.advance(time.Hour)
	if got := b.Available(); got != 10 {
		t.Fatalf("after 1h bucket should be capped at 10, has %d", got)
	}
	for i := 0; i < 10; i++ {
		if !b.Acquire() {
			t.Fatalf("acquire %d of 10 capped tokens failed", i+1)
		}
	}
	if b.Acquire() {
		t.Fatal("bucket should be empty after draining capped refill")
	}

	// Sub-tick elapsed time grants nothing and loses nothing.
	clock.advance(500 * time.Microsecond) // half a tick
	if b.Acquire() {
		t.Fatal("half a tick must not refill a token")
	}
	clock.advance(600 * time.Microsecond) // now 1.1 ticks total
	if !b.Acquire() {
		t.Fatal("one full tick elapsed, token must be granted")
	}
}

// TestConcurrentRealClock hammers the bucket with 100 workers against the
// real monotonic clock (run with -race). It asserts progress (no deadlock)
// and that grants never exceed the theoretical supply.
func TestConcurrentRealClock(t *testing.T) {
	const rate = 200_000.0 // tokens per second
	const capacity = 1000
	b := New(rate, capacity)

	const workers = 100
	const duration = 200 * time.Millisecond

	var granted atomic.Int64
	var wg sync.WaitGroup
	deadline := time.Now().Add(duration)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				if b.Acquire() {
					granted.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	// Upper bound: initial capacity plus every token that could have been
	// refilled since construction, plus one in-flight acquire per worker.
	elapsed := time.Since(processStart) // bucket was created after processStart
	theoreticalMax := int64(capacity) + int64(elapsed.Nanoseconds())*int64(rate)/1e9 + workers
	if got := granted.Load(); got == 0 {
		t.Fatal("no tokens granted: workers made no progress (deadlock?)")
	} else if got > theoreticalMax {
		t.Fatalf("granted %d tokens, theoretical max %d: over-grant detected", got, theoreticalMax)
	}
	t.Logf("granted %d tokens in %s with %d workers (max %d)", granted.Load(), duration, workers, theoreticalMax)
}

// TestAcquireNeverBlocksOnEmpty: failed acquires must not mutate state or
// block; a later refill still yields exactly the right amount.
func TestAcquireNeverBlocksOnEmpty(t *testing.T) {
	clock := &fakeClock{}
	b := newBucket(1000, 100, clock.now)
	runWorkers(b, 100, 10) // drain all 100, many failing acquires
	clock.advance(50 * time.Millisecond)
	if got := runWorkers(b, 100, 10); got != 50 {
		t.Fatalf("granted %d, want exactly 50 after failed-acquire storm", got)
	}
}
