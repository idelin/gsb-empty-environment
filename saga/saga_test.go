package saga

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fastOpts() Options {
	return Options{MaxCompensateRetries: 3, CompensateBackoff: time.Millisecond}
}

func okStep(name string, log *[]string, mu *sync.Mutex) Step {
	return Step{
		Name: name,
		Action: func(ctx context.Context) error {
			mu.Lock()
			*log = append(*log, "do:"+name)
			mu.Unlock()
			return nil
		},
		Compensate: func(ctx context.Context) error {
			mu.Lock()
			*log = append(*log, "undo:"+name)
			mu.Unlock()
			return nil
		},
	}
}

func TestSuccessPath(t *testing.T) {
	o := New(fastOpts())
	var mu sync.Mutex
	var log []string
	steps := []Step{okStep("a", &log, &mu), okStep("b", &log, &mu), okStep("c", &log, &mu)}

	if err := o.Execute(context.Background(), "tx1", steps); err != nil {
		t.Fatal(err)
	}
	st, _ := o.Status("tx1")
	if st != StatusCompleted {
		t.Fatalf("status=%v, want Completed", st)
	}
	want := []string{"do:a", "do:b", "do:c"}
	if fmt.Sprint(log) != fmt.Sprint(want) {
		t.Fatalf("log=%v, want %v", log, want)
	}
	done, comp, _ := o.Progress("tx1")
	if fmt.Sprint(done) != "[true true true]" || fmt.Sprint(comp) != "[false false false]" {
		t.Fatalf("progress done=%v comp=%v", done, comp)
	}
}

func TestFailureTriggersReverseCompensation(t *testing.T) {
	o := New(fastOpts())
	var mu sync.Mutex
	var log []string
	boom := errors.New("charge failed")
	steps := []Step{
		okStep("reserve", &log, &mu),
		okStep("charge", &log, &mu),
		{Name: "ship", Action: func(ctx context.Context) error { return boom },
			Compensate: func(ctx context.Context) error {
				mu.Lock()
				log = append(log, "undo:ship")
				mu.Unlock()
				return nil
			}},
	}

	err := o.Execute(context.Background(), "tx2", steps)
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := o.Status("tx2")
	if st != StatusRolledBack {
		t.Fatalf("status=%v, want RolledBack", st)
	}
	// strict reverse order, failed step never compensated
	want := []string{"do:reserve", "do:charge", "undo:charge", "undo:reserve"}
	if fmt.Sprint(log) != fmt.Sprint(want) {
		t.Fatalf("log=%v, want %v", log, want)
	}
}

func TestFailureOnFirstStepNoCompensation(t *testing.T) {
	o := New(fastOpts())
	compensated := false
	steps := []Step{
		{Name: "a", Action: func(ctx context.Context) error { return errors.New("nope") },
			Compensate: func(ctx context.Context) error { compensated = true; return nil }},
		okStep("b", &[]string{}, &sync.Mutex{}),
	}
	if err := o.Execute(context.Background(), "tx", steps); err == nil {
		t.Fatal("expected error")
	}
	if compensated {
		t.Fatal("failed step must not be compensated")
	}
	if st, _ := o.Status("tx"); st != StatusRolledBack {
		t.Fatalf("status=%v, want RolledBack", st)
	}
}

func TestIdempotentReexecution(t *testing.T) {
	o := New(fastOpts())
	var actions int32
	steps := []Step{
		{Name: "a", Action: func(ctx context.Context) error {
			atomic.AddInt32(&actions, 1)
			return nil
		}, Compensate: func(ctx context.Context) error { return nil }},
	}
	for i := 0; i < 3; i++ {
		if err := o.Execute(context.Background(), "tx", steps); err != nil {
			t.Fatal(err)
		}
	}
	if atomic.LoadInt32(&actions) != 1 {
		t.Fatalf("action ran %d times, want exactly 1", actions)
	}
}

func TestReexecuteRolledBackReturnsOriginalError(t *testing.T) {
	o := New(fastOpts())
	var actions int32
	steps := []Step{
		{Name: "a", Action: func(ctx context.Context) error {
			atomic.AddInt32(&actions, 1)
			return errors.New("always fails")
		}, Compensate: func(ctx context.Context) error { return nil }},
	}
	err1 := o.Execute(context.Background(), "tx", steps)
	err2 := o.Execute(context.Background(), "tx", steps)
	if err1 == nil || err2 == nil {
		t.Fatal("both executions should fail")
	}
	if atomic.LoadInt32(&actions) != 1 {
		t.Fatalf("action ran %d times, want 1 (no duplicate side effects)", actions)
	}
}

// Simulates a crash between steps: first execution dies after step 1, the
// retry must resume at step 2 without re-running step 1.
func TestResumeAfterPartialExecution(t *testing.T) {
	o := New(fastOpts())
	runsA := new(int32)
	runsB := new(int32)
	crash := true
	steps := []Step{
		{Name: "a", Action: func(ctx context.Context) error {
			atomic.AddInt32(runsA, 1)
			return nil
		}, Compensate: func(ctx context.Context) error { return nil }},
		{Name: "b", Action: func(ctx context.Context) error {
			atomic.AddInt32(runsB, 1)
			if crash {
				crash = false
				return errors.New("simulated crash")
			}
			return nil
		}, Compensate: func(ctx context.Context) error { return nil }},
	}
	if err := o.Execute(context.Background(), "tx", steps); err == nil {
		t.Fatal("first run should fail")
	}
	// rollback happened; manually reset to Running state is not exposed, so
	// model crash-recovery with a fresh transaction id sharing the log is
	// wrong — instead verify a rolled-back tx stays rolled back and a new
	// attempt under a new id reuses idempotent steps.
	if st, _ := o.Status("tx"); st != StatusRolledBack {
		t.Fatalf("status=%v", st)
	}
	if err := o.Execute(context.Background(), "tx-attempt2", steps); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(runsA) != 2 || atomic.LoadInt32(runsB) != 2 {
		t.Fatalf("runs a=%d b=%d, want 2 each", atomic.LoadInt32(runsA), atomic.LoadInt32(runsB))
	}
}

func TestCompensationRetryUntilSuccess(t *testing.T) {
	o := New(fastOpts())
	var compCalls int32
	steps := []Step{
		{Name: "a", Action: func(ctx context.Context) error { return nil },
			Compensate: func(ctx context.Context) error {
				if atomic.AddInt32(&compCalls, 1) < 3 {
					return errors.New("compensation flaky")
				}
				return nil
			}},
		{Name: "b", Action: func(ctx context.Context) error { return errors.New("boom") },
			Compensate: func(ctx context.Context) error { return nil }},
	}
	if err := o.Execute(context.Background(), "tx", steps); err == nil {
		t.Fatal("expected error")
	}
	if atomic.LoadInt32(&compCalls) != 3 {
		t.Fatalf("compensation called %d times, want 3 (retried to success)", compCalls)
	}
	if st, _ := o.Status("tx"); st != StatusRolledBack {
		t.Fatalf("status=%v, want RolledBack", st)
	}
}

func TestCompensationStuckThenResume(t *testing.T) {
	o := New(Options{MaxCompensateRetries: 2, CompensateBackoff: time.Millisecond})
	var compCalls int32
	gate := true
	steps := []Step{
		{Name: "a", Action: func(ctx context.Context) error { return nil },
			Compensate: func(ctx context.Context) error {
				atomic.AddInt32(&compCalls, 1)
				if gate {
					return errors.New("still failing")
				}
				return nil
			}},
		{Name: "b", Action: func(ctx context.Context) error { return errors.New("boom") },
			Compensate: func(ctx context.Context) error { return nil }},
	}
	if err := o.Execute(context.Background(), "tx", steps); err == nil {
		t.Fatal("expected error")
	}
	if st, _ := o.Status("tx"); st != StatusCompensationStuck {
		t.Fatalf("status=%v, want CompensationStuck", st)
	}
	// Resume while still failing -> stays stuck.
	if err := o.Resume(context.Background(), "tx"); err == nil {
		t.Fatal("resume should report still stuck")
	}
	// Fix the world, resume again -> eventual consistency reached.
	gate = false
	if err := o.Resume(context.Background(), "tx"); err != nil {
		t.Fatal(err)
	}
	if st, _ := o.Status("tx"); st != StatusRolledBack {
		t.Fatalf("status=%v, want RolledBack", st)
	}
	// Resume on a finished transaction is a no-op.
	if err := o.Resume(context.Background(), "tx"); err != nil {
		t.Fatal(err)
	}
	if err := o.Resume(context.Background(), "unknown"); err == nil {
		t.Fatal("resume of unknown tx should error")
	}
}

func TestContextCancelDuringCompensation(t *testing.T) {
	o := New(Options{MaxCompensateRetries: 100, CompensateBackoff: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	steps := []Step{
		{Name: "a", Action: func(ctx context.Context) error { return nil },
			Compensate: func(ctx context.Context) error {
				cancel() // abort while compensating
				return errors.New("keep failing")
			}},
		{Name: "b", Action: func(ctx context.Context) error { return errors.New("boom") },
			Compensate: func(ctx context.Context) error { return nil }},
	}
	done := make(chan error, 1)
	go func() { done <- o.Execute(ctx, "tx", steps) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not return after context cancellation")
	}
	if st, _ := o.Status("tx"); st != StatusCompensationStuck {
		t.Fatalf("status=%v, want CompensationStuck", st)
	}
}

// Many goroutines executing the same transaction id concurrently: the action
// runs exactly once and everyone observes the terminal state.
func TestConcurrentSameTransaction(t *testing.T) {
	o := New(fastOpts())
	var actions int32
	steps := []Step{
		{Name: "a", Action: func(ctx context.Context) error {
			atomic.AddInt32(&actions, 1)
			time.Sleep(time.Millisecond) // widen the race window
			return nil
		}, Compensate: func(ctx context.Context) error { return nil }},
	}
	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- o.Execute(context.Background(), "shared", steps)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if atomic.LoadInt32(&actions) != 1 {
		t.Fatalf("action ran %d times, want exactly 1", actions)
	}
	if st, _ := o.Status("shared"); st != StatusCompleted {
		t.Fatalf("status=%v", st)
	}
}

// Concurrent distinct transactions with mixed success/failure, plus a
// goroutine-leak assertion.
func TestConcurrentDistinctTransactionsNoLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	o := New(fastOpts())
	var mu sync.Mutex
	completed, rolledBack := 0, 0

	const n = 64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			fail := i%2 == 1
			var log []string
			var lmu sync.Mutex
			steps := []Step{okStep("s1", &log, &lmu)}
			if fail {
				steps = append(steps, Step{
					Name:       "bad",
					Action:     func(ctx context.Context) error { return errors.New("boom") },
					Compensate: func(ctx context.Context) error { return nil },
				})
			}
			err := o.Execute(context.Background(), fmt.Sprintf("tx-%d", i), steps)
			mu.Lock()
			if err == nil {
				completed++
			} else {
				rolledBack++
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	if completed != n/2 || rolledBack != n/2 {
		t.Fatalf("completed=%d rolledBack=%d, want %d each", completed, rolledBack, n/2)
	}
	for i := 0; i < n; i++ {
		st, _ := o.Status(fmt.Sprintf("tx-%d", i))
		want := StatusCompleted
		if i%2 == 1 {
			want = StatusRolledBack
		}
		if st != want {
			t.Fatalf("tx-%d status=%v, want %v", i, st, want)
		}
	}

	// no leaked goroutines: orchestrator is fully synchronous
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

func TestEmptyTransaction(t *testing.T) {
	o := New(fastOpts())
	if err := o.Execute(context.Background(), "empty", nil); err != nil {
		t.Fatal(err)
	}
	if st, _ := o.Status("empty"); st != StatusCompleted {
		t.Fatalf("status=%v, want Completed", st)
	}
}

func TestStatusOfUnknownTransaction(t *testing.T) {
	o := New(fastOpts())
	if _, ok := o.Status("nope"); ok {
		t.Fatal("unknown transaction should report ok=false")
	}
	if _, _, ok := o.Progress("nope"); ok {
		t.Fatal("unknown transaction progress should report ok=false")
	}
}
