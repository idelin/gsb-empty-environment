package tokenbucket

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a manually advanced, goroutine-safe clock (milliseconds).
type fakeClock struct{ ms atomic.Int64 }

func (f *fakeClock) now() time.Time {
	return time.Unix(0, 0).Add(time.Duration(f.ms.Load()) * time.Millisecond)
}

func newFakeBucket(capacity int, ratePerSecond float64) (*TokenBucket, *fakeClock) {
	fc := &fakeClock{}
	b := newTokenBucket(capacity, ratePerSecond, fc.now, func(time.Duration) {})
	return b, fc
}

// TestTryAcquireExact: sequential exactness with a deterministic clock.
func TestTryAcquireExact(t *testing.T) {
	b, fc := newFakeBucket(5, 1000) // 5 burst, exactly 1 token/ms

	for i := 0; i < 5; i++ {
		if !b.TryAcquire() {
			t.Fatalf("acquire %d should succeed", i+1)
		}
	}
	if b.TryAcquire() {
		t.Fatal("bucket should be empty")
	}

	fc.ms.Add(3) // +3 tokens
	for i := 0; i < 3; i++ {
		if !b.TryAcquire() {
			t.Fatalf("acquire after refill %d should succeed", i+1)
		}
	}
	if b.TryAcquire() {
		t.Fatal("only 3 tokens should have refilled")
	}
	if got := b.Tokens(); got != 0 {
		t.Fatalf("Tokens() = %v, want 0", got)
	}
}

// TestCapacityClamp: tokens never exceed capacity no matter how much time passes.
func TestCapacityClamp(t *testing.T) {
	b, fc := newFakeBucket(7, 1000)
	fc.ms.Add(1_000_000) // ~16 minutes
	if got := b.Tokens(); got != 7 {
		t.Fatalf("Tokens() = %v, want clamped 7", got)
	}
	for i := 0; i < 7; i++ {
		if !b.TryAcquire() {
			t.Fatalf("acquire %d should succeed", i+1)
		}
	}
	if b.TryAcquire() {
		t.Fatal("bucket should be empty after draining capacity")
	}
}

// TestConcurrentExactTotal: 100 workers hammer TryAcquire with no mutex
// anywhere; the total number of granted tokens must match theory EXACTLY.
func TestConcurrentExactTotal(t *testing.T) {
	const (
		workers    = 100
		capacity   = 100  // must exceed advanceMs so refill is not clamped
		ratePerSec = 1000 // exactly 1 token per fake-ms
		advanceMs  = 50
		wantTotal  = capacity + advanceMs // 150
	)
	b, fc := newFakeBucket(capacity, ratePerSec)

	var granted atomic.Int64
	var stop atomic.Bool
	var wg sync.WaitGroup
	t.Cleanup(func() { stop.Store(true); wg.Wait() })
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if b.TryAcquire() {
					granted.Add(1)
				} else {
					runtime.Gosched()
				}
			}
		}()
	}

	waitFor := func(target int64) bool {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if granted.Load() >= target {
				return true
			}
			time.Sleep(time.Millisecond)
		}
		return false
	}

	// Phase 1: frozen clock -> exactly the initial capacity is granted.
	if !waitFor(capacity) {
		t.Fatalf("workers did not drain initial capacity, got %d", granted.Load())
	}
	time.Sleep(50 * time.Millisecond) // give workers time to over-grant if buggy
	if got := granted.Load(); got != capacity {
		t.Fatalf("frozen clock: granted = %d, want exactly %d", got, capacity)
	}

	// Phase 2: advance the clock once -> exactly advanceMs more tokens.
	fc.ms.Add(advanceMs)
	if !waitFor(wantTotal) {
		t.Fatalf("workers did not drain refilled tokens, got %d", granted.Load())
	}
	time.Sleep(50 * time.Millisecond)
	if got := granted.Load(); got != wantTotal {
		t.Fatalf("after refill: granted = %d, want exactly %d", got, wantTotal)
	}

	stop.Store(true)
	wg.Wait()
	if got := granted.Load(); got != wantTotal {
		t.Fatalf("final: granted = %d, want exactly %d (dirty data detected)", got, wantTotal)
	}
}

// TestConcurrentRealClockBounded: 100 workers, real clock, high frequency.
// Total grants must stay within the theoretical envelope and the observed
// token count must never leave [0, capacity].
func TestConcurrentRealClockBounded(t *testing.T) {
	const (
		workers    = 100
		capacity   = 50
		ratePerSec = 20_000
		duration   = 300 * time.Millisecond
	)
	b := NewTokenBucket(capacity, ratePerSec)

	var granted atomic.Int64
	var stop atomic.Bool
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if b.TryAcquire() {
					granted.Add(1)
				}
			}
		}()
	}
	// Observer: token count must never go negative or exceed capacity.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			if tok := b.Tokens(); tok < 0 || tok > capacity {
				t.Errorf("Tokens() out of range: %v", tok)
				return
			}
		}
	}()

	time.Sleep(duration)
	stop.Store(true)
	wg.Wait()
	elapsed := time.Since(start).Seconds()

	got := granted.Load()
	upper := int64(capacity) + int64(ratePerSec*elapsed) + ratePerSec/100 // 10ms slack
	lower := int64(ratePerSec * elapsed * 0.05)                           // loose sanity bound
	t.Logf("granted=%d elapsed=%.3fs theoretical=[%d, %d]", got, elapsed, lower, upper)
	if got > upper {
		t.Fatalf("granted %d exceeds theoretical maximum %d", got, upper)
	}
	if got < lower {
		t.Fatalf("granted %d suspiciously low (< %d), possible starvation", got, lower)
	}
}

// TestAcquireBlocks: blocking Acquire waits for a refill, then succeeds.
func TestAcquireBlocks(t *testing.T) {
	b := NewTokenBucket(1, 200) // 1 burst, 1 token per 5ms
	b.Acquire()                 // drains the initial token instantly

	start := time.Now()
	b.Acquire() // must wait ~5ms for the next token
	elapsed := time.Since(start)

	if elapsed < 3*time.Millisecond {
		t.Fatalf("Acquire returned too early (%v), did not wait for refill", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Acquire blocked too long (%v)", elapsed)
	}
}

// TestConcurrentBlockingAcquire: 100 workers blocking on Acquire; total
// granted over a fixed window must match the theoretical envelope.
func TestConcurrentBlockingAcquire(t *testing.T) {
	const (
		workers    = 100
		capacity   = 10
		ratePerSec = 5_000
		duration   = 200 * time.Millisecond
	)
	b := NewTokenBucket(capacity, ratePerSec)

	var granted atomic.Int64
	var stop atomic.Bool
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				b.Acquire()
				granted.Add(1)
			}
		}()
	}
	time.Sleep(duration)
	stop.Store(true)
	wg.Wait()
	elapsed := time.Since(start).Seconds()

	got := granted.Load()
	upper := int64(capacity) + int64(ratePerSec*elapsed) + workers // in-flight slack
	t.Logf("granted=%d elapsed=%.3fs theoretical upper=%d", got, elapsed, upper)
	if got > upper {
		t.Fatalf("granted %d exceeds theoretical maximum %d", got, upper)
	}
	if got == 0 {
		t.Fatal("no tokens granted, possible deadlock")
	}
}
