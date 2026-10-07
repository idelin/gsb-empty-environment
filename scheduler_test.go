package dagsched

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

var errBoom = errors.New("boom")

func succeed(context.Context) error { return nil }

// A failed task skips exactly its transitive dependents; the sibling
// branch keeps running and succeeds.
func TestFailureSkipsOnlyDependents(t *testing.T) {
	ex := newVirtualExecutor(ResourceMap{"cpu": 4})
	s := NewScheduler(ResourceMap{"cpu": 4})
	//       root
	//      /    \
	//     a1(X)  b1
	//     |
	//     a2
	mustAdd(t, s, ex.task("root", nil, ResourceMap{"cpu": 1}, succeed))
	mustAdd(t, s, ex.task("a1", []string{"root"}, ResourceMap{"cpu": 1},
		func(context.Context) error { return errBoom }))
	mustAdd(t, s, ex.task("a2", []string{"a1"}, ResourceMap{"cpu": 1}, succeed))
	mustAdd(t, s, ex.task("b1", []string{"root"}, ResourceMap{"cpu": 1}, succeed))

	res, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := map[string]State{
		"root": StateSucceeded,
		"a1":   StateFailed,
		"a2":   StateSkipped,
		"b1":   StateSucceeded,
	}
	assertStates(t, res, want)
	if !errors.Is(res["a1"].Err, errBoom) {
		t.Errorf("a1 error does not wrap errBoom: %v", res["a1"].Err)
	}
	if !ex.started("b1") {
		t.Error("sibling branch task b1 never started")
	}
	if ex.started("a2") {
		t.Error("dependent a2 started despite failed dependency")
	}
	ex.checkConservation(t)
}

// The reason chain from a skipped leaf unwraps through every intermediate
// skipped dependency down to the root failure.
func TestFailureReasonChain(t *testing.T) {
	ex := newVirtualExecutor(ResourceMap{"cpu": 1})
	s := NewScheduler(ResourceMap{"cpu": 1})
	mustAdd(t, s, ex.task("t1", nil, nil, func(context.Context) error { return errBoom }))
	mustAdd(t, s, ex.task("t2", []string{"t1"}, nil, succeed))
	mustAdd(t, s, ex.task("t3", []string{"t2"}, nil, succeed))
	mustAdd(t, s, ex.task("t4", []string{"t3"}, nil, succeed))

	res, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertStates(t, res, map[string]State{
		"t1": StateFailed,
		"t2": StateSkipped,
		"t3": StateSkipped,
		"t4": StateSkipped,
	})

	// The leaf's error reaches the root cause via errors.Is.
	if !errors.Is(res["t4"].Err, errBoom) {
		t.Fatalf("t4 error chain does not reach errBoom: %v", res["t4"].Err)
	}
	// Each hop is a *DepError naming the failing dependency.
	wantDeps := []string{"t3", "t2", "t1"}
	err = res["t4"].Err
	for i, wantDep := range wantDeps {
		var de *DepError
		if !errors.As(err, &de) {
			t.Fatalf("hop %d: no *DepError in chain of %v", i, err)
		}
		if de.Dep != wantDep {
			t.Errorf("hop %d: DepError.Dep = %q, want %q", i, de.Dep, wantDep)
		}
		if i < len(wantDeps)-1 && de.DepState != StateSkipped {
			t.Errorf("hop %d: DepState = %s, want skipped", i, de.DepState)
		}
		err = de.Err
	}
	// The rendered message names every task along the chain.
	msg := res["t4"].Err.Error()
	for _, id := range []string{"t4", "t3", "t2", "t1"} {
		if !strings.Contains(msg, fmt.Sprintf("%q", id)) {
			t.Errorf("chain message %q missing task %q", msg, id)
		}
	}
	ex.checkConservation(t)
}

// A task holding one resource must not prevent accounting for the other:
// a task needing cpu+mem starts only when both are fully available.
func TestResourceGrantIsAllOrNothing(t *testing.T) {
	ex := newVirtualExecutor(ResourceMap{"cpu": 1, "mem": 1})
	s := NewScheduler(ResourceMap{"cpu": 1, "mem": 1})
	gA, gB, gC := newGate(), newGate(), newGate()
	mustAdd(t, s, ex.task("a", nil, ResourceMap{"cpu": 1}, gA.wait))
	mustAdd(t, s, ex.task("b", nil, ResourceMap{"mem": 1}, gB.wait))
	mustAdd(t, s, ex.task("c", nil, ResourceMap{"cpu": 1, "mem": 1}, gC.wait))

	resCh, errCh := runAsync(s, context.Background())
	waitFor(t, "a and b to start", func() bool { return ex.startedCount() == 2 })
	if ex.started("c") {
		t.Fatal("c started while mem was still held by b")
	}

	gA.open() // frees cpu only; mem still held
	waitFor(t, "a to finish", func() bool { return ex.inUseOf("cpu") == 0 })
	if ex.started("c") {
		t.Fatal("c started with only cpu free; grant must be all-or-nothing")
	}

	gB.open() // now both resources are free
	waitFor(t, "c to start", func() bool { return ex.started("c") })
	gC.open()

	res, err := awaitRun(t, resCh, errCh)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertStates(t, res, map[string]State{
		"a": StateSucceeded, "b": StateSucceeded, "c": StateSucceeded,
	})
	ex.checkConservation(t)
}

// Parallel tasks pack up to capacity but never beyond it; a task needing
// the whole capacity waits for a completely free pool.
func TestResourceConservation(t *testing.T) {
	ex := newVirtualExecutor(ResourceMap{"cpu": 2})
	s := NewScheduler(ResourceMap{"cpu": 2})
	g1, g2 := newGate(), newGate()
	mustAdd(t, s, ex.task("p1", nil, ResourceMap{"cpu": 1}, g1.wait))
	mustAdd(t, s, ex.task("p2", nil, ResourceMap{"cpu": 1}, g1.wait))
	mustAdd(t, s, ex.task("big", nil, ResourceMap{"cpu": 2}, g2.wait))

	resCh, errCh := runAsync(s, context.Background())
	waitFor(t, "p1 and p2 to start", func() bool { return ex.startedCount() == 2 })
	if got := ex.inUseOf("cpu"); got != 2 {
		t.Fatalf("cpu in use = %d, want 2 (p1+p2 packed)", got)
	}
	if ex.started("big") {
		t.Fatal("big started while cpu was fully consumed")
	}

	g1.open()
	waitFor(t, "big to start", func() bool { return ex.started("big") })
	if got := ex.inUseOf("cpu"); got != 2 {
		t.Fatalf("cpu in use = %d, want 2 (big alone)", got)
	}
	g2.open()

	res, err := awaitRun(t, resCh, errCh)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertStates(t, res, map[string]State{
		"p1": StateSucceeded, "p2": StateSucceeded, "big": StateSucceeded,
	})
	if got := ex.maxInUse["cpu"]; got != 2 {
		t.Errorf("max cpu in use = %d, want exactly capacity 2", got)
	}
	ex.checkConservation(t)
}

// Cancellation admits no new tasks and Run returns only after every
// started task has run its cleanup.
func TestCancelWaitsForStartedTasksToCleanUp(t *testing.T) {
	ex := newVirtualExecutor(ResourceMap{"cpu": 1})
	s := NewScheduler(ResourceMap{"cpu": 1})
	// slow holds the only cpu; queued is ready but starved; child never
	// becomes ready.
	mustAdd(t, s, ex.task("slow", nil, ResourceMap{"cpu": 1},
		func(ctx context.Context) error {
			<-ctx.Done()
			ex.recordCleanup("slow")
			return nil
		}))
	mustAdd(t, s, ex.task("queued", nil, ResourceMap{"cpu": 1}, succeed))
	mustAdd(t, s, ex.task("child", []string{"slow"}, nil, succeed))

	ctx, cancel := context.WithCancel(context.Background())
	resCh, errCh := runAsync(s, ctx)
	waitFor(t, "slow to start", func() bool { return ex.started("slow") })
	cancel()

	res, err := awaitRun(t, resCh, errCh)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	// Run returned, so slow's cleanup must already have happened.
	ex.mu.Lock()
	cleanups := append([]string(nil), ex.cleanups...)
	ex.mu.Unlock()
	if len(cleanups) != 1 || cleanups[0] != "slow" {
		t.Errorf("cleanups = %v, want [slow] before Run returned", cleanups)
	}
	assertStates(t, res, map[string]State{
		"slow":   StateSucceeded, // cleaned up and returned nil
		"queued": StateCanceled,
		"child":  StateCanceled,
	})
	if !errors.Is(res["queued"].Err, context.Canceled) {
		t.Errorf("queued error does not wrap context.Canceled: %v", res["queued"].Err)
	}
	ex.checkConservation(t)
}

// The same scenario produces identical terminal states and identical
// start order on repeated runs.
func TestDeterministicTerminalStates(t *testing.T) {
	scenario := func() (map[string]Result, []string) {
		ex := newVirtualExecutor(ResourceMap{"cpu": 2})
		s := NewScheduler(ResourceMap{"cpu": 2})
		g1, g2 := newGate(), newGate()
		mustAdd(t, s, ex.task("p1", nil, ResourceMap{"cpu": 1}, g1.wait))
		mustAdd(t, s, ex.task("p2", nil, ResourceMap{"cpu": 1}, g1.wait))
		mustAdd(t, s, ex.task("big", nil, ResourceMap{"cpu": 2}, g2.wait))
		mustAdd(t, s, ex.task("bad", []string{"big"}, nil,
			func(context.Context) error { return errBoom }))
		mustAdd(t, s, ex.task("leaf", []string{"bad"}, nil, succeed))

		resCh, errCh := runAsync(s, context.Background())
		waitFor(t, "p1 and p2 to start", func() bool { return ex.startedCount() == 2 })
		g1.open()
		waitFor(t, "big to start", func() bool { return ex.started("big") })
		g2.open()
		res, err := awaitRun(t, resCh, errCh)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return res, ex.startsSnapshot()
	}

	res1, starts1 := scenario()
	res2, starts2 := scenario()

	// Tasks granted in the same dispatch pass (p1, p2) race to record
	// their start, so compare the started multiset across runs...
	count1, count2 := map[string]int{}, map[string]int{}
	for _, id := range starts1 {
		count1[id]++
	}
	for _, id := range starts2 {
		count2[id]++
	}
	if fmt.Sprint(count1) != fmt.Sprint(count2) {
		t.Fatalf("started task sets differ: %v vs %v", starts1, starts2)
	}
	// ...and assert the order constraints that must hold deterministically:
	// big waits for both cpus, bad waits for big.
	assertStartsAfter(t, starts1, "big", "p1", "p2")
	assertStartsAfter(t, starts1, "bad", "big")
	if len(res1) != len(res2) {
		t.Fatalf("result counts differ: %d vs %d", len(res1), len(res2))
	}
	for id, r1 := range res1 {
		r2, ok := res2[id]
		if !ok {
			t.Fatalf("task %q missing from second run", id)
		}
		if r1.State != r2.State {
			t.Errorf("task %q state differs: %s vs %s", id, r1.State, r2.State)
		}
		if fmt.Sprint(r1.Err) != fmt.Sprint(r2.Err) {
			t.Errorf("task %q error differs: %v vs %v", id, r1.Err, r2.Err)
		}
	}
	// Sanity: the scenario itself exercises failure and skipping.
	assertStates(t, res1, map[string]State{
		"p1": StateSucceeded, "p2": StateSucceeded, "big": StateSucceeded,
		"bad": StateFailed, "leaf": StateSkipped,
	})
}

// A panicking task is reported as failed, not as a scheduler crash.
func TestPanicBecomesFailure(t *testing.T) {
	ex := newVirtualExecutor(ResourceMap{"cpu": 1})
	s := NewScheduler(ResourceMap{"cpu": 1})
	mustAdd(t, s, ex.task("panic", nil, nil,
		func(context.Context) error { panic("kaboom") }))
	mustAdd(t, s, ex.task("after", []string{"panic"}, nil, succeed))

	res, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertStates(t, res, map[string]State{
		"panic": StateFailed,
		"after": StateSkipped,
	})
	if !strings.Contains(res["panic"].Err.Error(), "kaboom") {
		t.Errorf("panic error = %v, want it to mention the panic value", res["panic"].Err)
	}
}

func TestValidation(t *testing.T) {
	t.Run("duplicate ID", func(t *testing.T) {
		s := NewScheduler(nil)
		mustAdd(t, s, Task{ID: "x", Run: succeed})
		if err := s.Add(Task{ID: "x", Run: succeed}); err == nil {
			t.Error("duplicate ID accepted")
		}
	})
	t.Run("unknown resource", func(t *testing.T) {
		s := NewScheduler(ResourceMap{"cpu": 1})
		err := s.Add(Task{ID: "x", Needs: ResourceMap{"gpu": 1}, Run: succeed})
		if err == nil {
			t.Error("unknown resource accepted")
		}
	})
	t.Run("request exceeds capacity", func(t *testing.T) {
		s := NewScheduler(ResourceMap{"cpu": 1})
		err := s.Add(Task{ID: "x", Needs: ResourceMap{"cpu": 2}, Run: succeed})
		if err == nil {
			t.Error("over-capacity request accepted")
		}
	})
	t.Run("unknown dependency", func(t *testing.T) {
		s := NewScheduler(nil)
		mustAdd(t, s, Task{ID: "x", Deps: []string{"ghost"}, Run: succeed})
		if _, err := s.Run(context.Background()); err == nil {
			t.Error("unknown dependency accepted")
		}
	})
	t.Run("self dependency", func(t *testing.T) {
		s := NewScheduler(nil)
		mustAdd(t, s, Task{ID: "x", Deps: []string{"x"}, Run: succeed})
		if _, err := s.Run(context.Background()); err == nil {
			t.Error("self dependency accepted")
		}
	})
	t.Run("cycle", func(t *testing.T) {
		s := NewScheduler(nil)
		mustAdd(t, s, Task{ID: "a", Deps: []string{"b"}, Run: succeed})
		mustAdd(t, s, Task{ID: "b", Deps: []string{"a"}, Run: succeed})
		if _, err := s.Run(context.Background()); err == nil {
			t.Error("cycle accepted")
		}
	})
	t.Run("empty scheduler", func(t *testing.T) {
		res, err := NewScheduler(nil).Run(context.Background())
		if err != nil || len(res) != 0 {
			t.Errorf("empty run = %v, %v", res, err)
		}
	})
}

func mustAdd(t *testing.T, s *Scheduler, task Task) {
	t.Helper()
	if err := s.Add(task); err != nil {
		t.Fatalf("Add(%q): %v", task.ID, err)
	}
}

func assertStates(t *testing.T, res map[string]Result, want map[string]State) {
	t.Helper()
	if len(res) != len(want) {
		t.Errorf("got %d results, want %d", len(res), len(want))
	}
	for id, wantState := range want {
		r, ok := res[id]
		if !ok {
			t.Errorf("task %q: no result", id)
			continue
		}
		if r.State != wantState {
			t.Errorf("task %q: state = %s, want %s (err: %v)", id, r.State, wantState, r.Err)
		}
		if wantState.Terminal() && wantState != StateSucceeded && r.Err == nil {
			t.Errorf("task %q: %s but Err is nil", id, wantState)
		}
	}
}

func assertStartsAfter(t *testing.T, starts []string, id string, before ...string) {
	t.Helper()
	pos := map[string]int{}
	for i, s := range starts {
		pos[s] = i
	}
	for _, b := range before {
		if pos[id] <= pos[b] {
			t.Errorf("start order %v: %q must start after %q", starts, id, b)
		}
	}
}
