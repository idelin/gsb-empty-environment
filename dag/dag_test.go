package dag

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

func noop(context.Context) error { return nil }

func fail(context.Context) error { return errBoom }

func sleepTask(d time.Duration) TaskFunc {
	return func(context.Context) error {
		time.Sleep(d)
		return nil
	}
}

func runWithTimeout(t *testing.T, s *Scheduler) *Report {
	t.Helper()
	done := make(chan struct{})
	var report *Report
	go func() {
		report, _ = s.Run(context.Background())
		close(done)
	}()
	select {
	case <-done:
		return report
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler deadlocked: Run did not return")
		return nil
	}
}

// A diamond converges on a single task that must run exactly once, and the
// whole graph succeeds.
func TestDiamondRunsOnce(t *testing.T) {
	s := New()
	var endRuns int32
	var mu sync.Mutex
	var order []string
	record := func(id string) TaskFunc {
		return func(context.Context) error {
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			if id == "d" {
				atomic.AddInt32(&endRuns, 1)
			}
			return nil
		}
	}
	mustAdd(t, s, "a", record("a"))
	mustAdd(t, s, "b", record("b"), "a")
	mustAdd(t, s, "c", record("c"), "a")
	mustAdd(t, s, "d", record("d"), "b", "c")

	r := runWithTimeout(t, s)
	if got := atomic.LoadInt32(&endRuns); got != 1 {
		t.Fatalf("diamond end node ran %d times, want 1", got)
	}
	if got := r.Succeeded(); len(got) != 4 || got[3] != "d" {
		t.Fatalf("unexpected succeeded tasks: %v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 4 {
		t.Fatalf("ran %d tasks, want 4; order=%v", len(order), order)
	}
	if order[0] != "a" || order[len(order)-1] != "d" {
		t.Fatalf("bad execution order %v", order)
	}
}

// A failed task blocks every transitive dependent, while an independent
// branch keeps running to completion.
func TestFailureBlocksDependentsNotOthers(t *testing.T) {
	s := New()
	ok1 := make(chan struct{})
	mustAdd(t, s, "bad", fail)
	mustAdd(t, s, "child", noop, "bad")
	mustAdd(t, s, "grandchild", noop, "child")
	mustAdd(t, s, "indep", func(context.Context) error {
		close(ok1)
		return nil
	})

	r := runWithTimeout(t, s)
	if got := r.Failed(); len(got) != 1 || got[0] != "bad" {
		t.Fatalf("failed=%v, want [bad]", got)
	}
	if got := r.Blocked(); len(got) != 2 {
		t.Fatalf("blocked=%v, want [child grandchild]", got)
	}
	for _, id := range []string{"child", "grandchild"} {
		st, ok := r.StatusOf(id)
		if !ok || st != Blocked {
			t.Fatalf("%s status=%v ok=%v, want BLOCKED", id, st, ok)
		}
	}
	if st, _ := r.StatusOf("indep"); st != Succeeded {
		t.Fatalf("indep status=%v, want SUCCEEDED", st)
	}
	if r.Err("bad") == nil || !errors.Is(r.Err("bad"), errBoom) {
		t.Fatalf("bad err=%v, want errBoom", r.Err("bad"))
	}
	select {
	case <-ok1:
	default:
		t.Fatal("independent branch never ran")
	}
}

// Independent ready tasks execute concurrently: two 80ms sleeps in parallel
// finish in roughly one sleep, not two.
func TestIndependentBranchesConcurrent(t *testing.T) {
	s := New()
	mustAdd(t, s, "x", sleepTask(80*time.Millisecond))
	mustAdd(t, s, "y", sleepTask(80*time.Millisecond))
	mustAdd(t, s, "z", noop, "x", "y")

	start := time.Now()
	r := runWithTimeout(t, s)
	elapsed := time.Since(start)

	if got := len(r.Succeeded()); got != 3 {
		t.Fatalf("succeeded=%v, want all 3", r.Succeeded())
	}
	if elapsed > 140*time.Millisecond {
		t.Fatalf("tasks ran serially: elapsed=%v", elapsed)
	}
}

// A task only starts after ALL dependencies succeed.
func TestWaitsForAllDependencies(t *testing.T) {
	s := New()
	var midStart int64
	mustAdd(t, s, "a", sleepTask(60*time.Millisecond))
	mustAdd(t, s, "b", sleepTask(60*time.Millisecond))
	mustAdd(t, s, "c", func(context.Context) error {
		midStart = time.Now().UnixNano()
		return nil
	}, "a", "b")

	base := time.Now().UnixNano()
	r := runWithTimeout(t, s)
	if len(r.Succeeded()) != 3 {
		t.Fatalf("succeeded=%v", r.Succeeded())
	}
	if d := time.Duration(midStart - base); d < 50*time.Millisecond {
		t.Fatalf("c started after %v, want >= 60ms (both deps)", d)
	}
}

// Cycles present at Run time are rejected before any task runs, returning the
// node chain that closes the cycle.
func TestCycleDetectedBeforeRun(t *testing.T) {
	s := New()
	ran := int32(0)
	mark := func(context.Context) error { atomic.AddInt32(&ran, 1); return nil }
	// Forward reference: a is declared with a dependency on c before c is
	// registered; Add accepts this, so the cycle only surfaces in Run's
	// pre-run graph check.
	mustAdd(t, s, "a", mark, "c")
	mustAdd(t, s, "b", mark, "a")
	mustAdd(t, s, "c", mark, "b")

	done := make(chan error, 1)
	go func() { _, err := s.Run(context.Background()); done <- err }()
	select {
	case err := <-done:
		var ce *CycleError
		if !errors.As(err, &ce) {
			t.Fatalf("err=%v, want *CycleError", err)
		}
		if len(ce.Chain) < 3 || ce.Chain[0] != ce.Chain[len(ce.Chain)-1] {
			t.Fatalf("bad cycle chain %v", ce.Chain)
		}
		if !strings.Contains(err.Error(), "cycle detected") {
			t.Fatalf("error message %q lacks cycle description", err.Error())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run hung on cyclic graph")
	}
	if got := atomic.LoadInt32(&ran); got != 0 {
		t.Fatalf("%d tasks ran despite cycle, want 0", got)
	}
}

// A self-loop is a cycle too.
func TestSelfLoopIsCycle(t *testing.T) {
	s := New()
	mustAdd(t, s, "a", noop)
	if err := s.AddDependency("a", "a"); err == nil {
		t.Fatal("expected cycle error for self dependency")
	}
}

// A dependency edge added while the scheduler runs is honored when the source
// is still pending.
func TestDynamicDependencyHonoredAtRuntime(t *testing.T) {
	s := New()
	gateA := make(chan struct{})
	gateRoot := make(chan struct{})
	aStarted := make(chan struct{})
	rootStarted := make(chan struct{})
	bRan := make(chan struct{})
	mustAdd(t, s, "a", func(context.Context) error {
		close(aStarted)
		<-gateA
		return nil
	})
	mustAdd(t, s, "root", func(context.Context) error {
		close(rootStarted)
		<-gateRoot
		return nil
	})
	// b already waits for root; while root and a are both running, b stays
	// pending, giving a deterministic window to add the extra edge b -> a.
	mustAdd(t, s, "b", func(context.Context) error {
		close(bRan)
		return nil
	}, "root")

	wait := make(chan struct{})
	go func() {
		_, _ = s.Run(context.Background())
		close(wait)
	}()
	<-aStarted
	<-rootStarted
	if err := s.AddDependency("b", "a"); err != nil {
		t.Fatalf("dynamic AddDependency failed: %v", err)
	}
	close(gateRoot) // root finishes: b still needs a
	select {
	case <-bRan:
		t.Fatal("b ran before its dynamically added dependency a finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(gateA)
	select {
	case <-bRan:
	case <-time.After(5 * time.Second):
		t.Fatal("b never ran after dependency finished")
	}
	select {
	case <-wait:
	case <-time.After(5 * time.Second):
		t.Fatal("Run hung")
	}
}

// An edge cannot be attached to a task that has already started.
func TestDynamicDependencyRejectedAfterStart(t *testing.T) {
	s := New()
	inA := make(chan struct{})
	release := make(chan struct{})
	mustAdd(t, s, "a", func(context.Context) error {
		close(inA)
		<-release
		return nil
	})
	mustAdd(t, s, "b", noop)

	go func() { _, _ = s.Run(context.Background()) }()
	<-inA
	err := s.AddDependency("a", "b")
	if !errors.Is(err, ErrTaskNotPending) {
		t.Fatalf("err=%v, want ErrTaskNotPending", err)
	}
	close(release)
}

// A dynamically added edge that would close a cycle is rejected at runtime,
// and the scheduler keeps draining the still-acyclic graph without hanging.
func TestDynamicCycleRejectedAtRuntime(t *testing.T) {
	s := New()
	gate := make(chan struct{})
	rootStarted := make(chan struct{})
	// root stays Running during the attempt; a and b are both Pending.
	mustAdd(t, s, "root", func(context.Context) error {
		close(rootStarted)
		<-gate
		return nil
	})
	mustAdd(t, s, "a", noop, "root")
	mustAdd(t, s, "b", noop, "root")

	wait := make(chan struct{})
	go func() {
		_, _ = s.Run(context.Background())
		close(wait)
	}()
	<-rootStarted // a, b are both Pending: a safe window to mutate edges
	if err := s.AddDependency("b", "a"); err != nil {
		t.Fatalf("AddDependency b->a: %v", err)
	}
	err := s.AddDependency("a", "b") // would make a <-> b
	var ce *CycleError
	if !errors.As(err, &ce) {
		close(gate)
		t.Fatalf("err=%v, want *CycleError", err)
	}
	if len(ce.Chain) < 3 || ce.Chain[0] != ce.Chain[len(ce.Chain)-1] {
		close(gate)
		t.Fatalf("bad cycle chain %v", ce.Chain)
	}
	close(gate)
	select {
	case <-wait:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler hung after rejecting a dynamic cycle")
	}
}

// Unknown node references are reported clearly.
func TestUnknownNodeAndMissingDep(t *testing.T) {
	s := New()
	if err := s.AddDependency("ghost", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
	mustAdd(t, s, "a", noop, "missing")
	_, err := s.Run(context.Background())
	if !errors.Is(err, ErrMissingDependency) {
		t.Fatalf("err=%v, want ErrMissingDependency", err)
	}
}

// After a missing-dependency error the lock must be released (subsequent
// calls must not deadlock) and the scheduler must not be marked as started.
func TestMissingDependencyReleasesLock(t *testing.T) {
	s := New()
	mustAdd(t, s, "a", noop, "missing")
	done := make(chan error, 1)
	go func() {
		_, err := s.Run(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrMissingDependency) {
			t.Fatalf("err=%v, want ErrMissingDependency", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run hung")
	}
	ch := make(chan error, 1)
	go func() { ch <- s.AddDependency("a", "a") }()
	select {
	case <-ch: // returned (self-loop cycle error); the lock was released
	case <-time.After(5 * time.Second):
		t.Fatal("lock held after failed Run")
	}
	if err := s.Add("b", noop); !errors.Is(err, nil) {
		t.Fatalf("Add after failed Run should still be allowed, got %v", err)
	}
}

// Duplicate registration and duplicate edges behave predictably.
func TestDuplicateTaskAndEdge(t *testing.T) {
	s := New()
	if err := s.Add("a", noop); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("a", noop); !errors.Is(err, ErrTaskExists) {
		t.Fatalf("err=%v, want ErrTaskExists", err)
	}
	mustAdd(t, s, "b", noop)
	if err := s.AddDependency("b", "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDependency("b", "a"); err != nil {
		t.Fatalf("duplicate edge should be a no-op, got %v", err)
	}
	r := runWithTimeout(t, s)
	if len(r.Succeeded()) != 2 {
		t.Fatalf("succeeded=%v", r.Succeeded())
	}
}

// A panicking task counts as failure and blocks its dependents.
func TestPanicBecomesFailure(t *testing.T) {
	s := New()
	mustAdd(t, s, "p", func(context.Context) error { panic("kaboom") })
	mustAdd(t, s, "q", noop, "p")
	r := runWithTimeout(t, s)
	if st, _ := r.StatusOf("p"); st != Failed {
		t.Fatalf("p=%v, want FAILED", st)
	}
	if st, _ := r.StatusOf("q"); st != Blocked {
		t.Fatalf("q=%v, want BLOCKED", st)
	}
}

// Adding tasks after Run started is rejected.
func TestAddAfterRunRejected(t *testing.T) {
	s := New()
	in := make(chan struct{})
	release := make(chan struct{})
	mustAdd(t, s, "a", func(context.Context) error {
		close(in)
		<-release
		return nil
	})
	go func() { _, _ = s.Run(context.Background()) }()
	<-in
	if err := s.Add("late", noop); !errors.Is(err, ErrSchedulerStarted) {
		t.Fatalf("err=%v, want ErrSchedulerStarted", err)
	}
	close(release)
}

// Concurrent AddDependency / Run / completion traffic must stay race-clean.
func TestConcurrentDynamicEdges(t *testing.T) {
	s := New()
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("t%d", i)
		if i == 0 {
			if err := s.Add(id, sleepTask(time.Duration(i%5)*time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := s.Add(id, sleepTask(time.Duration(i%5)*time.Millisecond), "t0"); err != nil {
			t.Fatal(err)
		}
	}
	errCh := make(chan error, 8)
	go func() {
		_, err := s.Run(context.Background())
		errCh <- err
	}()
	for j := 0; j < 8; j++ {
		j := j
		go func() {
			time.Sleep(time.Duration(j) * time.Millisecond)
			errCh <- s.AddDependency("t1", fmt.Sprintf("t%d", 10+j))
		}()
	}
	for j := 0; j < 9; j++ {
		if err := <-errCh; err != nil &&
			!errors.Is(err, ErrTaskNotPending) &&
			!errors.As(err, new(*CycleError)) {
			t.Fatalf("unexpected err: %v", err)
		}
	}
}

func mustAdd(t *testing.T, s *Scheduler, id string, fn TaskFunc, deps ...string) {
	t.Helper()
	if err := s.Add(id, fn, deps...); err != nil {
		t.Fatalf("Add(%q): %v", id, err)
	}
}
