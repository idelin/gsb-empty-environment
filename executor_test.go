package taskexec

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls cond until it holds or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestNestedWaitNoDeadlock builds a deep chain with a single slot: each
// task spawns one child and waits for it, lending out its slot. With one
// slot total, any slot leak or missing re-acquire would deadlock.
func TestNestedWaitNoDeadlock(t *testing.T) {
	ex := New(1)
	const depth = 6

	var mu sync.Mutex
	var resumed []int

	var run func(ctx context.Context, w *Worker, level int) error
	run = func(ctx context.Context, w *Worker, level int) error {
		if level == depth {
			return nil
		}
		w.Spawn(func(ctx context.Context, cw *Worker) error {
			return run(ctx, cw, level+1)
		})
		if err := w.Wait(); err != nil {
			return err
		}
		// After Wait the task must hold a slot again.
		if got := ex.InFlight(); got != 1 {
			t.Errorf("level %d: InFlight after Wait = %d, want 1", level, got)
		}
		mu.Lock()
		resumed = append(resumed, level)
		mu.Unlock()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := ex.Run(ctx, func(ctx context.Context, w *Worker) error {
		return run(ctx, w, 0)
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(resumed) != depth {
		t.Fatalf("resumed %d levels, want %d", len(resumed), depth)
	}
	for i, lvl := range resumed {
		if want := depth - 1 - i; lvl != want {
			t.Fatalf("resume order %v, want deepest-first", resumed)
		}
	}
	if got := ex.InFlight(); got != 0 {
		t.Fatalf("InFlight after Run = %d, want 0", got)
	}
}

// TestPeakConcurrency verifies the slot bound is respected and reached:
// the first three children block until all three hold a slot, so the peak
// is exactly the limit, never more.
func TestPeakConcurrency(t *testing.T) {
	const limit = 3
	ex := New(limit)

	var started atomic.Int64
	release := make(chan struct{})
	var once sync.Once

	child := func(ctx context.Context, w *Worker) error {
		n := started.Add(1)
		if n == limit {
			once.Do(func() { close(release) })
		}
		if n <= limit {
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := ex.Run(ctx, func(ctx context.Context, w *Worker) error {
		for i := 0; i < 12; i++ {
			w.Spawn(child)
		}
		return w.Wait()
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := ex.Peak(); got != limit {
		t.Fatalf("Peak = %d, want %d", got, limit)
	}
	if got := started.Load(); got != 12 {
		t.Fatalf("started = %d, want 12", got)
	}
}

// TestCancellationReturnsSlots cancels a tree whose tasks block forever,
// then checks every slot is returned and the executor is reusable.
func TestCancellationReturnsSlots(t *testing.T) {
	ex := New(2)

	ctx, cancel := context.WithCancel(context.Background())
	blocking := func(ctx context.Context, w *Worker) error {
		<-ctx.Done()
		return ctx.Err()
	}

	done := make(chan error, 1)
	go func() {
		done <- ex.Run(ctx, func(ctx context.Context, w *Worker) error {
			for i := 0; i < 6; i++ {
				w.Spawn(blocking)
			}
			if err := w.Wait(); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		})
	}()

	waitFor(t, 5*time.Second, "2 slots in use", func() bool {
		return ex.InFlight() == 2
	})
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if got := ex.InFlight(); got != 0 {
		t.Fatalf("InFlight after cancel = %d, want 0", got)
	}

	// The executor must be fully reusable: all slots were returned.
	err := ex.Run(context.Background(), func(ctx context.Context, w *Worker) error {
		for i := 0; i < 4; i++ {
			w.Spawn(func(ctx context.Context, w *Worker) error { return nil })
		}
		return w.Wait()
	})
	if err != nil {
		t.Fatalf("reuse after cancel: %v", err)
	}
	if got := ex.InFlight(); got != 0 {
		t.Fatalf("InFlight after reuse = %d, want 0", got)
	}
}

// TestErrorPropagation checks that a grandchild's error travels up
// through each waiting ancestor to Run, and that sibling errors join.
func TestErrorPropagation(t *testing.T) {
	ex := New(2)
	errBoom := errors.New("boom")
	errLeft := errors.New("left")
	errRight := errors.New("right")

	err := ex.Run(context.Background(), func(ctx context.Context, w *Worker) error {
		w.Spawn(func(ctx context.Context, w *Worker) error {
			return errLeft
		})
		w.Spawn(func(ctx context.Context, w *Worker) error {
			return errRight
		})
		w.Spawn(func(ctx context.Context, w *Worker) error {
			// Middle child spawns a failing grandchild and propagates.
			w.Spawn(func(ctx context.Context, w *Worker) error {
				return errBoom
			})
			return w.Wait()
		})
		return w.Wait()
	})

	for _, want := range []error{errLeft, errRight, errBoom} {
		if !errors.Is(err, want) {
			t.Fatalf("Run error %v does not contain %v", err, want)
		}
	}
	if got := ex.InFlight(); got != 0 {
		t.Fatalf("InFlight after Run = %d, want 0", got)
	}
}

// TestChildErrorDoesNotBlockSiblings verifies that one failing child does
// not prevent its siblings from running to completion.
func TestChildErrorDoesNotBlockSiblings(t *testing.T) {
	ex := New(2)
	errBoom := errors.New("boom")
	var ok atomic.Int64

	err := ex.Run(context.Background(), func(ctx context.Context, w *Worker) error {
		w.Spawn(func(ctx context.Context, w *Worker) error { return errBoom })
		for i := 0; i < 5; i++ {
			w.Spawn(func(ctx context.Context, w *Worker) error {
				ok.Add(1)
				return nil
			})
		}
		return w.Wait()
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Run error = %v, want boom", err)
	}
	if got := ok.Load(); got != 5 {
		t.Fatalf("siblings completed = %d, want 5", got)
	}
}
