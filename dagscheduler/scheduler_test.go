package dagscheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runWithTimeout fails the test if Run does not return within d, guarding
// against accidental deadlocks (e.g. waiting on a cycle).
func runWithTimeout(t *testing.T, s *Scheduler, d time.Duration) ([]Result, error) {
	t.Helper()
	type outcome struct {
		rs  []Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		rs, err := s.Run(context.Background())
		done <- outcome{rs, err}
	}()
	select {
	case o := <-done:
		return o.rs, o.err
	case <-time.After(d):
		t.Fatalf("Run did not return within %v (possible deadlock/hang on a cycle)", d)
		return nil, nil
	}
}

func statusMap(rs []Result) map[string]Result {
	m := make(map[string]Result, len(rs))
	for _, r := range rs {
		m[r.Name] = r
	}
	return m
}

// TestDiamondRunsOnce: root -> {a, b} -> merge. The merge node must execute
// exactly once, after both parents, which in turn run after root.
func TestDiamondRunsOnce(t *testing.T) {
	s := New()

	var mu sync.Mutex
	var order []string
	record := func(name string) {
		mu.Lock()
		order = append(order, name)
		mu.Unlock()
	}
	var rootDone, aDone, bDone atomic.Bool
	var mergeRuns atomic.Int32

	mustAdd(t, s, "root", func(context.Context) error {
		record("root")
		rootDone.Store(true)
		return nil
	})
	mustAdd(t, s, "a", func(context.Context) error {
		if !rootDone.Load() {
			t.Errorf("a ran before root")
		}
		record("a")
		aDone.Store(true)
		return nil
	}, "root")
	mustAdd(t, s, "b", func(context.Context) error {
		if !rootDone.Load() {
			t.Errorf("b ran before root")
		}
		record("b")
		bDone.Store(true)
		return nil
	}, "root")
	mustAdd(t, s, "merge", func(context.Context) error {
		if !aDone.Load() || !bDone.Load() {
			t.Errorf("merge ran before both parents: a=%v b=%v", aDone.Load(), bDone.Load())
		}
		record("merge")
		mergeRuns.Add(1)
		return nil
	}, "a", "b")

	rs, err := runWithTimeout(t, s, 5*time.Second)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mergeRuns.Load(); got != 1 {
		t.Fatalf("merge ran %d times, want exactly 1", got)
	}
	for _, r := range rs {
		if r.Status != Succeeded {
			t.Errorf("task %s = %s, want Succeeded", r.Name, r.Status)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	pos := map[string]int{}
	for i, name := range order {
		pos[name] = i
	}
	if pos["root"] > pos["a"] || pos["root"] > pos["b"] {
		t.Errorf("ordering broken: %v", order)
	}
	if pos["a"] > pos["merge"] || pos["b"] > pos["merge"] {
		t.Errorf("merge before a parent: %v", order)
	}
}

// TestFailureBlocksBranchNotOthers: a failing task blocks all descendants but
// independent branches and their descendants still run.
func TestFailureBlocksBranchNotOthers(t *testing.T) {
	s := New()

	failErr := errors.New("boom")
	var blockedRan, goodRan, afterGoodRan atomic.Int32

	mustAdd(t, s, "bad", func(context.Context) error { return failErr })
	mustAdd(t, s, "down1", func(context.Context) error {
		blockedRan.Add(1)
		return nil
	}, "bad")
	mustAdd(t, s, "down2", func(context.Context) error {
		blockedRan.Add(1)
		return nil
	}, "down1")
	mustAdd(t, s, "good", func(context.Context) error {
		goodRan.Add(1)
		return nil
	})
	mustAdd(t, s, "afterGood", func(context.Context) error {
		afterGoodRan.Add(1)
		return nil
	}, "good")

	rs, err := runWithTimeout(t, s, 5*time.Second)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	m := statusMap(rs)

	if m["bad"].Status != Failed || !errors.Is(m["bad"].Err, failErr) {
		t.Errorf("bad = %+v, want Failed with injected error", m["bad"])
	}
	if m["down1"].Status != Blocked || m["down2"].Status != Blocked {
		t.Errorf("descendants = down1:%s down2:%s, want Blocked/Blocked",
			m["down1"].Status, m["down2"].Status)
	}
	if m["good"].Status != Succeeded || m["afterGood"].Status != Succeeded {
		t.Errorf("independent branch = good:%s afterGood:%s, want Succeeded",
			m["good"].Status, m["afterGood"].Status)
	}
	if blockedRan.Load() != 0 {
		t.Errorf("blocked task functions ran %d times", blockedRan.Load())
	}
	if goodRan.Load() != 1 || afterGoodRan.Load() != 1 {
		t.Errorf("good ran %d times, afterGood ran %d times", goodRan.Load(), afterGoodRan.Load())
	}
}

// TestCycleRejectedBeforeRun: cycles added up front return a *CycleError whose
// chain closes on itself; no task starts and Run never hangs.
func TestCycleRejectedBeforeRun(t *testing.T) {
	s := New()
	mustAdd(t, s, "a", noop)
	mustAdd(t, s, "b", noop)
	mustAdd(t, s, "c", noop)

	if err := s.AddDependency("b", "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDependency("c", "b"); err != nil {
		t.Fatal(err)
	}
	err := s.AddDependency("a", "c")
	ce := wantCycle(t, err)
	if ce.Chain[0] != ce.Chain[len(ce.Chain)-1] {
		t.Fatalf("cycle chain does not close: %v", ce.Chain)
	}
	if len(ce.Chain) != 4 {
		t.Fatalf("chain = %v, want [a c b a]-length 4", ce.Chain)
	}

	// The rejected edge must leave the graph acyclic and runnable.
	if _, err := runWithTimeout(t, s, 5*time.Second); err != nil {
		t.Fatalf("graph after rejected edge should run: %v", err)
	}
}

// TestRunRejectsInjectedCycle covers the Run pre-flight check using a cycle
// that cannot be built through the public API (self-loop injected directly).
func TestRunRejectsInjectedCycle(t *testing.T) {
	s := New()
	mustAdd(t, s, "a", noop)
	mustAdd(t, s, "b", noop, "a")

	var ran atomic.Int32
	s.nodes["b"].fn = func(context.Context) error { ran.Add(1); return nil }

	s.mu.Lock()
	s.nodes["a"].deps["a"] = struct{}{}
	s.mu.Unlock()

	_, err := runWithTimeout(t, s, 5*time.Second)
	ce := wantCycle(t, err)
	if ce.Chain[0] != "a" || ce.Chain[len(ce.Chain)-1] != "a" {
		t.Fatalf("self-loop chain = %v, want [a a]", ce.Chain)
	}
	if ran.Load() != 0 {
		t.Fatalf("task ran despite cyclic graph")
	}
}

// TestAddDependencyDynamic exercises the restricted rule while tasks run:
// edges onto Pending tasks are accepted; edges onto Running tasks are rejected;
// edges that close a cycle are rejected without hanging.
func TestAddDependencyDynamic(t *testing.T) {
	s := New()

	var lateDone atomic.Bool
	var dynamicErr, runningErr, cycleErr error
	var dynamicMu sync.Mutex
	// gateRelease keeps target Pending until mutator finishes adding edges.
	gateRelease := make(chan struct{})

	// late is independent and can be pulled in as a prerequisite at runtime.
	mustAdd(t, s, "late", func(context.Context) error {
		lateDone.Store(true)
		return nil
	})
	// mutator has no prerequisites, so it runs while the target chain is
	// still Pending and exercises every dynamic AddDependency rule.
	mustAdd(t, s, "mutator", func(context.Context) error {
		dynamicMu.Lock()
		defer dynamicMu.Unlock()
		dynamicErr = s.AddDependency("target", "late")  // Pending target: ok
		runningErr = s.AddDependency("mutator", "late") // self is Running
		cycleErr = s.AddDependency("target", "c")       // closes target<->c
		close(gateRelease)
		return nil
	})
	mustAdd(t, s, "gate", func(context.Context) error {
		<-gateRelease
		return nil
	})
	mustAdd(t, s, "blocker", noop)
	mustAdd(t, s, "target", func(context.Context) error {
		if !lateDone.Load() {
			t.Errorf("target ran before dynamically added dependency late")
		}
		return nil
	}, "blocker", "gate")
	// c -> target already exists, so target -> c closes a two-node cycle.
	mustAdd(t, s, "c", noop, "target")

	rs, err := runWithTimeout(t, s, 5*time.Second)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if dynamicErr != nil {
		t.Errorf("dynamic edge onto Pending task: %v", dynamicErr)
	}
	if !errors.Is(runningErr, ErrRunning) {
		t.Errorf("AddDependency on Running task: got %v, want ErrRunning", runningErr)
	}
	if !errors.Is(cycleErr, &CycleError{}) {
		t.Errorf("dynamic cycle: got %v, want *CycleError", cycleErr)
	}

	m := statusMap(rs)
	if m["target"].Status != Succeeded {
		t.Errorf("target = %s, want Succeeded after dynamic edge", m["target"].Status)
	}
	if m["late"].Status != Succeeded || m["c"].Status != Succeeded {
		t.Errorf("late=%s c=%s, want both Succeeded", m["late"].Status, m["c"].Status)
	}
}

// TestAddValidation covers registration errors and the one-shot Run rule.
func TestAddValidation(t *testing.T) {
	s := New()
	if err := s.Add("", noop); !errors.Is(err, ErrEmptyName) {
		t.Errorf("empty name: %v", err)
	}
	if err := s.Add("x", noop, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing prerequisite: %v", err)
	}
	if err := s.Add("x", noop); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("x", noop); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate: %v", err)
	}
	if err := s.AddDependency("x", "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown dependency: %v", err)
	}
	if err := s.AddDependency("ghost", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown task: %v", err)
	}

	if _, err := s.Run(context.Background()); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if _, err := s.Run(context.Background()); !errors.Is(err, ErrAlreadyRun) {
		t.Errorf("second Run: %v, want ErrAlreadyRun", err)
	}
}

// TestManyIndependentTasks stress-tests scheduling with the race detector.
func TestManyIndependentTasks(t *testing.T) {
	s := New()
	const n = 200
	var counter atomic.Int32
	for i := range n {
		name := fmt.Sprintf("t%d", i)
		deps := []string(nil)
		if i > 0 {
			deps = []string{fmt.Sprintf("t%d", i-1)}
		}
		mustAdd(t, s, name, func(context.Context) error {
			counter.Add(1)
			return nil
		}, deps...)
	}
	rs, err := runWithTimeout(t, s, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		if r.Status != Succeeded {
			t.Fatalf("%s = %s", r.Name, r.Status)
		}
	}
	if counter.Load() != n {
		t.Fatalf("ran %d tasks, want %d", counter.Load(), n)
	}
}

func noop(context.Context) error { return nil }

func mustAdd(t *testing.T, s *Scheduler, name string, fn TaskFunc, deps ...string) {
	t.Helper()
	if err := s.Add(name, fn, deps...); err != nil {
		t.Fatalf("Add(%q): %v", name, err)
	}
}

func wantCycle(t *testing.T, err error) *CycleError {
	t.Helper()
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("got %v, want *CycleError", err)
	}
	return ce
}
