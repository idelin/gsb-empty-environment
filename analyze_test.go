package netsec

import "testing"

func edgeNames(steps []Step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Thread + ":" + s.Edge
	}
	return out
}

// buildMutexBranches creates two threads that touch locks in different orders
// only on mutually exclusive branches of one condition variable. A static
// lock-order analysis might splice the branches into a cycle; the concrete
// enumeration must not.
func buildMutexBranches() Spec {
	c := Var{Name: "c"}
	t0 := Thread{
		Name:   "T0",
		Start:  "s0",
		States: []string{"s0", "a", "b", "done"},
		Edges: []Edge{
			{Name: "lockA", From: "s0", To: "a", Op: OpLock, Lock: "A", Guard: c},
			{Name: "lockB", From: "s0", To: "b", Op: OpLock, Lock: "B", Guard: Not{Inner: c}},
			{Name: "unlockA", From: "a", To: "done", Op: OpUnlock, Lock: "A"},
			{Name: "unlockB", From: "b", To: "done", Op: OpUnlock, Lock: "B"},
		},
	}
	t1 := Thread{
		Name:   "T1",
		Start:  "r0",
		States: []string{"r0", "a", "b", "done"},
		Edges: []Edge{
			{Name: "lockB", From: "r0", To: "b", Op: OpLock, Lock: "B", Guard: c},
			{Name: "lockA", From: "r0", To: "a", Op: OpLock, Lock: "A", Guard: Not{Inner: c}},
			{Name: "unlockB", From: "b", To: "done", Op: OpUnlock, Lock: "B"},
			{Name: "unlockA", From: "a", To: "done", Op: OpUnlock, Lock: "A"},
		},
	}
	return Spec{Threads: [2]Thread{t0, t1}, Locks: []string{"A", "B"}}
}

func TestMutexBranchesDoNotFormCycle(t *testing.T) {
	res, err := Analyze(buildMutexBranches())
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if res.Reachable {
		t.Fatalf("no deadlock expected across mutually exclusive branches, got %s with trace %v",
			res.Kind, edgeNames(res.Trace))
	}
}

// buildABBA is the classic two-thread cyclic deadlock.
func buildABBA() Spec {
	t0 := Thread{
		Name:   "T0",
		Start:  "0",
		States: []string{"0", "1", "2"},
		Edges: []Edge{
			{Name: "lockA", From: "0", To: "1", Op: OpLock, Lock: "A"},
			{Name: "lockB", From: "1", To: "2", Op: OpLock, Lock: "B"},
		},
	}
	t1 := Thread{
		Name:   "T1",
		Start:  "0",
		States: []string{"0", "1", "2"},
		Edges: []Edge{
			{Name: "lockB", From: "0", To: "1", Op: OpLock, Lock: "B"},
			{Name: "lockA", From: "1", To: "2", Op: OpLock, Lock: "A"},
		},
	}
	return Spec{Threads: [2]Thread{t0, t1}, Locks: []string{"A", "B"}}
}

func TestABBACycleShortestTrace(t *testing.T) {
	res, err := Analyze(buildABBA())
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if !res.Reachable || res.Kind != DeadlockCycle {
		t.Fatalf("expected cycle deadlock, got reachable=%v kind=%s", res.Reachable, res.Kind)
	}
	if len(res.Trace) != 2 {
		t.Fatalf("expected shortest trace of 2 steps, got %d: %v", len(res.Trace), edgeNames(res.Trace))
	}
	got := edgeNames(res.Trace)
	want := []string{"T0:lockA", "T1:lockB"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trace mismatch: got %v want %v", got, want)
		}
	}
	pairs := map[string]string{
		"T0": "T1",
		"T1": "T0",
	}
	wantLock := map[string]string{
		"T0": "B",
		"T1": "A",
	}
	wantOwner, ok := pairs[res.BlockedThread]
	if !ok || res.Owner != wantOwner || res.RequestedLock != wantLock[res.BlockedThread] {
		t.Fatalf("unexpected wait-for report: blocked=%s lock=%s owner=%s",
			res.BlockedThread, res.RequestedLock, res.Owner)
	}
	for i, s := range res.Trace {
		if s.Seq != i+1 {
			t.Fatalf("step sequence not numbered from 1: %+v", res.Trace)
		}
	}
}

func TestRecursiveRequestSameLock(t *testing.T) {
	t0 := Thread{
		Name:   "T0",
		Start:  "0",
		States: []string{"0", "1"},
		Edges: []Edge{
			{Name: "first", From: "0", To: "1", Op: OpLock, Lock: "L"},
			{Name: "again", From: "1", To: "1", Op: OpLock, Lock: "L"},
		},
	}
	t1 := Thread{Name: "T1", Start: "0", States: []string{"0"}}
	spec := Spec{Threads: [2]Thread{t0, t1}, Locks: []string{"L"}}

	res, err := Analyze(spec)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if !res.Reachable || res.Kind != DeadlockRecursive {
		t.Fatalf("expected recursive deadlock, got reachable=%v kind=%s", res.Reachable, res.Kind)
	}
	if len(res.Trace) != 1 || res.Trace[0].Edge != "first" {
		t.Fatalf("expected one-step trace [first], got %v", edgeNames(res.Trace))
	}
	if res.BlockedThread != "T0" || res.RequestedLock != "L" || res.Owner != "T0" {
		t.Fatalf("unexpected recursive report: %+v", res)
	}
}

func TestNormalExitNoDeadlock(t *testing.T) {
	t0 := Thread{
		Name:   "T0",
		Start:  "0",
		States: []string{"0", "1"},
		Edges: []Edge{
			{Name: "acquire", From: "0", To: "1", Op: OpLock, Lock: "L"},
			{Name: "release", From: "1", To: "0", Op: OpUnlock, Lock: "L"},
		},
	}
	t1 := Thread{Name: "T1", Start: "0", States: []string{"0"}}
	res, err := Analyze(Spec{Threads: [2]Thread{t0, t1}, Locks: []string{"L"}})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if res.Reachable {
		t.Fatalf("expected no deadlock for lock/unlock loop with an exited peer, got %s", res.Kind)
	}
}

func TestNoFeasibleCycleUnderGuard(t *testing.T) {
	// When c is true both threads take A; when false both take B. The
	// reversed order always belongs to the disabled branch, so no assignment
	// enables an AB-BA interleaving.
	c := Var{Name: "c"}
	branch := func(prefix string) Thread {
		return Thread{
			Name:   prefix,
			Start:  "0",
			States: []string{"0", "ha", "hb", "out"},
			Edges: []Edge{
				{Name: "viaA", From: "0", To: "ha", Op: OpLock, Lock: "A", Guard: c},
				{Name: "viaB", From: "0", To: "hb", Op: OpLock, Lock: "B", Guard: Not{Inner: c}},
				{Name: "relA", From: "ha", To: "out", Op: OpUnlock, Lock: "A"},
				{Name: "relB", From: "hb", To: "out", Op: OpUnlock, Lock: "B"},
			},
		}
	}
	t0 := branch("T0")
	t1 := branch("T1")
	res, err := Analyze(Spec{Threads: [2]Thread{t0, t1}, Locks: []string{"A", "B"}})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if res.Reachable {
		t.Fatalf("expected no feasible cycle, got %s trace %v", res.Kind, edgeNames(res.Trace))
	}
}

func TestConditionalABBAReportsAssignment(t *testing.T) {
	// Deadlock is reachable only while condition d is true.
	d := Var{Name: "d"}
	t0 := Thread{
		Name:   "T0",
		Start:  "0",
		States: []string{"0", "1", "2"},
		Edges: []Edge{
			{Name: "lockA", From: "0", To: "1", Op: OpLock, Lock: "A", Guard: d},
			{Name: "lockB", From: "1", To: "2", Op: OpLock, Lock: "B"},
		},
	}
	t1 := Thread{
		Name:   "T1",
		Start:  "0",
		States: []string{"0", "1", "2"},
		Edges: []Edge{
			{Name: "lockB", From: "0", To: "1", Op: OpLock, Lock: "B", Guard: d},
			{Name: "lockA", From: "1", To: "2", Op: OpLock, Lock: "A"},
		},
	}
	res, err := Analyze(Spec{Threads: [2]Thread{t0, t1}, Locks: []string{"A", "B"}})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if !res.Reachable || res.Kind != DeadlockCycle {
		t.Fatalf("expected conditional cycle, got reachable=%v kind=%s", res.Reachable, res.Kind)
	}
	if v, ok := res.Assignment["d"]; !ok || !v {
		t.Fatalf("expected assignment d=true, got %+v", res.Assignment)
	}
}

func TestOrphanedOwnerExited(t *testing.T) {
	t0 := Thread{
		Name:   "T0",
		Start:  "0",
		States: []string{"0", "1"},
		Edges: []Edge{
			{Name: "hold", From: "0", To: "1", Op: OpLock, Lock: "L"},
		},
	}
	t1 := Thread{
		Name:   "T1",
		Start:  "0",
		States: []string{"0", "1"},
		Edges: []Edge{
			{Name: "want", From: "0", To: "1", Op: OpLock, Lock: "L"},
		},
	}
	res, err := Analyze(Spec{Threads: [2]Thread{t0, t1}, Locks: []string{"L"}})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if !res.Reachable || res.Kind != DeadlockOrphaned {
		t.Fatalf("expected orphaned deadlock, got reachable=%v kind=%s", res.Reachable, res.Kind)
	}
	if res.BlockedThread != "T1" || res.Owner != "T0" || res.RequestedLock != "L" {
		t.Fatalf("unexpected orphaned report: blocked=%s owner=%s lock=%s",
			res.BlockedThread, res.Owner, res.RequestedLock)
	}
}

func TestCyclePreferredOverShallowerOrphan(t *testing.T) {
	// T0 can terminate early while holding L (shallow orphan for T1), but
	// both threads also have the classic A/B vs B/A path. The analyzer must
	// keep searching and report the shortest cyclic deadlock rather than the
	// shallower orphaned wait.
	t0 := Thread{
		Name:   "T0",
		Start:  "0",
		States: []string{"0", "1", "2", "exit"},
		Edges: []Edge{
			{Name: "lockA", From: "0", To: "1", Op: OpLock, Lock: "A"},
			{Name: "lockB", From: "1", To: "2", Op: OpLock, Lock: "B"},
			{Name: "keepA", From: "0", To: "exit", Op: OpLock, Lock: "L"},
		},
	}
	t1 := Thread{
		Name:   "T1",
		Start:  "0",
		States: []string{"0", "1", "2"},
		Edges: []Edge{
			{Name: "lockB", From: "0", To: "1", Op: OpLock, Lock: "B"},
			{Name: "lockA", From: "1", To: "2", Op: OpLock, Lock: "A"},
			{Name: "wantL", From: "0", To: "2", Op: OpLock, Lock: "L"},
		},
	}
	res, err := Analyze(Spec{Threads: [2]Thread{t0, t1}, Locks: []string{"A", "B", "L"}})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if !res.Reachable || res.Kind != DeadlockCycle {
		t.Fatalf("expected cycle to outrank shallow orphan, got %s", res.Kind)
	}
	if len(res.Trace) != 2 {
		t.Fatalf("expected shortest cycle trace of 2 steps, got %v", edgeNames(res.Trace))
	}
}

func TestValidationErrors(t *testing.T) {
	base := buildABBA()

	tooMany := base
	tooMany.Locks = []string{"A", "B", "C", "D", "E", "F", "G"}
	if _, err := Analyze(tooMany); err == nil {
		t.Fatal("expected error for seven locks")
	}

	badEdge := base
	badEdge.Threads[0].Edges[0].From = "missing"
	if _, err := Analyze(badEdge); err == nil {
		t.Fatal("expected error for unknown edge source")
	}

	dupLock := base
	dupLock.Locks = []string{"A", "A"}
	if _, err := Analyze(dupLock); err == nil {
		t.Fatal("expected error for duplicate lock names")
	}
}

func TestUnlockNotHeldIsError(t *testing.T) {
	t0 := Thread{
		Name:   "T0",
		Start:  "0",
		States: []string{"0", "1"},
		Edges: []Edge{
			{Name: "release", From: "0", To: "1", Op: OpUnlock, Lock: "L"},
		},
	}
	t1 := Thread{Name: "T1", Start: "0", States: []string{"0"}}
	if _, err := Analyze(Spec{Threads: [2]Thread{t0, t1}, Locks: []string{"L"}}); err == nil {
		t.Fatal("expected error when unlocking a mutex not held")
	}
}

func TestAssignmentEnumerationCoversBothBranches(t *testing.T) {
	// Under d=false T0 exits immediately; under d=true it blocks on T1's
	// mutex, producing an orphaned deadlock. The analyzer must find it even
	// though it only exists on one fixed assignment.
	d := Var{Name: "d"}
	t0 := Thread{
		Name:   "T0",
		Start:  "0",
		States: []string{"0", "1", "out"},
		Edges: []Edge{
			{Name: "guarded", From: "0", To: "1", Op: OpLock, Lock: "L", Guard: d},
			{Name: "skip", From: "0", To: "out", Op: OpNone, Guard: Not{Inner: d}},
		},
	}
	t1 := Thread{
		Name:   "T1",
		Start:  "0",
		States: []string{"0", "1"},
		Edges: []Edge{
			{Name: "hold", From: "0", To: "1", Op: OpLock, Lock: "L"},
		},
	}
	res, err := Analyze(Spec{Threads: [2]Thread{t0, t1}, Locks: []string{"L"}})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if !res.Reachable || res.Kind != DeadlockOrphaned {
		t.Fatalf("expected assignment-specific orphaned deadlock, got reachable=%v kind=%s",
			res.Reachable, res.Kind)
	}
	if v, ok := res.Assignment["d"]; !ok || !v {
		t.Fatalf("expected assignment d=true, got %+v", res.Assignment)
	}
}

func TestMultipleConditionBitsStayAligned(t *testing.T) {
	// Two variables whose name order differs from their first appearance.
	// The AB-BA edges require z=true and a=false; the opposite assignment
	// lets both threads exit. This only works if bit positions stay aligned
	// between assignment enumeration and guard evaluation.
	z := Var{Name: "z"}
	a := Var{Name: "a"}
	t0 := Thread{
		Name:   "T0",
		Start:  "0",
		States: []string{"0", "1", "2", "out"},
		Edges: []Edge{
			{Name: "lockA", From: "0", To: "1", Op: OpLock, Lock: "A", Guard: And{Lhs: z, Rhs: Not{Inner: a}}},
			{Name: "skip", From: "0", To: "out", Op: OpNone, Guard: Or{Lhs: Not{Inner: z}, Rhs: a}},
			{Name: "lockB", From: "1", To: "2", Op: OpLock, Lock: "B"},
		},
	}
	t1 := Thread{
		Name:   "T1",
		Start:  "0",
		States: []string{"0", "1", "2", "out"},
		Edges: []Edge{
			{Name: "lockB", From: "0", To: "1", Op: OpLock, Lock: "B", Guard: And{Lhs: z, Rhs: Not{Inner: a}}},
			{Name: "skip", From: "0", To: "out", Op: OpNone, Guard: Or{Lhs: Not{Inner: z}, Rhs: a}},
			{Name: "lockA", From: "1", To: "2", Op: OpLock, Lock: "A"},
		},
	}
	res, err := Analyze(Spec{Threads: [2]Thread{t0, t1}, Locks: []string{"A", "B"}})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if !res.Reachable || res.Kind != DeadlockCycle {
		t.Fatalf("expected cycle under z=true,a=false, got reachable=%v kind=%s",
			res.Reachable, res.Kind)
	}
	if !res.Assignment["z"] || res.Assignment["a"] {
		t.Fatalf("expected z=true a=false, got %+v", res.Assignment)
	}
}
