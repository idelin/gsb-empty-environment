package taskgroup

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// TestReverseOrderErrors uses a barrier so that every task is created
// before any of them reports, then releases them in reverse creation
// order: the error created last arrives first. Wait must still return
// the non-cancellation error with the smallest creation sequence.
func TestReverseOrderErrors(t *testing.T) {
	const n = 6
	errs := make([]error, n)
	for i := range errs {
		errs[i] = fmt.Errorf("error-%d", i)
	}

	g, ctx := New(context.Background())

	arrived := make(chan int, n)
	gates := make([]chan struct{}, n)
	for i := range gates {
		gates[i] = make(chan struct{})
	}
	recorded := make(chan uint64, n)
	reportHook = func(seq uint64, err error) { recorded <- seq }
	defer func() { reportHook = nil }()

	for i := 0; i < n; i++ {
		i := i
		g.Go(func(context.Context, *Tasks) error {
			arrived <- i
			<-gates[i]
			return errs[i]
		})
	}

	controller := make(chan struct{})
	go func() {
		defer close(controller)
		for i := 0; i < n; i++ {
			<-arrived
		}
		for i := n - 1; i >= 0; i-- {
			close(gates[i])
			if seq := <-recorded; seq != uint64(i) {
				t.Errorf("arrival %d: got seq %d, want %d", n-1-i, seq, i)
			}
		}
	}()

	if err := g.Wait(); err != errs[0] {
		t.Fatalf("Wait() = %v, want %v (smallest creation sequence)", err, errs[0])
	}
	<-controller
	if ctx.Err() != context.Canceled {
		t.Fatalf("ctx.Err() = %v, want context.Canceled", ctx.Err())
	}
}

// TestCancellationPropagates verifies that the first non-cancellation
// error cancels the group context, that cancellation errors never
// overwrite a business error, and that a later business error with a
// larger creation sequence does not replace the earlier one.
func TestCancellationPropagates(t *testing.T) {
	g, ctx := New(context.Background())

	errFirst := errors.New("first business error")
	errLater := errors.New("later business error")

	g.Go(func(ctx context.Context, _ *Tasks) error {
		<-ctx.Done()
		return ctx.Err()
	})
	g.Go(func(context.Context, *Tasks) error {
		return errFirst
	})
	g.Go(func(ctx context.Context, _ *Tasks) error {
		<-ctx.Done()
		return errLater
	})

	if err := g.Wait(); err != errFirst {
		t.Fatalf("Wait() = %v, want %v", err, errFirst)
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("ctx.Err() = %v, want context.Canceled", ctx.Err())
	}
}

// TestParentWaitsForDescendants blocks a grandchild and verifies that
// Wait does not return until the whole tree has exited.
func TestParentWaitsForDescendants(t *testing.T) {
	g, _ := New(context.Background())

	release := make(chan struct{})
	grandchildStarted := make(chan struct{})
	var exited int32

	g.Go(func(_ context.Context, t *Tasks) error {
		t.Go(func(_ context.Context, t *Tasks) error {
			t.Go(func(context.Context, *Tasks) error {
				close(grandchildStarted)
				<-release
				atomic.AddInt32(&exited, 1)
				return nil
			})
			atomic.AddInt32(&exited, 1)
			return nil
		})
		atomic.AddInt32(&exited, 1)
		return nil
	})

	waitReturned := make(chan error, 1)
	go func() { waitReturned <- g.Wait() }()

	<-grandchildStarted
	select {
	case err := <-waitReturned:
		t.Fatalf("Wait returned %v before all descendants exited", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	if err := <-waitReturned; err != nil {
		t.Fatalf("Wait() = %v, want nil", err)
	}
	if got := atomic.LoadInt32(&exited); got != 3 {
		t.Fatalf("exited = %d, want 3", got)
	}
}

// TestSpawnAfterEndPanics verifies that no subtask can be created once
// the group has ended, neither at the top level nor through a retained
// subtask handle.
func TestSpawnAfterEndPanics(t *testing.T) {
	g, _ := New(context.Background())

	var held *Tasks
	g.Go(func(_ context.Context, t *Tasks) error {
		held = t
		return nil
	})
	if err := g.Wait(); err != nil {
		t.Fatalf("Wait() = %v, want nil", err)
	}

	mustPanic(t, func() {
		g.Go(func(context.Context, *Tasks) error { return nil })
	})
	mustPanic(t, func() {
		held.Go(func(context.Context, *Tasks) error { return nil })
	})
}

// TestExternalCancellation verifies that when no business error exists,
// Wait surfaces the cancellation of the parent context.
func TestExternalCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	g, _ := New(parent)

	ready := make(chan struct{})
	g.Go(func(ctx context.Context, _ *Tasks) error {
		close(ready)
		<-ctx.Done()
		return ctx.Err()
	})

	<-ready
	cancel()
	if err := g.Wait(); err != context.Canceled {
		t.Fatalf("Wait() = %v, want context.Canceled", err)
	}
}

// TestAllSucceed verifies the nil result of a clean tree run.
func TestAllSucceed(t *testing.T) {
	g, _ := New(context.Background())
	for i := 0; i < 3; i++ {
		g.Go(func(_ context.Context, t *Tasks) error {
			t.Go(func(context.Context, *Tasks) error { return nil })
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		t.Fatalf("Wait() = %v, want nil", err)
	}
}

// TestNoGoroutineLeak runs a full tree and verifies that no goroutine
// outlives Wait.
func TestNoGoroutineLeak(t *testing.T) {
	baseline := runtime.NumGoroutine()

	errBoom := errors.New("boom")
	g, _ := New(context.Background())
	for i := 0; i < 4; i++ {
		i := i
		g.Go(func(_ context.Context, t *Tasks) error {
			for j := 0; j < 4; j++ {
				j := j
				t.Go(func(context.Context, *Tasks) error {
					if i == 0 && j == 0 {
						return errBoom
					}
					return nil
				})
			}
			return nil
		})
	}
	if err := g.Wait(); err != errBoom {
		t.Fatalf("Wait() = %v, want %v", err, errBoom)
	}

	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if now := runtime.NumGoroutine(); now > baseline {
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		t.Fatalf("goroutine leak: baseline %d, now %d\n%s", baseline, now, buf[:n])
	}
}

func mustPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	fn()
}
