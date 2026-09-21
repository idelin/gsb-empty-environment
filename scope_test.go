package scope

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Tests inject task functions, timing (via channels and explicit
// timeouts) and failures; the scope itself contains no timers, only
// standard sync primitives.

func mustClose(t *testing.T, ch <-chan struct{}, d time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func mustNotClose(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("%s closed unexpectedly", what)
	default:
	}
}

func TestAllSucceed(t *testing.T) {
	s := New(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		s.Go(func(ctx context.Context) error {
			defer wg.Done()
			return nil
		})
	}
	if err := s.Wait(); err != nil {
		t.Fatalf("Wait() = %v, want nil", err)
	}
	wg.Wait()
}

func TestFailureCancelsSibling(t *testing.T) {
	boom := errors.New("boom")
	siblingStarted := make(chan struct{})
	siblingCancelled := make(chan struct{})

	s := New(context.Background())
	s.Go(func(ctx context.Context) error {
		close(siblingStarted)
		select {
		case <-ctx.Done():
			close(siblingCancelled)
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return errors.New("sibling was not cancelled")
		}
	})
	s.Go(func(ctx context.Context) error {
		<-siblingStarted // ensure the sibling is running before failing
		return boom
	})

	err := s.Wait()
	if err == nil {
		t.Fatal("Wait() = nil, want failure")
	}
	mustClose(t, siblingCancelled, time.Second, "sibling cancellation")
	if !errors.Is(err, boom) {
		t.Fatalf("Wait() error %v does not match original cause %v", err, boom)
	}
}

func TestWaitBlocksUntilCleanupFinishes(t *testing.T) {
	boom := errors.New("boom")
	inCleanup := make(chan struct{})
	releaseCleanup := make(chan struct{})
	waitReturned := make(chan struct{})

	s := New(context.Background())
	s.Go(func(ctx context.Context) error { return boom })
	s.Go(func(ctx context.Context) error {
		<-ctx.Done() // cancelled by the sibling's failure
		close(inCleanup)
		<-releaseCleanup // simulate slow cleanup
		return nil
	})
	go func() {
		_ = s.Wait()
		close(waitReturned)
	}()

	mustClose(t, inCleanup, time.Second, "sibling to enter cleanup")
	// The sibling is still cleaning up, so Wait must not have returned.
	mustNotClose(t, waitReturned, "Wait")
	close(releaseCleanup)
	mustClose(t, waitReturned, time.Second, "Wait to return after cleanup")
}

func TestCancellationDoesNotOverrideFirstFailure(t *testing.T) {
	first := errors.New("first failure")
	second := errors.New("second failure")
	siblingStarted := make(chan struct{})

	s := New(context.Background())
	s.Go(func(ctx context.Context) error {
		close(siblingStarted)
		<-ctx.Done()
		return ctx.Err() // context.Canceled must not win
	})
	s.Go(func(ctx context.Context) error {
		<-siblingStarted
		return first
	})
	s.Go(func(ctx context.Context) error {
		<-ctx.Done()
		return second // a later real failure is preserved but not primary
	})

	err := s.Wait()
	var serr *Error
	if !errors.As(err, &serr) {
		t.Fatalf("Wait() = %T, want *Error", err)
	}
	if !errors.Is(serr.First, first) {
		t.Fatalf("First = %v, want %v", serr.First, first)
	}
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("aggregate %v lost an original cause", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("aggregate %v lost the sibling's cancellation", err)
	}
	if len(serr.All) != 3 {
		t.Fatalf("All has %d errors, want 3", len(serr.All))
	}
}

func TestNestedScopes(t *testing.T) {
	boom := errors.New("inner boom")
	innerRan := make(chan struct{}, 4)

	s := New(context.Background())
	s.Go(func(ctx context.Context) error {
		inner := New(ctx) // child scope owned by the parent's context
		for i := 0; i < 2; i++ {
			inner.Go(func(ctx context.Context) error {
				innerRan <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			})
		}
		inner.Go(func(ctx context.Context) error { return boom })
		return inner.Wait()
	})
	s.Go(func(ctx context.Context) error {
		<-ctx.Done() // cancelled when the inner failure propagates up
		return ctx.Err()
	})

	err := s.Wait()
	if !errors.Is(err, boom) {
		t.Fatalf("Wait() = %v, want cause %v", err, boom)
	}
	for i := 0; i < 2; i++ {
		mustClose(t, innerRan, time.Second, "grandchild to run")
	}
}

func TestConcurrentFailuresRepeatedCancelSafe(t *testing.T) {
	// Many children fail at once: every failure calls cancel, and
	// repeated cancellation must be safe (run with -race).
	const n = 32
	errs := make([]error, n)
	for i := range errs {
		errs[i] = errors.New("failure")
	}

	s := New(context.Background())
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		i := i
		s.Go(func(ctx context.Context) error {
			<-start
			return errs[i]
		})
	}
	s.Go(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	close(start)

	err := s.Wait()
	if err == nil {
		t.Fatal("Wait() = nil, want failure")
	}
	matched := 0
	for _, e := range errs {
		if errors.Is(err, e) {
			matched++
		}
	}
	if matched != n {
		t.Fatalf("aggregate preserved %d/%d failures", matched, n)
	}
}

func TestParentCancelPropagates(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	s := New(parent)
	done := make(chan struct{})
	s.Go(func(ctx context.Context) error {
		<-ctx.Done()
		close(done)
		return ctx.Err()
	})
	cancel()
	cancel() // repeated cancel of the parent is safe
	if err := s.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait() = %v, want context.Canceled", err)
	}
	mustClose(t, done, time.Second, "child cancellation")
}

func TestGoAfterWaitPanics(t *testing.T) {
	s := New(context.Background())
	if err := s.Wait(); err != nil {
		t.Fatalf("Wait() = %v, want nil", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Go after Wait did not panic")
		}
	}()
	s.Go(func(ctx context.Context) error { return nil })
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	s := New(context.Background())
	for i := 0; i < 4; i++ {
		s.Go(func(ctx context.Context) error {
			inner := New(ctx)
			for j := 0; j < 4; j++ {
				inner.Go(func(ctx context.Context) error {
					<-ctx.Done()
					return nil
				})
			}
			inner.Go(func(ctx context.Context) error { return errors.New("x") })
			return inner.Wait()
		})
	}
	_ = s.Wait()

	// Allow the runtime a moment to reap finished goroutines.
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
