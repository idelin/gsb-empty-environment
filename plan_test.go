package netsec

import (
	"errors"
	"math/bits"
	"testing"
)

func TestPlanOrdersPermissionWriteBeforeLock(t *testing.T) {
	// Bit 2 is the lock. The permission write is only valid while the lock is
	// still zero. A shortest plan must therefore be set-then-lock, even
	// though the operations are listed in the opposite order.
	p := Problem{
		Bits:     3,
		Initial:  0,
		Target:   0b101,
		LockMask: 0b100,
		Ops: []Operation{
			{Name: "lock", Mask: 0b100, Value: 0b100},
			{Name: "grant", Mask: 0b001, Value: 0b001, RequireMask: 0b100, RequireValue: 0},
		},
	}

	got, err := Plan(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete() {
		t.Fatalf("status = %s, reason = %s", got.Status, got.Reason)
	}
	if len(got.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(got.Steps))
	}
	if got.Steps[0].Name != "grant" || got.Steps[1].Name != "lock" {
		t.Fatalf("wrong order: %+v", got.Steps)
	}
	assertPlanValid(t, p, got)
}

func TestPlanAvoidsForbiddenStates(t *testing.T) {
	// The direct grant enters forbidden state 0b001. The safe route sets
	// bit 1 first, then sets bit 0 as the final write.
	p := Problem{
		Bits:    3,
		Initial: 0,
		Target:  0b011,
		Ops: []Operation{
			{Name: "direct", Mask: 0b001, Value: 0b001},
			{Name: "grant-1", Mask: 0b010, Value: 0b010},
			{Name: "grant-0", Mask: 0b001, Value: 0b001},
		},
		ForbiddenStates: []uint32{0b001},
	}

	got, err := Plan(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete() {
		t.Fatalf("status = %s, reason = %s", got.Status, got.Reason)
	}
	if len(got.Steps) != 2 {
		t.Fatalf("steps = %d, want 2: %+v", len(got.Steps), got.Steps)
	}
	assertPlanValid(t, p, got)
	for _, state := range got.States {
		if state == 0b001 {
			t.Fatal("plan entered forbidden state")
		}
	}
}

func TestForbiddenBits(t *testing.T) {
	p := Problem{
		Bits:          3,
		Initial:       0,
		Target:        0b011,
		ForbiddenBits: 0b001,
		Ops: []Operation{
			{Name: "a", Mask: 0b010, Value: 0b010},
			{Name: "b", Mask: 0b001, Value: 0b001},
		},
	}
	got, err := Plan(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusUnreachable || got.Reason != "target state is forbidden" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestPlanFindsShortestPath(t *testing.T) {
	// Two operations with overlapping masks: the one-write path must win over
	// the two-write path.
	p := Problem{
		Bits:    4,
		Initial: 0,
		Target:  0b1111,
		Ops: []Operation{
			{Name: "low", Mask: 0b0011, Value: 0b0011},
			{Name: "high", Mask: 0b1100, Value: 0b1100},
			{Name: "all", Mask: 0b1111, Value: 0b1111},
		},
	}
	got, err := Plan(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete() || len(got.Steps) != 1 || got.Steps[0].Name != "all" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestPlanClearsSensitiveAreaBeforeLock(t *testing.T) {
	// A sensitive permission starts writable. Locking while it is still set
	// is forbidden forever; the safe shortest plan clears it, then locks.
	p := Problem{
		Bits:     3,
		Initial:  0b010,
		Target:   0b100,
		LockMask: 0b100,
		Ops: []Operation{
			{Name: "lock", Mask: 0b100, Value: 0b100},
			{Name: "clear-sensitive", Mask: 0b010, Value: 0},
		},
		Forbidden: []Condition{{Mask: 0b110, Value: 0b110}},
	}
	got, err := Plan(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete() || len(got.Steps) != 2 {
		t.Fatalf("unexpected result: %+v", got)
	}
	if got.Steps[0].Name != "clear-sensitive" || got.Steps[1].Name != "lock" {
		t.Fatalf("wrong order: %+v", got.Steps)
	}
	if err := got.Verify(p); err != nil {
		t.Fatalf("verify: %v", err)
	}
	assertPlanValid(t, p, got)
}

func TestPlanBudgetIncomplete(t *testing.T) {
	p := Problem{
		Bits:     2,
		Initial:  0,
		Target:   0b11,
		MaxSteps: 1,
		Ops: []Operation{
			{Name: "a", Mask: 0b01, Value: 0b01},
			{Name: "b", Mask: 0b10, Value: 0b10},
		},
	}
	got, err := Plan(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusIncomplete {
		t.Fatalf("status = %s, want incomplete", got.Status)
	}
	if !got.Truncated {
		t.Fatal("incomplete result was not marked truncated")
	}
	if len(got.Steps) != 0 {
		t.Fatalf("incomplete result should not claim a complete plan: %+v", got.Steps)
	}
	if len(got.BestSteps) != 1 {
		t.Fatalf("best prefix = %d, want 1", len(got.BestSteps))
	}
}

func TestPlanStateBudgetIncomplete(t *testing.T) {
	p := Problem{
		Bits:      4,
		Initial:   0,
		Target:    0b1111,
		MaxStates: 2,
		Ops: []Operation{
			{Name: "a", Mask: 0b0001, Value: 0b0001},
			{Name: "b", Mask: 0b0010, Value: 0b0010},
			{Name: "c", Mask: 0b0100, Value: 0b0100},
			{Name: "d", Mask: 0b1000, Value: 0b1000},
			{Name: "all", Mask: 0b1111, Value: 0b1111},
		},
	}
	got, err := Plan(p)
	if err != nil {
		t.Fatal(err)
	}
	// The cap fills after the first two discovered states, but the direct
	// "all" write is still checked and reaches the goal.
	if got.Status != StatusComplete || len(got.Steps) != 1 {
		t.Fatalf("unexpected result: %+v", got)
	}

	// Here the goal requires an intermediate state that does not fit in the
	// two-state budget, so the search must report incomplete.
	p = Problem{
		Bits:      3,
		Initial:   0,
		Target:    0b100,
		MaxStates: 2,
		Ops: []Operation{
			{Name: "a", Mask: 0b001, Value: 0b001},
			{Name: "b", Mask: 0b010, Value: 0b010, RequireMask: 0b001, RequireValue: 0b001},
			{Name: "c", Mask: 0b100, Value: 0b100, RequireMask: 0b010, RequireValue: 0b010},
		},
	}
	got, err = Plan(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusIncomplete {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestPlanUnreachableWhenLockedTooEarly(t *testing.T) {
	// If the lock is set first, the grant operation can never run. The only
	// other op is a no-op.
	p := Problem{
		Bits:     2,
		Initial:  0,
		Target:   0b11,
		LockMask: 0b10,
		Ops: []Operation{
			{Name: "lock", Mask: 0b10, Value: 0b10},
			{Name: "noop", Mask: 0, Value: 0},
		},
	}
	got, err := Plan(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusUnreachable {
		t.Fatalf("status = %s, want unreachable", got.Status)
	}
}

func TestLockFreezesProtectedBits(t *testing.T) {
	// Locks freeze the permission bits. The commit operation may set the
	// permission and the lock atomically; any later change is rejected.
	p := Problem{
		Bits:  3,
		Locks: []LockRule{{Bit: 2, Freezes: 0b011}},
		Ops: []Operation{
			{Name: "commit", Mask: 0b111, Value: 0b101}, // bits 0,2 = 1
			{Name: "late", Mask: 0b001, Value: 0},
		},
	}

	state, ok, err := Next(p, 0b000, 0)
	if err != nil || !ok || state != 0b101 {
		t.Fatalf("commit: state=%#b ok=%v err=%v", state, ok, err)
	}
	state, ok, err = Next(p, state, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("late write changed frozen permission bits: %#b", state)
	}
}

func TestDefaultLockFreezesPermissions(t *testing.T) {
	// With no explicit LockRule, setting the lock bit freezes all other bits.
	p := Problem{
		Bits:     3,
		Initial:  0,
		LockMask: 0b100,
		Ops: []Operation{
			{Name: "lock", Mask: 0b100, Value: 0b100},
			{Name: "grant", Mask: 0b001, Value: 0b001},
		},
	}
	state, ok, err := Next(p, 0, 0)
	if err != nil || !ok || state != 0b100 {
		t.Fatalf("lock: state=%#b ok=%v err=%v", state, ok, err)
	}
	if _, ok, err = Next(p, state, 1); err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("permission write must be rejected after lock")
	}

	// The escape hatch keeps ordinary one-way lock semantics.
	p.SelfLockOnly = true
	if _, ok, err = Next(p, state, 1); err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("SelfLockOnly should allow post-lock permission writes")
	}
}

func TestPartialGoal(t *testing.T) {
	p := Problem{
		Bits:    3,
		Initial: 0,
		Target:  0, // ignored because Goal is set
		Goal:    &Condition{Mask: 0b110, Value: 0b110},
		Ops: []Operation{
			{Name: "a", Mask: 0b010, Value: 0b010},
			{Name: "b", Mask: 0b100, Value: 0b100},
		},
	}
	got, err := Plan(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete() || len(got.Steps) != 2 {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestPartialTargetMask(t *testing.T) {
	p := Problem{
		Bits:       3,
		Initial:    0,
		Target:     0b110,
		TargetMask: 0b110,
		Ops: []Operation{
			{Name: "a", Mask: 0b010, Value: 0b010},
			{Name: "b", Mask: 0b100, Value: 0b100},
		},
	}
	got, err := Plan(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete() || len(got.Steps) != 2 {
		t.Fatalf("unexpected result: %+v", got)
	}
	if err := got.Verify(p); err != nil {
		t.Fatal(err)
	}
}

func TestInitialAndTargetForbidden(t *testing.T) {
	p := Problem{
		Bits:      2,
		Initial:   0b01,
		Target:    0b10,
		Forbidden: []Condition{{Mask: 0b01, Value: 0b01}},
		Ops:       []Operation{{Name: "set", Mask: 0b10, Value: 0b10}},
	}
	got, err := Plan(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusUnreachable || got.Reason != "initial state is forbidden" {
		t.Fatalf("unexpected result: %+v", got)
	}

	p.Initial = 0
	p.Forbidden = []Condition{{Mask: 0b10, Value: 0b10}}
	got, err = Plan(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusUnreachable || got.Reason != "target state is forbidden" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestWidthValidation(t *testing.T) {
	_, err := Plan(Problem{Bits: 25, Target: 1})
	if !errors.Is(err, ErrTooManyBits) {
		t.Fatalf("err = %v, want ErrTooManyBits", err)
	}
	_, err = Plan(Problem{Bits: 4, Initial: 0b10000})
	if !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("err = %v, want ErrOutOfRange", err)
	}
}

func TestMaskedOutValueBitsAreIgnored(t *testing.T) {
	p := Problem{
		Bits:    2,
		Initial: 0,
		Target:  0b01,
		Ops: []Operation{{
			Name:         "set",
			Mask:         0b01,
			Value:        0xffffffff, // high bits are outside Mask
			RequireMask:  0b10,
			RequireValue: 0xfffffffc, // high bits are outside RequireMask
		}},
		Forbidden: []Condition{{Mask: 0b10, Value: 0xffffffff}},
	}
	got, err := Plan(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete() || len(got.Steps) != 1 || got.Steps[0].To != 0b01 {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestInferWidth(t *testing.T) {
	p := Problem{
		Initial: 0,
		Target:  0b1000,
		Ops:     []Operation{{Name: "set", Mask: 0b1000, Value: 0b1000}},
	}
	got, err := Plan(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete() || len(got.Steps) != 1 {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestPlanIsDeterministic(t *testing.T) {
	p := Problem{
		Bits:    3,
		Initial: 0,
		Target:  0b111,
		Ops: []Operation{
			{Name: "a", Mask: 0b001, Value: 0b001},
			{Name: "b", Mask: 0b010, Value: 0b010},
			{Name: "c", Mask: 0b100, Value: 0b100},
		},
	}
	first, err := Plan(p)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		got, err := Plan(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Steps) != len(first.Steps) {
			t.Fatalf("length changed: %d vs %d", len(got.Steps), len(first.Steps))
		}
		for j := range got.Steps {
			if got.Steps[j].Op != first.Steps[j].Op {
				t.Fatalf("step %d changed", j)
			}
		}
	}
}

func assertPlanValid(t *testing.T, p Problem, result Result) {
	t.Helper()
	n, err := p.normalize()
	if err != nil {
		t.Fatal(err)
	}
	state := p.Initial
	if n.forbidden(state) {
		t.Fatal("initial state forbidden")
	}
	if len(result.States) != len(result.Steps)+1 {
		t.Fatalf("states=%d steps=%d", len(result.States), len(result.Steps))
	}
	if result.States[0] != p.Initial {
		t.Fatalf("first state = %#b, want initial %#b", result.States[0], p.Initial)
	}
	for i, step := range result.Steps {
		if step.Op < 0 || step.Op >= len(p.Ops) {
			t.Fatalf("bad op index %d", step.Op)
		}
		next, ok := applyOp(state, p.Ops[step.Op], n.locks, n.problem.Locks, n.globalFreeze)
		if !ok {
			t.Fatalf("step %d not applicable", i)
		}
		if next != step.To {
			t.Fatalf("step %d produced %#b, reported %#b", i, next, step.To)
		}
		if n.forbidden(next) {
			t.Fatalf("step %d entered forbidden state %#b", i, next)
		}
		state = next
	}
	if !n.goal(state) {
		t.Fatalf("final state %#b does not satisfy goal", state)
	}
}

func popcount24(v uint32) int {
	return bits.OnesCount32(v & ((1 << MaxStateBits) - 1))
}

func TestStateSpaceBound(t *testing.T) {
	if got := popcount24(1<<MaxStateBits - 1); got != MaxStateBits {
		t.Fatalf("popcount = %d", got)
	}
}
