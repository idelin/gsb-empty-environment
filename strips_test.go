package plango

import "testing"

// testProblem models a small boot pipeline with a non-monotonic calibrate
// step that deletes "loaded" when adding "calibrated".
func testProblem() Problem {
	return Problem{
		Actions: []Action{
			{Name: "boot", Pre: Caps(), Add: Caps(0)},
			{Name: "load", Pre: Caps(0), Add: Caps(1)},
			{Name: "calibrate", Pre: Caps(1), Add: Caps(2), Del: Caps(1)},
			{Name: "deploy", Pre: Caps(0, 2), Add: Caps(3)},
			{Name: "shortcut", Pre: Caps(0), Add: Caps(1, 2)},
		},
		Initial:   Caps(),
		Goal:      Caps(3),
		Forbidden: []State{Caps(1, 2)}, // loaded+calibrated together is unsafe
	}
}

func TestSolveShortestAvoidingForbidden(t *testing.T) {
	plan, ok := Solve(testProblem())
	if !ok {
		t.Fatal("expected a plan")
	}
	// shortcut would give a 2-step plan (boot, shortcut, deploy needs c2...
	// shortcut sets c1,c2 but that state is forbidden), so the shortest safe
	// plan is boot, load, calibrate, deploy.
	if len(plan.Actions) != 4 {
		t.Fatalf("expected 4 actions, got %d: %v", len(plan.Actions), plan.Actions)
	}
	wantStates := []State{Caps(), Caps(0), Caps(0, 1), Caps(0, 2), Caps(0, 2, 3)}
	if len(plan.States) != len(wantStates) {
		t.Fatalf("expected %d states, got %d", len(wantStates), len(plan.States))
	}
	for i, want := range wantStates {
		if plan.States[i] != want {
			t.Fatalf("step %d: want %s, got %s", i, want, plan.States[i])
		}
	}
	for _, a := range plan.Actions {
		if a.Name == "shortcut" {
			t.Fatal("plan uses forbidden shortcut")
		}
	}
}

func TestInitialGoalReturnsEmptyPlan(t *testing.T) {
	p := testProblem()
	p.Initial = Caps(0, 2, 3)
	plan, ok := Solve(p)
	if !ok {
		t.Fatal("expected a plan")
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("expected empty plan, got %d actions", len(plan.Actions))
	}
	if len(plan.States) != 1 || plan.States[0] != p.Initial {
		t.Fatalf("expected single initial state, got %v", plan.States)
	}
}

func TestUnsolvable(t *testing.T) {
	p := testProblem()
	p.Actions = p.Actions[:3] // no deploy, no shortcut: c3 unreachable
	if _, ok := Solve(p); ok {
		t.Fatal("expected no plan")
	}
}

func TestForbiddenInitial(t *testing.T) {
	p := testProblem()
	p.Initial = Caps(1, 2)
	if _, ok := Solve(p); ok {
		t.Fatal("expected no plan from forbidden initial state")
	}
}

func TestReplayCrossCheck(t *testing.T) {
	p := testProblem()
	plan, err := Verify(p)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if len(plan.Actions) != 4 {
		t.Fatalf("expected 4 actions, got %d", len(plan.Actions))
	}
}

func TestReplayRejectsNonMonotonicMisuse(t *testing.T) {
	p := testProblem()
	// calibrate deletes c1; a hand-written plan that tries to deploy right
	// after calibrate without c0... deploy needs c0 and c2, fine, but
	// calibrating before loading must fail applicability.
	bad := []Action{
		{Name: "boot", Pre: Caps(), Add: Caps(0)},
		{Name: "calibrate", Pre: Caps(1), Add: Caps(2), Del: Caps(1)},
	}
	if _, err := Replay(p, bad); err == nil {
		t.Fatal("expected replay to reject calibrate before load")
	}
	// A plan that lands on the forbidden {c1,c2} state must be rejected even
	// though it reaches the goal afterwards.
	dirty := []Action{
		{Name: "boot", Pre: Caps(), Add: Caps(0)},
		{Name: "shortcut", Pre: Caps(0), Add: Caps(1, 2)},
		{Name: "deploy", Pre: Caps(0, 2), Add: Caps(3)},
	}
	if _, err := Replay(p, dirty); err == nil {
		t.Fatal("expected replay to reject forbidden intermediate state")
	}
}

func TestDeleteEffectsAreNonMonotonic(t *testing.T) {
	// calibrate clears c1: the state after calibrate must NOT contain c1,
	// and a later load must be able to re-add it.
	p := Problem{
		Actions: []Action{
			{Name: "load", Pre: Caps(), Add: Caps(1)},
			{Name: "calibrate", Pre: Caps(1), Add: Caps(2), Del: Caps(1)},
			{Name: "reload", Pre: Caps(2), Add: Caps(1)},
		},
		Initial: Caps(),
		Goal:    Caps(1, 2),
	}
	plan, err := Verify(p)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if len(plan.Actions) != 3 {
		t.Fatalf("expected 3 actions, got %d", len(plan.Actions))
	}
	mid := plan.States[2]
	if mid.Has(Caps(1)) {
		t.Fatalf("calibrate should have deleted c1, state %s", mid)
	}
	if !plan.States[3].Has(Caps(1, 2)) {
		t.Fatalf("final state should hold c1 and c2, got %s", plan.States[3])
	}
}
