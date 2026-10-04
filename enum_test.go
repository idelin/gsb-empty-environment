package main

import "testing"

// The shortest lost-update violation needs all 4 ops of T1 and T2
// (program order forces every op before the second "done" write), then
// T3's successful wait and failing assert: 10 events.
func TestLostUpdateShortestViolation(t *testing.T) {
	res := lostUpdate().Explore()
	if res.Violation == nil {
		t.Fatal("expected an assertion violation")
	}
	if got := len(res.Violation); got != 10 {
		t.Fatalf("shortest violation = %d events, want 10", got)
	}
	last := res.Violation[len(res.Violation)-1]
	if last.Thread != "T3" {
		t.Fatalf("last event by %s, want T3", last.Thread)
	}
}

// Duplicate branches merge on full state: two identical constant writes
// produce exactly 4 distinct states (init, two intermediates, final).
func TestMergeDuplicateBranches(t *testing.T) {
	ex := &Explorer{
		InitMem:  Mem{"x": 0},
		MaxSteps: 8,
		Threads: []Thread{
			{Name: "T1", Ops: []Op{WriteConst("x", 1)}},
			{Name: "T2", Ops: []Op{WriteConst("x", 1)}},
		},
	}
	res := ex.Explore()
	if res.Violation != nil {
		t.Fatal("unexpected violation")
	}
	if res.States != 4 {
		t.Fatalf("states = %d, want 4", res.States)
	}
	if res.Truncated {
		t.Fatal("unexpected truncation")
	}
}

// States that differ only in wait state must not merge: same memory,
// same pc, same locals, different waiting tag => different keys.
func TestWaitStatePartOfMergeKey(t *testing.T) {
	base := state{
		mem: Mem{"x": 0},
		threads: []threadState{
			{pc: 0, waiting: "", locals: Locals{"t": 1}},
		},
	}
	blocked := base.clone()
	blocked.threads[0].waiting = "w"
	if base.key() == blocked.key() {
		t.Fatal("merge key ignores wait state")
	}
}

// A blocked coroutine must never be scheduled while its wait condition
// is false; with no writer this deadlocks after one event.
func TestWaitBlocksUntilCondition(t *testing.T) {
	ex := &Explorer{
		InitMem:  Mem{"x": 0, "y": 0},
		MaxSteps: 8,
		Threads: []Thread{
			{Name: "T1", Ops: []Op{
				WaitOp("x==1", "w", func(m Mem, _ Locals) bool { return m["x"] == 1 }),
				WriteConst("y", 1),
			}},
		},
	}
	res := ex.Explore()
	if res.Deadlock == nil {
		t.Fatal("expected deadlock")
	}
	if got := len(res.Deadlock); got != 1 {
		t.Fatalf("deadlock trace = %d events, want 1", got)
	}
	if res.States != 2 {
		t.Fatalf("states = %d, want 2 (init + blocked)", res.States)
	}
}

// Once another coroutine makes the wait condition true, the blocked
// coroutine becomes runnable again and no deadlock is reported.
func TestWaitUnblocks(t *testing.T) {
	res := synchronized().Explore()
	if res.Violation != nil {
		t.Fatalf("unexpected violation: %v", res.Violation)
	}
	if res.Deadlock != nil {
		t.Fatalf("unexpected deadlock: %v", res.Deadlock)
	}
}

// The step bound truncates live schedules and reports it.
func TestStepBoundTruncates(t *testing.T) {
	ex := lostUpdate()
	ex.MaxSteps = 3
	res := ex.Explore()
	if !res.Truncated {
		t.Fatal("expected truncation")
	}
	if res.Violation != nil {
		t.Fatal("no violation should be reachable within 3 steps")
	}
}

// More than MaxCoroutines coroutines is rejected.
func TestCoroutineLimit(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for 5 coroutines")
		}
	}()
	ex := &Explorer{InitMem: Mem{}, Threads: []Thread{
		{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}, {Name: "e"},
	}}
	ex.Explore()
}

// Local operations never yield: a read+local-compute+write burst is a
// single scheduling decision per yield point, so the lost update still
// appears but local math itself creates no extra interleavings.
func TestLocalOpsDoNotYield(t *testing.T) {
	burst := func(name string) Thread {
		return Thread{Name: name, Ops: []Op{
			ReadOp("x", "t"),
			LocalOp(func(l Locals) { l["t"] = l["t"] + 1 }),
			WriteOp("x", func(l Locals) int { return l["t"] }),
		}}
	}
	ex := &Explorer{
		InitMem:  Mem{"x": 0},
		MaxSteps: 8,
		Threads:  []Thread{burst("T1"), burst("T2")},
	}
	res := ex.Explore()
	// Hand-counted merged graph: init, 2 read states, 7 mid states
	// (the two "both read x=0" branches merge into one) and 3 finals:
	// x=1, plus two x=2 finals that stay distinct because the locals
	// differ (which coroutine read x=1).
	if res.States != 13 {
		t.Fatalf("states = %d, want 13", res.States)
	}
}
