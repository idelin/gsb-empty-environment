package sched

import (
	"strings"
	"testing"
)

// lostWakeupMachine models the classic missed-notify bug:
// the consumer checks the flag, then waits; the producer sets the flag
// and notifies. If the notify lands between the check and the wait, the
// consumer blocks forever.
func lostWakeupMachine() *Machine {
	return &Machine{
		MemSize: 1, // mem[0] = flag
		Regs:    1,
		Progs: [][]Instr{
			{ // consumer
				{Kind: Load, Dst: 0, Addr: 0},
				{Kind: JmpIf, Cond: Bin{Op: Ne, L: Reg(0), R: Const(0)}, Target: 3},
				{Kind: Wait, Cv: 0},
				{Kind: Load, Dst: 0, Addr: 0},
				{Kind: Assert, Val: Bin{Op: Eq, L: Reg(0), R: Const(1)}, Note: "flag == 1 after wakeup"},
			},
			{ // producer
				{Kind: Store, Addr: 0, Val: Const(1)},
				{Kind: Notify, Cv: 0},
			},
		},
	}
}

func TestLostWakeupDeadlock(t *testing.T) {
	res, err := Explore(lostWakeupMachine(), Options{MaxSteps: 16})
	if err != nil {
		t.Fatal(err)
	}
	if res.Violation == nil {
		t.Fatalf("expected a deadlock violation, explored=%d merged=%d", res.Explored, res.Merged)
	}
	if res.Violation.Kind != ViolationDeadlock {
		t.Fatalf("expected deadlock, got %s: %s", res.Violation.Kind, res.Violation.Message)
	}
	// Shortest lost-wakeup schedule: consumer loads 0, producer stores,
	// producer notifies (no waiters), consumer blocks.
	if got := len(res.Violation.Trace); got != 4 {
		t.Fatalf("expected shortest trace of 4 events, got %d: %v", got, res.Violation.Trace)
	}
	if v, err := lostWakeupMachine().Replay(res.Violation.Trace); err != nil || v == nil || v.Kind != ViolationDeadlock {
		t.Fatalf("replay did not reproduce deadlock: v=%v err=%v", v, err)
	}
}

// waitStateMachine reaches, via two different schedules, states with
// identical memory and identical PCs that differ ONLY in T0's wait
// state: woken (runnable) vs blocked. Merging them would either hide
// the deadlock or lose the store to mem[0].
func waitStateMachine() *Machine {
	return &Machine{
		MemSize: 1,
		Regs:    1,
		Progs: [][]Instr{
			{ // T0
				{Kind: Wait, Cv: 0},
				{Kind: Store, Addr: 0, Val: Const(1)},
			},
			{ // T1
				{Kind: Store, Addr: 0, Val: Const(2)},
				{Kind: Notify, Cv: 0},
			},
		},
	}
}

func TestWaitStatePreservedByMerge(t *testing.T) {
	sawX1 := false
	res, err := Explore(waitStateMachine(), Options{
		MaxSteps: 8,
		FindAll:  true, // keep exploring past the deadlock so both branches are checked
		Visit: func(s State) {
			if s.Mem[0] == 1 {
				sawX1 = true
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Branch A: T0 waits, then T1 notifies -> T0 wakes and stores 1.
	if !sawX1 {
		t.Fatal("woken branch was lost: mem[0] == 1 never reached")
	}
	// Branch B: T0 waits, T1's notify already fired -> nothing runnable.
	if res.Violation == nil || res.Violation.Kind != ViolationDeadlock {
		t.Fatalf("blocked branch was lost: expected deadlock, got %+v", res.Violation)
	}
	// Shortest: T1 stores, T1 notifies (no waiters yet), T0 blocks.
	if got := len(res.Violation.Trace); got != 3 {
		t.Fatalf("expected shortest deadlock trace of 3 events, got %d", got)
	}
}

// counterMachine runs two workers doing a non-atomic increment of a
// shared counter, then a monitor that asserts the final value once both
// workers are done.
func counterMachine() *Machine {
	worker := func(doneAddr int) []Instr {
		return []Instr{
			{Kind: Load, Dst: 0, Addr: 0},
			{Kind: Store, Addr: 0, Val: Bin{Op: Add, L: Reg(0), R: Const(1)}},
			{Kind: Store, Addr: doneAddr, Val: Const(1)},
		}
	}
	return &Machine{
		MemSize: 3, // mem[0] = counter, mem[1..2] = done flags
		Regs:    1,
		Progs: [][]Instr{
			worker(1),
			worker(2),
			{ // monitor
				{Kind: Load, Dst: 0, Addr: 1},
				{Kind: JmpIf, Cond: Bin{Op: Eq, L: Reg(0), R: Const(0)}, Target: 0},
				{Kind: Load, Dst: 0, Addr: 2},
				{Kind: JmpIf, Cond: Bin{Op: Eq, L: Reg(0), R: Const(0)}, Target: 0},
				{Kind: Load, Dst: 0, Addr: 0},
				{Kind: Assert, Val: Bin{Op: Eq, L: Reg(0), R: Const(2)}, Note: "counter == 2"},
			},
		},
	}
}

func TestCounterRaceShortestAssert(t *testing.T) {
	m := counterMachine()
	res, err := Explore(m, Options{MaxSteps: 40})
	if err != nil {
		t.Fatal(err)
	}
	if res.Violation == nil || res.Violation.Kind != ViolationAssert {
		t.Fatalf("expected assertion violation, got %+v", res.Violation)
	}
	// 3 steps per worker + 3 monitor loads + the failing assert step.
	if got := len(res.Violation.Trace); got != 10 {
		t.Fatalf("expected shortest trace of 10 events, got %d", got)
	}
	last := res.Violation.Trace[len(res.Violation.Trace)-1]
	if !strings.Contains(last.Text, "ASSERT FAIL") {
		t.Fatalf("trace should end at the failing assert, got %q", last.Text)
	}
	if res.Merged == 0 {
		t.Fatal("expected duplicate branches to be merged")
	}
	v, err := m.Replay(res.Violation.Trace)
	if err != nil {
		t.Fatalf("counterexample does not replay: %v", err)
	}
	if v == nil || v.Kind != ViolationAssert {
		t.Fatalf("replay produced %v, want assertion violation", v)
	}
}

func TestStepBoundRespected(t *testing.T) {
	res, err := Explore(counterMachine(), Options{MaxSteps: 5})
	if err != nil {
		t.Fatal(err)
	}
	if res.Violation != nil {
		t.Fatalf("violation needs 10 steps, found within 5: %+v", res.Violation)
	}
	if !res.Truncated {
		t.Fatal("expected exploration to be truncated by the step bound")
	}
}

func TestMaxThreadsEnforced(t *testing.T) {
	m := &Machine{MemSize: 1, Regs: 1}
	for i := 0; i < MaxThreads+1; i++ {
		m.Progs = append(m.Progs, []Instr{{Kind: Store, Addr: 0, Val: Const(1)}})
	}
	if _, err := Explore(m, Options{}); err == nil {
		t.Fatal("expected an error for more than MaxThreads threads")
	}
}

func TestCleanProgramHasNoViolation(t *testing.T) {
	// Producer/consumer where the consumer spins on the flag instead of
	// waiting: every schedule terminates with the assertion holding.
	m := &Machine{
		MemSize: 1,
		Regs:    1,
		Progs: [][]Instr{
			{
				{Kind: Load, Dst: 0, Addr: 0},
				{Kind: JmpIf, Cond: Bin{Op: Eq, L: Reg(0), R: Const(0)}, Target: 0},
				{Kind: Assert, Val: Bin{Op: Eq, L: Reg(0), R: Const(1)}, Note: "flag == 1"},
			},
			{
				{Kind: Store, Addr: 0, Val: Const(1)},
			},
		},
	}
	res, err := Explore(m, Options{MaxSteps: 16})
	if err != nil {
		t.Fatal(err)
	}
	if res.Violation != nil {
		t.Fatalf("unexpected violation: %+v", res.Violation)
	}
	if res.Truncated {
		t.Fatal("small program should explore to completion")
	}
}
