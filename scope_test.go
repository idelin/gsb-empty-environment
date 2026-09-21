package scope

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// TestFailureCancelsSiblings: one child's failure must cancel its siblings,
// and the cancellation cause must be the original failure.
func TestFailureCancelsSiblings(t *testing.T) {
	boom := errors.New("boom")
	siblingStarted := make(chan struct{})
	siblingCause := make(chan error, 1)

	s := New(context.Background())
	s.Go(func(ctx context.Context) error {
		close(siblingStarted)
		<-ctx.Done()
		siblingCause <- context.Cause(ctx)
		return ctx.Err()
	})
	s.Go(func(ctx context.Context) error {
		<-siblingStarted // ensure the sibling is parked before we fail
		return boom
	})

	err := s.Wait()
	if !errors.Is(err, boom) {
		t.Fatalf("Wait() = %v, want aggregate containing boom", err)
	}
	if cause := <-siblingCause; !errors.Is(cause, boom) {
		t.Fatalf("sibling cancellation cause = %v, want boom", cause)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("aggregate %v must not contain context.Canceled echo", err)
	}
}

// TestWaitBlocksUntilSiblingCleanup: while one child has failed and another
// is still cleaning up, Wait must not return early.
func TestWaitBlocksUntilSiblingCleanup(t *testing.T) {
	boom := errors.New("boom")
	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	cleanupDone := atomic.Bool{}

	s := New(context.Background())
	s.Go(func(ctx context.Context) error { return boom }) // fails immediately
	s.Go(func(ctx context.Context) error {
		<-ctx.Done()          // observe cancellation...
		close(cleanupStarted) // ...then start a slow cleanup
		<-releaseCleanup      // test injects the cleanup duration
		cleanupDone.Store(true)
		return ctx.Err()
	})

	waitReturned := make(chan error, 1)
	go func() { waitReturned <- s.Wait() }()

	<-cleanupStarted // child is now blocked inside its cleanup
	select {
	case <-waitReturned:
		t.Fatal("Wait returned while a sibling was still cleaning up")
	default:
	}

	close(releaseCleanup)
	if err := <-waitReturned; !errors.Is(err, boom) {
		t.Fatalf("Wait() = %v, want boom", err)
	}
	if !cleanupDone.Load() {
		t.Fatal("Wait returned before cleanup finished")
	}
}

// TestFirstFailurePreserved: with multiple failures the first one stays at
// the head of the aggregate and is the cancellation cause; cancellation
// echoes never overwrite it.
func TestFirstFailurePreserved(t *testing.T) {
	first := errors.New("first failure")
	second := errors.New("second failure")
	gate := make(chan struct{})

	s := New(context.Background())
	s.Go(func(ctx context.Context) error {
		<-gate
		return second // always arrives after first
	})
	s.Go(func(ctx context.Context) error {
		close(gate) // unblocks the second failure only after we return
		return first
	})
	// A sibling that only reports the cancellation echo.
	s.Go(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	err := s.Wait()
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("Wait() = %v, want both failures aggregated", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("aggregate %v must not contain context.Canceled echo", err)
	}
	if cause := context.Cause(s.Context()); !errors.Is(cause, first) {
		t.Fatalf("cancellation cause = %v, want first failure", cause)
	}
}

// TestNestedScopesNoLeak: nested scopes propagate failure and cancellation
// both ways, and no goroutine survives after the outer Wait returns.
func TestNestedScopesNoLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	innerBoom := errors.New("inner boom")
	innerCancelled := make(chan struct{})

	s := New(context.Background())
	s.Go(func(ctx context.Context) error {
		sub := New(ctx) // nested scope belongs to the parent
		sub.Go(func(ctx context.Context) error {
			<-ctx.Done()
			close(innerCancelled)
			return ctx.Err()
		})
		sub.Go(func(ctx context.Context) error { return innerBoom })
		return sub.Wait() // inner failure propagates to the parent scope
	})
	s.Go(func(ctx context.Context) error {
		<-ctx.Done() // outer sibling cancelled by the inner failure
		return ctx.Err()
	})

	err := s.Wait()
	if !errors.Is(err, innerBoom) {
		t.Fatalf("Wait() = %v, want inner boom", err)
	}
	select {
	case <-innerCancelled:
	default:
		t.Fatal("inner sibling was never cancelled")
	}

	waitForGoroutines(t, before)
}

// TestParentCancellation: cancelling the parent context cancels all
// children; Wait reports the cancellation.
func TestParentCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())

	s := New(parent)
	started := make(chan struct{})
	s.Go(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})

	<-started
	cancel()
	if err := s.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait() = %v, want context.Canceled", err)
	}
}

// TestConcurrentFailuresRaceSafe: many children failing at once must be
// safe (run with -race) and must still yield a single coherent aggregate.
func TestConcurrentFailuresRaceSafe(t *testing.T) {
	s := New(context.Background())
	start := make(chan struct{})
	for i := 0; i < 32; i++ {
		s.Go(func(ctx context.Context) error {
			<-start
			return errors.New("failure")
		})
	}
	for i := 0; i < 32; i++ {
		s.Go(func(ctx context.Context) error {
			<-start
			<-ctx.Done()
			return ctx.Err()
		})
	}
	close(start)

	err := s.Wait()
	if err == nil {
		t.Fatal("Wait() = nil, want failures")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("aggregate %v must not contain context.Canceled echo", err)
	}
}

// TestSuccessAndIdempotentWait: a clean run returns nil, and repeated Wait
// calls are safe and consistent.
func TestSuccessAndIdempotentWait(t *testing.T) {
	s := New(context.Background())
	done := atomic.Int32{}
	for i := 0; i < 8; i++ {
		s.Go(func(ctx context.Context) error {
			done.Add(1)
			return nil
		})
	}
	if err := s.Wait(); err != nil {
		t.Fatalf("Wait() = %v, want nil", err)
	}
	if err := s.Wait(); err != nil {
		t.Fatalf("second Wait() = %v, want nil", err)
	}
	if got := done.Load(); got != 8 {
		t.Fatalf("ran %d children, want 8", got)
	}
}

// waitForGoroutines polls until the goroutine count settles back to n.
func waitForGoroutines(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: %d still running, want <= %d", runtime.NumGoroutine(), n)
}
