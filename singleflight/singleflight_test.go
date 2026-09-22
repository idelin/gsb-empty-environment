package singleflight

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls until cond is true or the test deadline is hit. It keeps the
// tests deterministic without relying on arbitrary sleep lengths.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// keyedRunOnce returns a fn that starts only after release is closed, reports
// whether its context was cancelled on stopped, and increments runs.
func keyedRunOnce(t *testing.T, runs *int32, started chan<- struct{}, release <-chan struct{}, stopped chan<- bool) func(context.Context) (interface{}, error) {
	t.Helper()
	return func(ctx context.Context) (interface{}, error) {
		atomic.AddInt32(runs, 1)
		if started != nil {
			close(started)
		}
		<-release
		<-ctx.Done()
		if stopped != nil {
			stopped <- true
		}
		return "ok", nil
	}
}

func TestTenCallsExecuteOnce(t *testing.T) {
	var g Group
	var runs int32
	release := make(chan struct{})
	attached := 0
	g.afterAttach = func(string, bool) { attached++ }

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	vals := make([]interface{}, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			vals[i], errs[i] = g.Do(context.Background(), "k", func(ctx context.Context) (interface{}, error) {
				atomic.AddInt32(&runs, 1)
				<-release
				return 42, nil
			})
		}(i)
	}
	// Wait until all callers have coalesced onto the single in-flight call.
	waitFor(t, "all 10 callers attached", func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return attached == n
	})
	waitFor(t, "the single computation to start", func() bool {
		return atomic.LoadInt32(&runs) == 1
	})
	close(release)
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil || vals[i] != 42 {
			t.Fatalf("caller %d = (%v, %v), want (42, nil)", i, vals[i], errs[i])
		}
	}
	if got := atomic.LoadInt32(&runs); got != 1 {
		t.Fatalf("runs after = %d, want 1", got)
	}
}

func TestCancelNineDoesNotAffectSurvivor(t *testing.T) {
	var g Group
	var runs int32
	release := make(chan struct{})
	g.afterAttach = nil

	const n = 10
	ctxs := make([]context.Context, n)
	cancels := make([]context.CancelFunc, n)
	resVals := make([]interface{}, n)
	resErrs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)

	leaderStarted := make(chan struct{})
	fn := func(ctx context.Context) (interface{}, error) {
		atomic.AddInt32(&runs, 1)
		close(leaderStarted)
		<-release
		return "survivor", nil
	}
	for i := 0; i < n; i++ {
		ctxs[i], cancels[i] = context.WithCancel(context.Background())
		go func(i int) {
			defer wg.Done()
			resVals[i], resErrs[i] = g.Do(ctxs[i], "k", fn)
		}(i)
	}
	<-leaderStarted
	waitFor(t, "ten waiters attached", func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.m["k"] != nil && len(g.m["k"].waiters) == n
	})

	// Cancel nine, leaving caller 0 as the lone survivor.
	for i := 1; i < n; i++ {
		cancels[i]()
	}
	waitFor(t, "nine cancellations processed", func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return len(g.m["k"].waiters) == 1
	})

	if atomic.LoadInt32(&runs) != 1 {
		t.Fatalf("runs = %d, want 1", runs)
	}
	close(release)
	wg.Wait()

	if resErrs[0] != nil || resVals[0] != "survivor" {
		t.Fatalf("survivor = (%v, %v), want (survivor, nil)", resVals[0], resErrs[0])
	}
	for i := 1; i < n; i++ {
		if !errors.Is(resErrs[i], context.Canceled) {
			t.Fatalf("canceled caller %d err = %v, want context.Canceled", i, resErrs[i])
		}
	}
}

func TestAllCancelTerminatesComputation(t *testing.T) {
	var g Group
	var runs int32
	started, stopped := make(chan struct{}), make(chan struct{})

	// fn only ever returns because its context is cancelled when all waiters
	// leave; there is no other release path.
	fn := func(ctx context.Context) (interface{}, error) {
		atomic.AddInt32(&runs, 1)
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_, _ = g.Do(ctx, "k", fn)
	}()
	<-started

	// A fresh call while the old one is still "active" must coalesce onto it
	// and also be cancelable.
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() {
		_, err := g.Do(ctx2, "k", fn)
		done2 <- err
	}()
	waitFor(t, "second waiter attached", func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.m["k"] != nil && len(g.m["k"].waiters) == 2
	})

	cancel()
	cancel2()

	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("fn context was not cancelled after all waiters left")
	}
	if err := <-done2; !errors.Is(err, context.Canceled) {
		t.Fatalf("second caller err = %v, want context.Canceled", err)
	}
	waitFor(t, "map entry removed", func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		_, ok := g.m["k"]
		return !ok
	})
	if got := atomic.LoadInt32(&runs); got != 1 {
		t.Fatalf("runs = %d, want 1", got)
	}

	// A new call after full cancellation starts a brand new computation.
	release2 := make(chan struct{})
	started2 := make(chan struct{})
	go func() {
		_, _ = g.Do(context.Background(), "k", func(ctx context.Context) (interface{}, error) {
			atomic.AddInt32(&runs, 1)
			close(started2)
			<-release2
			return nil, nil
		})
	}()
	<-started2
	if got := atomic.LoadInt32(&runs); got != 2 {
		t.Fatalf("runs after re-call = %d, want 2", got)
	}
	close(release2)
}

func TestFailureIsNotCached(t *testing.T) {
	var g Group
	var runs int32
	boom := errors.New("boom")

	_, err := g.Do(context.Background(), "k", func(context.Context) (interface{}, error) {
		atomic.AddInt32(&runs, 1)
		return nil, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}

	// The failed state must not be reusable: the next call runs fn again
	// and may succeed.
	v, err := g.Do(context.Background(), "k", func(context.Context) (interface{}, error) {
		atomic.AddInt32(&runs, 1)
		return "recovered", nil
	})
	if err != nil || v != "recovered" {
		t.Fatalf("second call = (%v, %v), want (recovered, nil)", v, err)
	}
	if got := atomic.LoadInt32(&runs); got != 2 {
		t.Fatalf("runs = %d, want 2", got)
	}
}

func TestPanicBecomesErrorAndReleasesAll(t *testing.T) {
	var g Group
	var runs int32
	started := make(chan struct{})
	var startedOnce sync.Once
	release := make(chan struct{})

	const n = 3
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make([]error, n)
	fn := func(context.Context) (interface{}, error) {
		atomic.AddInt32(&runs, 1)
		startedOnce.Do(func() { close(started) })
		<-release
		panic("kaboom")
	}
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = g.Do(context.Background(), "k", fn)
		}(i)
	}
	<-started
	waitFor(t, "three waiters attached", func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.m["k"] != nil && len(g.m["k"].waiters) == n
	})
	close(release)
	wg.Wait()

	for i := 0; i < n; i++ {
		var pe *PanicError
		if !errors.As(errs[i], &pe) {
			t.Fatalf("waiter %d err = %T(%v), want *PanicError", i, errs[i], errs[i])
		}
		if pe.Value != "kaboom" || len(pe.Stack) == 0 {
			t.Fatalf("waiter %d PanicError = %+v, want value kaboom with stack", i, pe)
		}
	}

	// Panicked state is released, not cached: a new call runs fn again.
	ranAgain := make(chan struct{})
	go func() {
		_, _ = g.Do(context.Background(), "k", func(context.Context) (interface{}, error) {
			atomic.AddInt32(&runs, 1)
			close(ranAgain)
			return "after-panic", nil
		})
	}()
	select {
	case <-ranAgain:
	case <-time.After(3 * time.Second):
		t.Fatal("new call after panic did not start a fresh computation")
	}
}

func TestCompletionCannotHandOffToNewCaller(t *testing.T) {
	var g Group
	var runs int32

	// Block the leader between "remove from map under lock" and "deliver".
	enter := make(chan struct{})
	proceed := make(chan struct{})
	var hookOnce sync.Once
	g.afterCloseLocked = func(string) {
		hookOnce.Do(func() { close(enter) })
		<-proceed
	}

	leaderDone := make(chan struct{})
	var leaderVal interface{}
	var leaderErr error
	go func() {
		leaderVal, leaderErr = g.Do(context.Background(), "k", func(context.Context) (interface{}, error) {
			atomic.AddInt32(&runs, 1)
			return "first", nil
		})
		close(leaderDone)
	}()
	<-enter // map entry is already gone but no result was delivered.

	// A new caller at this instant must start its own run rather than attach
	// to the closed call and observe its error.
	newStarted := make(chan struct{})
	var newOnce sync.Once
	releaseSecond := make(chan struct{})
	newDone := make(chan struct{})
	var newVal interface{}
	var newErr error
	go func() {
		newVal, newErr = g.Do(context.Background(), "k", func(context.Context) (interface{}, error) {
			atomic.AddInt32(&runs, 1)
			newOnce.Do(func() { close(newStarted) })
			<-releaseSecond
			return "second", nil
		})
		close(newDone)
	}()
	<-newStarted // proves a fresh computation began, not reuse of the closed one

	close(proceed)
	<-leaderDone
	close(releaseSecond)
	<-newDone

	if leaderErr != nil || leaderVal != "first" {
		t.Fatalf("leader = (%v, %v), want (first, nil)", leaderVal, leaderErr)
	}
	if newErr != nil || newVal != "second" {
		t.Fatalf("new = (%v, %v), want (second, nil)", newVal, newErr)
	}
	if got := atomic.LoadInt32(&runs); got != 2 {
		t.Fatalf("runs = %d, want 2", got)
	}
}

func TestAlreadyCancelledContextDoesNotStartWork(t *testing.T) {
	var g Group
	var runs int32

	// In-flight call that never returns.
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_, _ = g.Do(context.Background(), "k", func(context.Context) (interface{}, error) {
			atomic.AddInt32(&runs, 1)
			close(started)
			<-release
			return nil, nil
		})
	}()
	<-started

	// A pre-cancelled context attaches and then leaves immediately; the
	// in-flight computation must be untouched as long as it has another
	// waiter.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.Do(ctx, "k", func(context.Context) (interface{}, error) {
		t.Fatal("fn must not run when coalescing onto an in-flight call")
		return nil, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	close(release)
}

// ExampleGroup shows ten concurrent callers for one key sharing a single
// execution of the underlying function.
func ExampleGroup() {
	var g Group
	var runs int32
	release := make(chan struct{})

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, _ = g.Do(context.Background(), "config", func(context.Context) (interface{}, error) {
				atomic.AddInt32(&runs, 1)
				<-release
				return "loaded", nil
			})
		}()
	}

	// Let the shared computation finish and everyone return.
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()
	fmt.Println("executions:", atomic.LoadInt32(&runs))
	// Output: executions: 1
}
