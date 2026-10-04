package keyinit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// getN launches n concurrent Get calls and collects their results.
func getN(m *Manager, n int, key string, fn InitFunc) ([]any, []error) {
	vals := make([]any, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			vals[i], errs[i] = m.Get(context.Background(), key, fn)
		}(i)
	}
	wg.Wait()
	return vals, errs
}

func TestSharedRound(t *testing.T) {
	m := NewManager()
	var calls atomic.Int32
	fn := func(ctx context.Context) (any, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond) // widen the overlap window
		return "v", nil
	}
	vals, errs := getN(m, 16, "k", fn)
	for i := range vals {
		if errs[i] != nil || vals[i] != "v" {
			t.Fatalf("waiter %d: got (%v, %v), want (v, nil)", i, vals[i], errs[i])
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("fn ran %d times, want 1", got)
	}
	// Cached: subsequent calls must not re-run fn.
	v, err := m.Get(context.Background(), "k", fn)
	if err != nil || v != "v" || calls.Load() != 1 {
		t.Fatalf("cached get: got (%v, %v), calls=%d", v, err, calls.Load())
	}
}

func TestFailureBroadcastAndRetry(t *testing.T) {
	m := NewManager()
	boom := errors.New("boom")
	var calls atomic.Int32
	fn := func(ctx context.Context) (any, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return nil, boom
	}
	vals, errs := getN(m, 8, "k", fn)
	for i := range errs {
		if !errors.Is(errs[i], boom) || vals[i] != nil {
			t.Fatalf("waiter %d: got (%v, %v), want (nil, boom)", i, vals[i], errs[i])
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("fn ran %d times, want 1 (failure must be shared, not retried per waiter)", got)
	}
	// Failure is not cached: the next call starts a new round and may succeed.
	v, err := m.Get(context.Background(), "k", func(ctx context.Context) (any, error) {
		return "recovered", nil
	})
	if err != nil || v != "recovered" {
		t.Fatalf("retry: got (%v, %v), want (recovered, nil)", v, err)
	}
}

func TestCancelAndStaleRoundIsolation(t *testing.T) {
	m := NewManager()
	started := make(chan struct{})
	release := make(chan struct{})
	stale := func(ctx context.Context) (any, error) {
		close(started)
		select {
		case <-release:
			return "stale", nil // completes late, after cancellation
		case <-ctx.Done():
			return "stale", nil // even a ctx-aware fn may finish with a value
		}
	}

	// Round 1: starts and blocks.
	resCh := make(chan error, 1)
	go func() {
		_, err := m.Get(context.Background(), "k", stale)
		resCh <- err
	}()
	<-started

	if !m.Cancel("k") {
		t.Fatal("Cancel returned false, want true")
	}
	if err := <-resCh; !errors.Is(err, ErrCancelled) {
		t.Fatalf("waiter of cancelled round: got %v, want ErrCancelled", err)
	}
	if m.Cancel("k") {
		t.Fatal("second Cancel returned true, want false (no round in flight)")
	}

	// Round 2: a fresh round runs while round 1's fn is still blocked.
	var calls2 atomic.Int32
	v, err := m.Get(context.Background(), "k", func(ctx context.Context) (any, error) {
		calls2.Add(1)
		return "fresh", nil
	})
	if err != nil || v != "fresh" {
		t.Fatalf("new round: got (%v, %v), want (fresh, nil)", v, err)
	}

	// Round 1's fn completes late; its result must be discarded.
	close(release)
	time.Sleep(50 * time.Millisecond) // let the stale finish() run
	v, err = m.Get(context.Background(), "k", func(ctx context.Context) (any, error) {
		t.Fatal("fn must not run again: cache must hold round 2's value")
		return nil, nil
	})
	if err != nil || v != "fresh" {
		t.Fatalf("after stale completion: got (%v, %v), want (fresh, nil)", v, err)
	}
	if got := calls2.Load(); got != 1 {
		t.Fatalf("round 2 fn ran %d times, want 1", got)
	}
}

func TestSameKeyRecursion(t *testing.T) {
	m := NewManager()
	var fn InitFunc
	fn = func(ctx context.Context) (any, error) {
		_, err := m.Get(ctx, "k", fn)
		var rec *RecursionError
		if !errors.As(err, &rec) {
			t.Fatalf("nested Get: got %v, want *RecursionError", err)
		}
		if rec.Key != "k" {
			t.Fatalf("RecursionError.Key = %q, want %q", rec.Key, "k")
		}
		return nil, err // propagate: the outer round fails too
	}
	_, err := m.Get(context.Background(), "k", fn)
	var rec *RecursionError
	if !errors.As(err, &rec) {
		t.Fatalf("outer Get: got %v, want *RecursionError", err)
	}
}

func TestCrossKeyInitAllowed(t *testing.T) {
	m := NewManager()
	v, err := m.Get(context.Background(), "a", func(ctx context.Context) (any, error) {
		b, err := m.Get(ctx, "b", func(ctx context.Context) (any, error) {
			return 40, nil
		})
		if err != nil {
			return nil, err
		}
		return b.(int) + 2, nil
	})
	if err != nil || v != 42 {
		t.Fatalf("got (%v, %v), want (42, nil)", v, err)
	}
}

func TestCrossKeyCycleDetected(t *testing.T) {
	m := NewManager()
	var fnA, fnB InitFunc
	fnA = func(ctx context.Context) (any, error) {
		return m.Get(ctx, "b", fnB)
	}
	fnB = func(ctx context.Context) (any, error) {
		return m.Get(ctx, "a", fnA) // cycle: a -> b -> a
	}
	_, err := m.Get(context.Background(), "a", fnA)
	var rec *RecursionError
	if !errors.As(err, &rec) {
		t.Fatalf("got %v, want *RecursionError", err)
	}
	if rec.Key != "a" {
		t.Fatalf("RecursionError.Key = %q, want %q", rec.Key, "a")
	}
	if want := []string{"a", "b", "a"}; len(rec.Chain) != len(want) {
		t.Fatalf("chain = %v, want %v", rec.Chain, want)
	} else {
		for i := range want {
			if rec.Chain[i] != want[i] {
				t.Fatalf("chain = %v, want %v", rec.Chain, want)
			}
		}
	}
}

func TestInvalidate(t *testing.T) {
	m := NewManager()
	var calls atomic.Int32
	fn := func(ctx context.Context) (any, error) {
		return int(calls.Add(1)), nil
	}
	v, _ := m.Get(context.Background(), "k", fn)
	if v != 1 {
		t.Fatalf("first get: got %v, want 1", v)
	}
	m.Invalidate("k")
	v, _ = m.Get(context.Background(), "k", fn)
	if v != 2 {
		t.Fatalf("after invalidate: got %v, want 2 (re-initialized)", v)
	}
	v, _ = m.Get(context.Background(), "k", fn)
	if v != 2 || calls.Load() != 2 {
		t.Fatalf("cached again: got %v, calls=%d", v, calls.Load())
	}
}

func TestCallerContextCancelDoesNotAbortRound(t *testing.T) {
	m := NewManager()
	release := make(chan struct{})
	fn := func(ctx context.Context) (any, error) {
		<-release
		return "done", nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	impatient := make(chan error, 1)
	go func() {
		_, err := m.Get(ctx, "k", fn)
		impatient <- err
	}()
	patient := make(chan any, 1)
	go func() {
		v, _ := m.Get(context.Background(), "k", fn)
		patient <- v
	}()
	time.Sleep(20 * time.Millisecond) // both callers are waiting on the round
	cancel()
	if err := <-impatient; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller: got %v, want context.Canceled", err)
	}
	close(release)
	if v := <-patient; v != "done" {
		t.Fatalf("remaining waiter: got %v, want done (round must survive)", v)
	}
}
