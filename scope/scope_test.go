package taskscope

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	errFailA = errors.New("injected failure A")
	errFailB = errors.New("injected failure B")
)

// TestFailureCancelsSiblingsAndWaitsCleanup proves the three headline
// guarantees together: the first failure cancels the sibling, the sibling
// gets time to run its cleanup, and Wait does not return until that
// cleanup finishes. Failure timing and cleanup duration are injected.
func TestFailureCancelsSiblingsAndWaitsCleanup(t *testing.T) {
	const cleanup = 80 * time.Millisecond

	var siblingSawCancel atomic.Bool
	var siblingCleaned atomic.Bool

	s := New(nil)
	s.Go(func(ctx context.Context) error {
		select {
		case <-time.After(15 * time.Millisecond):
			return errFailA // injected first failure
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	s.Go(func(ctx context.Context) error {
		<-ctx.Done() // cancelled because of the sibling failure
		siblingSawCancel.Store(true)
		time.Sleep(cleanup) // injected cleanup time
		siblingCleaned.Store(true)
		return ctx.Err()
	})

	start := time.Now()
	err := s.Wait()
	elapsed := time.Since(start)

	if !errors.Is(err, errFailA) {
		t.Fatalf("Wait error = %v, want original cause errFailA", err)
	}
	if !siblingSawCancel.Load() {
		t.Fatal("sibling was not cancelled by the first failure")
	}
	if !siblingCleaned.Load() {
		t.Fatal("Wait returned before the sibling finished cleanup")
	}
	if elapsed < 60*time.Millisecond {
		t.Fatalf("Wait returned after %v, must wait for the whole cleanup", elapsed)
	}
}

// TestAggregateErrorsKeepsCauses verifies that every non-cancellation
// error survives, the first failure stays first, and cancellation noise is
// filtered out of the aggregate.
func TestAggregateErrorsKeepsCauses(t *testing.T) {
	s := New(nil)
	s.Go(func(ctx context.Context) error {
		time.Sleep(10 * time.Millisecond)
		return errFailA
	})
	s.Go(func(ctx context.Context) error {
		time.Sleep(40 * time.Millisecond)
		return errFailB
	})
	s.Go(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err() // cancellation result, must be dropped
	})

	err := s.Wait()
	if !errors.Is(err, errFailA) || !errors.Is(err, errFailB) {
		t.Fatalf("aggregate %v must expose both original causes", err)
	}

	msg := err.Error()
	posA, posB := strings.Index(msg, errFailA.Error()), strings.Index(msg, errFailB.Error())
	if posA < 0 || posB < 0 || posA > posB {
		t.Fatalf("first failure must precede later failure, got %q", msg)
	}
	if strings.Contains(msg, "context canceled") {
		t.Fatalf("cancellation must not pollute the aggregate, got %q", msg)
	}
	if cause := context.Cause(s.Ctx()); !errors.Is(cause, errFailA) {
		t.Fatalf("context cause = %v, want first failure errFailA", cause)
	}
}

// TestRepeatedCancelAndWaitSafe hammers Cancel from many goroutines and
// waits repeatedly; the scope must cancel exactly once semantically and
// never panic or deadlock.
func TestRepeatedCancelAndWaitSafe(t *testing.T) {
	s := New(nil)
	var active int64
	for range 16 {
		s.Go(func(ctx context.Context) error {
			atomic.AddInt64(&active, 1)
			<-ctx.Done()
			atomic.AddInt64(&active, -1)
			return ctx.Err()
		})
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				s.Cancel() // repeated, concurrent cancellation
			}
		}()
	}

	var waiters sync.WaitGroup
	for range 4 {
		waiters.Add(1)
		go func() {
			defer waiters.Done()
			if err := s.Wait(); err != nil {
				t.Errorf("pure cancellation must return nil, got %v", err)
			}
		}()
	}
	wg.Wait()
	waiters.Wait()

	if err := s.Wait(); err != nil {
		t.Fatalf("second Wait = %v, want nil", err)
	}
	if got := atomic.LoadInt64(&active); got != 0 {
		t.Fatalf("%d subtasks still running after Wait", got)
	}
}

// TestCancelBeforeGo verifies cancellation works before any subtask is
// spawned: subtasks start already cancelled and Wait returns nil.
func TestCancelBeforeGo(t *testing.T) {
	s := New(nil)
	s.Cancel()

	var started atomic.Bool
	s.Go(func(ctx context.Context) error {
		started.Store(true)
		<-ctx.Done()
		return ctx.Err()
	})
	if err := s.Wait(); err != nil {
		t.Fatalf("Wait after pre-cancel = %v, want nil", err)
	}
	if !started.Load() {
		t.Fatal("subtask still has to run and exit on its own")
	}
}

// TestAllSuccessNil checks the happy path: no failures means nil.
func TestAllSuccessNil(t *testing.T) {
	s := New(nil)
	var sum atomic.Int64
	for i := range 5 {
		s.Go(func(context.Context) error {
			time.Sleep(time.Duration(i) * time.Millisecond)
			sum.Add(1)
			return nil
		})
	}
	if err := s.Wait(); err != nil {
		t.Fatalf("Wait = %v, want nil", err)
	}
	if sum.Load() != 5 {
		t.Fatalf("ran %d subtasks, want 5", sum.Load())
	}
}

// TestNestedScopesPropagateAndDoNotLeak nests a child scope inside a
// parent subtask: parent cancellation propagates into the child, child
// cleanup is awaited, and after repeated failing runs the goroutine count
// returns to baseline (no leaked goroutines).
func TestNestedScopesPropagateAndDoNotLeak(t *testing.T) {
	// Single-run correctness: parent failure cancels the nested child.
	var childSawCancel, childCleaned atomic.Bool
	root := New(context.Background())
	root.Go(func(ctx context.Context) error {
		child := New(ctx) // child scope belongs to the parent scope
		child.Go(func(ctx context.Context) error {
			<-ctx.Done()
			childSawCancel.Store(true)
			time.Sleep(30 * time.Millisecond)
			childCleaned.Store(true)
			return ctx.Err()
		})
		child.Go(func(ctx context.Context) error {
			time.Sleep(10 * time.Millisecond)
			return errFailB // child failure cancels the child
		})
		return child.Wait() // parent subtask waits for child cleanup
	})
	root.Go(func(ctx context.Context) error {
		time.Sleep(5 * time.Millisecond)
		return errFailA // parent failure cancels the parent subtask's child too
	})
	if err := root.Wait(); !errors.Is(err, errFailA) || !errors.Is(err, errFailB) {
		t.Fatalf("nested aggregate %v must keep both causes", err)
	}
	if !childSawCancel.Load() || !childCleaned.Load() {
		t.Fatal("nested child must observe cancellation and finish cleanup")
	}

	// Leak check: many failing nested runs must return to the goroutine
	// baseline once Wait has drained every subtask.
	baseline := runtime.NumGoroutine()
	for range 20 {
		r := New(nil)
		r.Go(func(ctx context.Context) error {
			c := New(ctx)
			c.Go(func(ctx context.Context) error {
				<-ctx.Done()
				time.Sleep(time.Millisecond)
				return ctx.Err()
			})
			return c.Wait()
		})
		r.Go(func(context.Context) error { return fmt.Errorf("boom %d", 1) })
		if err := r.Wait(); err == nil {
			t.Fatal("expected an error from the failing nested run")
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= baseline+1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: baseline %d, now %d", baseline, runtime.NumGoroutine())
}

// TestEmptyScopeAndGoAfterWait covers the remaining lifecycle edges.
func TestEmptyScopeAndGoAfterWait(t *testing.T) {
	s := New(nil)
	if err := s.Wait(); err != nil {
		t.Fatalf("empty scope Wait = %v, want nil", err)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("Go after Wait must panic")
		}
	}()
	s.Go(func(context.Context) error { return nil })
}
