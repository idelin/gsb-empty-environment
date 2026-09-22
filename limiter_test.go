package aimd

import (
	"sync"
	"testing"
	"time"
)

const debounce = 100 * time.Millisecond

func newTestLimiter(initial, min, max int, backoff float64) (*Limiter, *ManualClock) {
	clock := NewManualClock(time.Unix(0, 0))
	return NewLimiter(initial, min, max, backoff, debounce, clock), clock
}

func successSamples(n int) []Sample {
	samples := make([]Sample, n)
	for i := range samples {
		samples[i] = Sample{Latency: 10 * time.Millisecond}
	}
	return samples
}

// assertBlocked fails the test if anything is received on ch within d.
func assertBlocked(t *testing.T, ch <-chan struct{}, d time.Duration, msg string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("expected blocked, but unblocked: %s", msg)
	case <-time.After(d):
	}
}

// TestTimeoutDecreaseNoPreemptionNoBackfill covers the required scenario:
// limit 4 with 4 tasks running; a timeout cuts the limit to 2; the 4 running
// tasks finish normally and freed slots are NOT backfilled until inflight
// drops below 2.
func TestTimeoutDecreaseNoPreemptionNoBackfill(t *testing.T) {
	l, clock := newTestLimiter(4, 1, 16, 0.5)

	for i := 0; i < 4; i++ {
		l.Acquire()
	}
	if got := l.InFlight(); got != 4 {
		t.Fatalf("inflight = %d, want 4", got)
	}

	clock.Advance(debounce)
	l.Update([]Sample{{Latency: 900 * time.Millisecond, Timeout: true}})
	if got := l.Limit(); got != 2 {
		t.Fatalf("limit after timeout = %d, want 2", got)
	}

	// A 5th task must block: no slot while inflight(4) >= limit(2).
	acquired := make(chan struct{})
	go func() {
		l.Acquire()
		close(acquired)
	}()
	assertBlocked(t, acquired, 50*time.Millisecond, "inflight 4 >= limit 2")

	// Running tasks finish one by one; freed slots must not be backfilled.
	l.Release() // inflight 3
	assertBlocked(t, acquired, 50*time.Millisecond, "inflight 3 >= limit 2")
	l.Release() // inflight 2
	assertBlocked(t, acquired, 50*time.Millisecond, "inflight 2 >= limit 2")

	l.Release() // inflight 1 < limit 2: 5th task finally gets a slot.
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("5th task should acquire once inflight < limit")
	}
	l.Release()
	l.Release()
	if got := l.InFlight(); got != 0 {
		t.Fatalf("inflight = %d, want 0", got)
	}
}

// TestEmptySamplesNoAdjust: empty batches never adjust the limit and do not
// consume the debounce window.
func TestEmptySamplesNoAdjust(t *testing.T) {
	l, clock := newTestLimiter(4, 1, 16, 0.5)

	clock.Advance(debounce)
	l.Update(nil)
	l.Update([]Sample{})
	if got := l.Limit(); got != 4 {
		t.Fatalf("limit after empty updates = %d, want 4", got)
	}

	// The debounce window was not consumed: a real update applies immediately.
	l.Update(successSamples(3))
	if got := l.Limit(); got != 5 {
		t.Fatalf("limit after real update = %d, want 5", got)
	}
}

// TestDeterministicSequenceWithManualClock drives a fixed script of samples
// and clock advances, asserting the exact limit sequence: decrease, debounced
// (ignored) updates, then additive recovery.
func TestDeterministicSequenceWithManualClock(t *testing.T) {
	l, clock := newTestLimiter(4, 1, 6, 0.5)

	type step struct {
		advance time.Duration
		samples []Sample
		want    int
	}
	steps := []step{
		{debounce, []Sample{{Timeout: true}}, 2},                         // 4 * 0.5 = 2
		{0, successSamples(1), 2},                                        // debounced: ignored
		{debounce / 2, []Sample{{Timeout: true}}, 2},                     // debounced: ignored
		{debounce / 2, successSamples(2), 3},                             // window elapsed: +1
		{debounce, successSamples(1), 4},                                 // +1
		{debounce, []Sample{{Timeout: true}, {Latency: time.Second}}, 2}, // 4 * 0.5
		{debounce, successSamples(5), 3},                                 // recovery resumes
	}
	for i, s := range steps {
		clock.Advance(s.advance)
		l.Update(s.samples)
		if got := l.Limit(); got != s.want {
			t.Fatalf("step %d: limit = %d, want %d", i, got, s.want)
		}
	}
}

// TestAdditiveIncreaseCappedAtMax: successes add 1 per update, never above max.
func TestAdditiveIncreaseCappedAtMax(t *testing.T) {
	l, clock := newTestLimiter(2, 1, 4, 0.5)
	for _, want := range []int{3, 4, 4, 4} {
		clock.Advance(debounce)
		l.Update(successSamples(1))
		if got := l.Limit(); got != want {
			t.Fatalf("limit = %d, want %d", got, want)
		}
	}
}

// TestMultiplicativeDecreaseFlooredAtMin: decreases never go below minLimit.
func TestMultiplicativeDecreaseFlooredAtMin(t *testing.T) {
	l, clock := newTestLimiter(3, 1, 16, 0.5)
	for _, want := range []int{1, 1, 1} { // int(3*0.5)=1, then floored at min
		clock.Advance(debounce)
		l.Update([]Sample{{Timeout: true}})
		if got := l.Limit(); got != want {
			t.Fatalf("limit = %d, want %d", got, want)
		}
	}
}

// TestConcurrentAcquireReleaseUpdate hammers the limiter from many goroutines
// to surface data races under `go test -race`. Invariant: at the moment an
// Acquire returns, inflight must not exceed the limit observed then.
func TestConcurrentAcquireReleaseUpdate(t *testing.T) {
	l, clock := newTestLimiter(4, 1, 8, 0.5)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				l.Acquire()
				if l.InFlight() > l.Limit() {
					t.Errorf("inflight %d exceeds limit %d after Acquire", l.InFlight(), l.Limit())
				}
				l.Release()
				if j%10 == 0 {
					clock.Advance(debounce)
					if i%2 == 0 {
						l.Update(successSamples(1))
					} else {
						l.Update([]Sample{{Timeout: true}})
					}
				}
			}
		}(i)
	}
	wg.Wait()
	if got := l.InFlight(); got != 0 {
		t.Fatalf("inflight = %d, want 0", got)
	}
}
