package scopex

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// blocking returns a task that runs until its context is canceled and then
// reports the cancellation (or nil, per the injected cleanup result).
func blocking(cleanupResult error) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		<-ctx.Done()
		return cleanupResult
	}
}

func TestAllSubtasksSucceed(t *testing.T) {
	var done atomic.Int32
	err := Run(context.Background(), func(s *Scope) error {
		for i := 0; i < 5; i++ {
			s.Go(func(ctx context.Context) error {
				done.Add(1)
				return nil
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if got := done.Load(); got != 5 {
		t.Fatalf("expected 5 completed subtasks, got %d", got)
	}
}

func TestFailureCancelsSiblings(t *testing.T) {
	injected := errors.New("injected failure")
	siblingSawCancel := make(chan struct{})

	s := New(context.Background())
	s.Go(func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			close(siblingSawCancel)
			return nil
		case <-time.After(5 * time.Second):
			t.Error("sibling was not canceled in time")
			return nil
		}
	})
	s.Go(func(ctx context.Context) error { return injected })

	<-siblingSawCancel
	if err := s.Wait(); !errors.Is(err, injected) {
		t.Fatalf("expected injected failure, got %v", err)
	}
}

// A failing subtask returns immediately while a sibling is still cleaning
// up; Wait must not return before the sibling's cleanup has finished.
func TestWaitBlocksUntilSiblingCleanupFinishes(t *testing.T) {
	injected := errors.New("boom")
	cleanupStarted := make(chan struct{})
	allowCleanup := make(chan struct{})
	cleanupDone := make(chan struct{})

	s := New(context.Background())
	s.Go(func(ctx context.Context) error {
		<-ctx.Done()
		close(cleanupStarted)
		<-allowCleanup // injected slow cleanup
		close(cleanupDone)
		return nil
	})
	s.Go(func(ctx context.Context) error { return injected })

	<-cleanupStarted
	waitReturned := make(chan error, 1)
	go func() { waitReturned <- s.Wait() }()

	select {
	case <-waitReturned:
		t.Fatal("Wait returned while a sibling was still cleaning up")
	case <-time.After(50 * time.Millisecond):
	}

	close(allowCleanup)
	<-cleanupDone
	if err := <-waitReturned; !errors.Is(err, injected) {
		t.Fatalf("expected injected failure, got %v", err)
	}
}

func TestFirstFailureWinsAndAggregates(t *testing.T) {
	first := errors.New("first failure")
	second := errors.New("second failure")
	release := make(chan struct{})

	s := New(context.Background())
	// Subtask A fails first; B is held back until A's failure is recorded.
	s.Go(func(ctx context.Context) error { return first })
	s.Go(func(ctx context.Context) error {
		<-release
		return second
	})

	deadline := time.After(5 * time.Second)
	for context.Cause(s.Context()) == nil {
		select {
		case <-deadline:
			t.Fatal("scope was never canceled")
		default:
		}
	}
	close(release)

	err := s.Wait()
	if !errors.Is(err, first) {
		t.Fatalf("aggregate does not contain first failure: %v", err)
	}
	if !errors.Is(err, second) {
		t.Fatalf("aggregate does not contain second failure: %v", err)
	}
	if got := context.Cause(s.Context()); !errors.Is(got, first) {
		t.Fatalf("context cause = %v, want first failure", got)
	}
}

// Cancellation reported by siblings during cleanup must not override or
// pollute the first failure.
func TestCancellationDoesNotOverrideFailure(t *testing.T) {
	injected := errors.New("real failure")

	s := New(context.Background())
	s.Go(blocking(context.Canceled)) // reports ctx.Err() on cleanup
	s.Go(blocking(nil))
	s.Go(func(ctx context.Context) error { return injected })

	err := s.Wait()
	if !errors.Is(err, injected) {
		t.Fatalf("expected injected failure, got %v", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation leaked into result: %v", err)
	}
}

func TestNestedScopesPropagateAndCleanUp(t *testing.T) {
	inner := errors.New("inner failure")
	innerSiblingCleaned := make(chan struct{})
	outerSiblingCleaned := make(chan struct{})

	s := New(context.Background())
	s.Scope(func(child *Scope) error {
		child.Go(func(ctx context.Context) error {
			<-ctx.Done()
			close(innerSiblingCleaned)
			return nil
		})
		child.Go(func(ctx context.Context) error { return inner })
		return nil
	})
	s.Go(func(ctx context.Context) error {
		<-ctx.Done()
		close(outerSiblingCleaned)
		return nil
	})

	err := s.Wait()
	if !errors.Is(err, inner) {
		t.Fatalf("nested failure did not propagate: %v", err)
	}
	select {
	case <-innerSiblingCleaned:
	default:
		t.Fatal("inner sibling was still running when Wait returned")
	}
	select {
	case <-outerSiblingCleaned:
	default:
		t.Fatal("outer sibling was still running when Wait returned")
	}
}

func TestNestedScopesDoNotLeakGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()

	err := Run(context.Background(), func(s *Scope) error {
		for i := 0; i < 4; i++ {
			s.Scope(func(child *Scope) error {
				for j := 0; j < 4; j++ {
					child.Go(blocking(nil))
				}
				child.Go(func(ctx context.Context) error {
					return errors.New("stop")
				})
				return nil
			})
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected failure from nested scopes")
	}

	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines leaked: before=%d after=%d", before, after)
	}
}

func TestRepeatedCancelIsSafe(t *testing.T) {
	s := New(context.Background())
	s.Go(blocking(nil))

	s.Cancel()
	s.Cancel()
	if err := s.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	// Cancel after Wait must remain a no-op and keep the original cause.
	s.Cancel()
	if got := context.Cause(s.Context()); !errors.Is(got, context.Canceled) {
		t.Fatalf("cause changed after repeated cancel: %v", got)
	}
}

func TestParentCancellationPropagates(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	s := New(parent)
	s.Go(blocking(context.Canceled))

	cancel()
	if err := s.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestAggregationPreservesEveryCause(t *testing.T) {
	errs := []error{errors.New("e1"), errors.New("e2"), errors.New("e3")}
	gate := make(chan struct{})

	s := New(context.Background())
	for _, injected := range errs {
		injected := injected
		s.Go(func(ctx context.Context) error {
			<-gate // all fail together, after every subtask is running
			return injected
		})
	}
	close(gate)

	err := s.Wait()
	for _, injected := range errs {
		if !errors.Is(err, injected) {
			t.Fatalf("aggregate lost cause %q: %v", injected, err)
		}
	}
}

func TestRunIncludesBodyError(t *testing.T) {
	injected := errors.New("body failure")
	err := Run(context.Background(), func(s *Scope) error {
		s.Go(blocking(nil))
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("expected body failure, got %v", err)
	}
}

// A parent failure propagates into nested scopes through their contexts;
// the nested scope's Wait reports that cause, and the parent must not
// aggregate the same failure twice.
func TestNestedScopeDoesNotDuplicateParentFailure(t *testing.T) {
	first := errors.New("outer failure")

	s := New(context.Background())
	s.Go(func(ctx context.Context) error { return first })
	s.Scope(func(child *Scope) error {
		child.Go(blocking(nil))
		return nil
	})

	err := s.Wait()
	if !errors.Is(err, first) {
		t.Fatalf("expected first failure, got %v", err)
	}
	if _, joined := err.(interface{ Unwrap() []error }); joined {
		t.Fatalf("failure was duplicated in a join: %v", err)
	}
}
