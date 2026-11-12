package netsec

import (
	"errors"
	"testing"
)

func bit(b uint) Mask { return 1 << b }

func TestPlanResetSimpleClearsAll(t *testing.T) {
	actions := []Action{
		{Name: "wipe", Clear: bit(0) | bit(1) | bit(2)},
	}

	plan, err := PlanReset(actions)
	if err != nil {
		t.Fatalf("PlanReset returned error: %v", err)
	}
	if plan.Width != 3 {
		t.Fatalf("Width = %d, want 3", plan.Width)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1", len(plan.Steps))
	}
	if plan.Steps[0].ActionIndex != 0 {
		t.Fatalf("ActionIndex = %d, want 0", plan.Steps[0].ActionIndex)
	}
	if plan.Steps[0].Before != 0 {
		t.Fatalf("Before = %b, want 0", plan.Steps[0].Before)
	}
	if want := bit(0) | bit(1) | bit(2); plan.Final != want {
		t.Fatalf("Final = %b, want %b", plan.Final, want)
	}
	for index, state := range plan.Steps[0].AfterBits {
		if state != BitZero {
			t.Fatalf("AfterBits[%d] = %s, want zero", index, state)
		}
	}
	if err := plan.Verify(actions, 0); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
}

func TestPlanResetPreconditionSequence(t *testing.T) {
	// unlock only legal once bit 0 is zero; wipe then clears bit 1.
	actions := []Action{
		{Name: "unlock", RequireZero: bit(0), Clear: bit(1)},
		{Name: "keyzero", Clear: bit(0)},
	}

	plan, err := PlanReset(actions)
	if err != nil {
		t.Fatalf("PlanReset returned error: %v", err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2", len(plan.Steps))
	}
	if plan.Steps[0].ActionIndex != 1 || plan.Steps[1].ActionIndex != 0 {
		t.Fatalf("order = [%d %d], want [1 0]",
			plan.Steps[0].ActionIndex, plan.Steps[1].ActionIndex)
	}
	if plan.Steps[1].Before != bit(0) || plan.Steps[1].After != bit(0)|bit(1) {
		t.Fatalf("unexpected unlock states: before=%b after=%b",
			plan.Steps[1].Before, plan.Steps[1].After)
	}

	// The precondition really blocks the action on the unknown register.
	if actions[0].Legal(0) {
		t.Fatal("unlock must be illegal before bit 0 is zero")
	}
	if _, err := actions[0].Step(0); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("Step error = %v, want ErrPrecondition", err)
	}
	if err := plan.Verify(actions, 0); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
}

func TestPlanResetTaintOrdering(t *testing.T) {
	// first clears bit 0 but dirties bit 1; second clears bit 1 without
	// side effects, so only [first, second] reaches the all-zero state.
	actions := []Action{
		{Name: "first", Clear: bit(0), Taint: bit(1)},
		{Name: "second", Clear: bit(1)},
	}

	plan, err := PlanReset(actions)
	if err != nil {
		t.Fatalf("PlanReset returned error: %v", err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2", len(plan.Steps))
	}
	if plan.Steps[0].ActionIndex != 0 || plan.Steps[1].ActionIndex != 1 {
		t.Fatalf("order = [%d %d], want [0 1]",
			plan.Steps[0].ActionIndex, plan.Steps[1].ActionIndex)
	}
	if plan.Steps[0].After != bit(0) {
		t.Fatalf("after first = %b, want %b", plan.Steps[0].After, bit(0))
	}
	if plan.Final != bit(0)|bit(1) {
		t.Fatalf("Final = %b, want all zero", plan.Final)
	}
	if err := plan.Verify(actions, 0); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
}

func TestPlanResetClearWinsOverTaint(t *testing.T) {
	// Re-unknowning is the final effect: a bit both cleared and tainted ends
	// unknown, so this action alone cannot prove the register clean.
	action := Action{Name: "self-taint", Clear: bit(0), Taint: bit(0)}
	if got := action.Apply(0); got != 0 {
		t.Fatalf("Apply = %b, want 0 (taint wins on overlap)", got)
	}
	if _, err := PlanReset([]Action{action}); !errors.Is(err, ErrUnclearable) {
		t.Fatalf("error = %v, want ErrUnclearable", err)
	}

	// A second action that clears the bit without tainting completes the job.
	plan, err := PlanReset([]Action{
		action,
		{Name: "final-wipe", Clear: bit(0)},
	})
	if err != nil {
		t.Fatalf("PlanReset returned error: %v", err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].ActionIndex != 1 {
		t.Fatalf("plan = %+v, want single final-wipe step", plan.Steps)
	}
}

func TestPlanResetUnclearable(t *testing.T) {
	// Bit 1 is a tracked secret (it appears in a mask) but no action ever
	// clears it, so the register cannot be fully cleaned.
	actions := []Action{
		{Name: "wipe0", Clear: bit(0)},
		{Name: "taunt", Taint: bit(1)},
	}

	_, err := PlanReset(actions)
	if !errors.Is(err, ErrUnclearable) {
		t.Fatalf("error = %v, want ErrUnclearable", err)
	}
}

func TestPlanResetPreconditionBlockedForever(t *testing.T) {
	// unlock needs bit 0 zero, but nothing can clear bit 0.
	actions := []Action{
		{Name: "unlock", RequireZero: bit(0), Clear: bit(1)},
	}

	_, err := PlanReset(actions)
	if !errors.Is(err, ErrUnclearable) {
		t.Fatalf("error = %v, want ErrUnclearable", err)
	}
}

func TestPlanResetLimitExceeded(t *testing.T) {
	// A strictly ordered chain of 21 secret bits. Action i requires bit i+1
	// to already be zero, clears bit i, and re-unknows every lower bit, so
	// each abstract state has exactly one legal successor and the only
	// reaching order needs 21 actions (one more than the cap).
	const width = MaxActions + 1
	actions := make([]Action, width)
	for index := range actions {
		action := Action{Name: "wipe", Clear: bit(uint(index))}
		for lower := 0; lower < index; lower++ {
			action.Taint |= bit(uint(lower))
		}
		if index+1 < width {
			action.RequireZero = bit(uint(index + 1))
		}
		actions[index] = action
	}

	_, err := PlanReset(actions)
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("error = %v, want ErrLimitExceeded", err)
	}

	// The same chain of exactly MaxActions bits fits within the cap.
	solvable := make([]Action, MaxActions)
	for index := range solvable {
		action := Action{Name: "wipe", Clear: bit(uint(index))}
		for lower := 0; lower < index; lower++ {
			action.Taint |= bit(uint(lower))
		}
		if index+1 < MaxActions {
			action.RequireZero = bit(uint(index + 1))
		}
		solvable[index] = action
	}
	ok, err := PlanReset(solvable)
	if err != nil {
		t.Fatalf("PlanReset returned error: %v", err)
	}
	if len(ok.Steps) != MaxActions {
		t.Fatalf("len(Steps) = %d, want %d", len(ok.Steps), MaxActions)
	}
	if err := ok.Verify(solvable, 0); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
}

func TestPlanResetAlreadyClean(t *testing.T) {
	actions := []Action{{Name: "wipe", Clear: bit(0)}}
	initial := []BitState{BitZero}

	plan, err := PlanResetFrom(actions, initial)
	if err != nil {
		t.Fatalf("PlanResetFrom returned error: %v", err)
	}
	if len(plan.Steps) != 0 {
		t.Fatalf("len(Steps) = %d, want 0", len(plan.Steps))
	}
	if plan.Final != bit(0) {
		t.Fatalf("Final = %b, want %b", plan.Final, bit(0))
	}
	if err := plan.Verify(actions, bit(0)); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
}

func TestPlanResetFromInvalidState(t *testing.T) {
	_, err := PlanResetFrom(nil, []BitState{BitState(99)})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("error = %v, want ErrInvalidState", err)
	}

	if _, err := Pack([]BitState{BitState(7)}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Pack error = %v, want ErrInvalidState", err)
	}
}

func TestMaskAndStatesRoundTrip(t *testing.T) {
	packed, err := Pack([]BitState{BitZero, BitUnknown, BitZero})
	if err != nil {
		t.Fatalf("Pack returned error: %v", err)
	}
	if packed != bit(0)|bit(2) {
		t.Fatalf("Pack = %b, want %b", packed, bit(0)|bit(2))
	}
	bits := States(packed, 3)
	want := []BitState{BitZero, BitUnknown, BitZero}
	for index := range want {
		if bits[index] != want[index] {
			t.Fatalf("States[%d] = %s, want %s", index, bits[index], want[index])
		}
	}
	if BitUnknown.String() != "unknown" || BitZero.String() != "zero" {
		t.Fatalf("unexpected BitState strings: %q %q", BitUnknown, BitZero)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	actions := []Action{{Name: "wipe", Clear: bit(0)}}
	plan, err := PlanReset(actions)
	if err != nil {
		t.Fatalf("PlanReset returned error: %v", err)
	}

	corrupt := *plan
	tamperedStep := plan.Steps[0]
	tamperedStep.After = 0
	corrupt.Steps = []Step{tamperedStep}
	if err := corrupt.Verify(actions, 0); !errors.Is(err, ErrTrace) {
		t.Fatalf("Verify error = %v, want ErrTrace", err)
	}

	notClean := *plan
	dirtyStep := plan.Steps[0]
	dirtyStep.After = 0
	notClean.Steps = []Step{dirtyStep}
	if err := notClean.Verify(actions, 0); !errors.Is(err, ErrTrace) {
		t.Fatalf("Verify error = %v, want ErrTrace", err)
	}
}

func TestShortestPathIsReturned(t *testing.T) {
	// A redundant slow action clears nothing new; BFS must prefer the direct
	// two-bit wipe and produce a one-step plan.
	actions := []Action{
		{Name: "noop-ish", Clear: bit(0), Taint: bit(0)},
		{Name: "full-wipe", Clear: bit(0) | bit(1)},
	}

	plan, err := PlanReset(actions)
	if err != nil {
		t.Fatalf("PlanReset returned error: %v", err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].ActionIndex != 1 {
		t.Fatalf("plan = %+v, want single full-wipe step", plan.Steps)
	}
}

func TestPlanResetNoSecretBits(t *testing.T) {
	plan, err := PlanReset(nil)
	if err != nil {
		t.Fatalf("PlanReset returned error: %v", err)
	}
	if plan.Width != 0 || len(plan.Steps) != 0 || plan.Final != 0 {
		t.Fatalf("unexpected empty-register plan: %+v", plan)
	}
	if err := plan.Verify(nil, 0); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
}

func TestVerifyReportsNotClearedAndBadTrace(t *testing.T) {
	actions := []Action{{Name: "wipe", Clear: bit(0)}}
	plan, err := PlanReset(actions)
	if err != nil {
		t.Fatalf("PlanReset returned error: %v", err)
	}

	// A plan whose trace is internally consistent but whose width declares an
	// extra never-cleared secret bit must fail with ErrNotCleared.
	extraBit := *plan
	extraBit.Width = 2
	if err := extraBit.Verify(actions, 0); !errors.Is(err, ErrNotCleared) {
		t.Fatalf("Verify error = %v, want ErrNotCleared", err)
	}

	// Wrong action table entry is rejected as a trace mismatch.
	if err := plan.Verify([]Action{{Name: "other", Clear: bit(0)}}, 0); !errors.Is(err, ErrTrace) {
		t.Fatalf("Verify error = %v, want ErrTrace", err)
	}

	var nilPlan *Plan
	if err := nilPlan.Verify(actions, 0); !errors.Is(err, ErrTrace) {
		t.Fatalf("Verify error = %v, want ErrTrace", err)
	}
}
