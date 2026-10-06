package plancmp

import (
	"reflect"
	"strings"
	"testing"
)

func mustPlan(t *testing.T, ops ...Op) *Plan {
	t.Helper()
	p, err := NewPlan(ops...)
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	return p
}

func TestMinimalSupplementAcrossPlans(t *testing.T) {
	oldPlan := mustPlan(t,
		Op{Name: "a", Eff: Assign{0: true}},
		Op{Name: "b", Deps: []string{"a"}, Eff: Assign{1: true}},
	)
	newPlan := mustPlan(t,
		Op{Name: "c", Deps: []string{"b"}, Eff: Assign{2: true}},
	)
	res, err := Reconcile(oldPlan, newPlan, 0, []string{"a"}, Assign{2: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !res.Reachable {
		t.Fatalf("expected reachable, explored=%v", res.Explored)
	}
	if want := []string{"b", "c"}; !reflect.DeepEqual(res.Steps, want) {
		t.Fatalf("steps = %v, want %v", res.Steps, want)
	}
}

func TestShortestPathChosen(t *testing.T) {
	p := mustPlan(t,
		Op{Name: "fast", Eff: Assign{3: true}},
		Op{Name: "d1", Eff: Assign{4: true}},
		Op{Name: "d2", Deps: []string{"d1"}, Eff: Assign{3: true}},
	)
	res, err := Reconcile(p, nil, 0, nil, Assign{3: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if want := []string{"fast"}; !reflect.DeepEqual(res.Steps, want) {
		t.Fatalf("steps = %v, want %v", res.Steps, want)
	}
}

func TestGoalAlreadySatisfied(t *testing.T) {
	p := mustPlan(t, Op{Name: "a", Eff: Assign{0: true}})
	res, err := Reconcile(p, nil, 0, []string{"a"}, Assign{0: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !res.Reachable || len(res.Steps) != 0 {
		t.Fatalf("expected reachable with no steps, got %+v", res)
	}
}

func TestUnreachableReturnsCompleteSearchRange(t *testing.T) {
	p := mustPlan(t,
		Op{Name: "a", Eff: Assign{0: true}},
		Op{Name: "b", Deps: []string{"a"}, Eff: Assign{1: true}},
	)
	// Nothing ever sets variable 5.
	res, err := Reconcile(p, nil, 0, nil, Assign{5: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Reachable {
		t.Fatal("expected unreachable")
	}
	// The complete search range is: {}, {a}, {a,b} over states 0, 1, 3.
	want := []ExploreNode{
		{State: 0, Done: nil},
		{State: 1, Done: []string{"a"}},
		{State: 3, Done: []string{"a", "b"}},
	}
	if !reflect.DeepEqual(res.Explored, want) {
		t.Fatalf("explored = %+v, want %+v", res.Explored, want)
	}
}

func TestCommitmentProtectsExecutedEffects(t *testing.T) {
	oldPlan := mustPlan(t,
		Op{Name: "a", Eff: Assign{0: true}},
		Op{Name: "slow1", Eff: Assign{2: true}},
		Op{Name: "slow2", Deps: []string{"slow1"}, Eff: Assign{1: true}},
	)
	// "reset" would reach the goal in one step but revokes the committed
	// effect of the already-executed "a"; it must be excluded from search.
	newPlan := mustPlan(t,
		Op{Name: "reset", Eff: Assign{0: false, 1: true}},
	)
	res, err := Reconcile(oldPlan, newPlan, 0, []string{"a"}, Assign{1: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if want := []string{"slow1", "slow2"}; !reflect.DeepEqual(res.Steps, want) {
		t.Fatalf("steps = %v, want %v", res.Steps, want)
	}
	// The final state still honours the commitment of "a".
	merged, err := MergePlans(oldPlan, newPlan)
	if err != nil {
		t.Fatalf("MergePlans: %v", err)
	}
	if err := merged.Verify(0, []string{"a"}, res.Steps, Assign{0: true, 1: true}); err != nil {
		t.Fatalf("Verify solution: %v", err)
	}
}

func TestVerifyRejectsSilentReset(t *testing.T) {
	p := mustPlan(t,
		Op{Name: "a", Eff: Assign{0: true}},
		Op{Name: "reset", Eff: Assign{0: false, 1: true}},
	)
	err := p.Verify(0, []string{"a"}, []string{"reset"}, Assign{1: true})
	if err == nil || !strings.Contains(err.Error(), "silently resets committed variable 0") {
		t.Fatalf("expected silent-reset error, got %v", err)
	}
}

func TestUnreachableWhenOnlyPathResetsCommitment(t *testing.T) {
	p := mustPlan(t,
		Op{Name: "a", Eff: Assign{0: true}},
		Op{Name: "reset", Eff: Assign{0: false, 1: true}},
	)
	res, err := Reconcile(p, nil, 0, []string{"a"}, Assign{1: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Reachable {
		t.Fatalf("expected unreachable, steps=%v", res.Steps)
	}
	if len(res.Explored) != 1 || res.Explored[0].State != 1 {
		t.Fatalf("explored = %+v, want only the committed state", res.Explored)
	}
}

func TestPreconditionsGateSearch(t *testing.T) {
	p := mustPlan(t,
		Op{Name: "key", Eff: Assign{0: true}},
		Op{Name: "door", Pre: Assign{0: true}, Eff: Assign{1: true}},
	)
	res, err := Reconcile(p, nil, 0, nil, Assign{1: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if want := []string{"key", "door"}; !reflect.DeepEqual(res.Steps, want) {
		t.Fatalf("steps = %v, want %v", res.Steps, want)
	}
}

func TestReplayRejectsBadExecutedPrefix(t *testing.T) {
	p := mustPlan(t,
		Op{Name: "a", Eff: Assign{0: true}},
		Op{Name: "b", Deps: []string{"a"}, Eff: Assign{1: true}},
		Op{Name: "c", Pre: Assign{0: true}, Eff: Assign{2: true}},
	)
	if _, err := Reconcile(p, nil, 0, []string{"b"}, Assign{1: true}); err == nil {
		t.Fatal("expected error: executed op ran before its dependency")
	}
	if _, err := Reconcile(p, nil, 0, []string{"c"}, Assign{2: true}); err == nil {
		t.Fatal("expected error: executed op preconditions unmet at replay")
	}
	if _, err := Reconcile(p, nil, 0, []string{"ghost"}, Assign{0: true}); err == nil {
		t.Fatal("expected error: unknown executed op")
	}
}

func TestVerifyRejectsOutOfOrderAndUnknownSteps(t *testing.T) {
	p := mustPlan(t,
		Op{Name: "a", Eff: Assign{0: true}},
		Op{Name: "b", Deps: []string{"a"}, Eff: Assign{1: true}},
	)
	if err := p.Verify(0, nil, []string{"b"}, Assign{1: true}); err == nil {
		t.Fatal("expected dependency error")
	}
	if err := p.Verify(0, nil, []string{"ghost"}, Assign{0: true}); err == nil {
		t.Fatal("expected unknown-step error")
	}
	if err := p.Verify(0, nil, []string{"a"}, Assign{1: true}); err == nil {
		t.Fatal("expected goal-not-reached error")
	}
}

func TestMergeConflictingDefinitions(t *testing.T) {
	oldPlan := mustPlan(t, Op{Name: "a", Eff: Assign{0: true}})
	newPlan := mustPlan(t, Op{Name: "a", Eff: Assign{0: false}})
	if _, err := Reconcile(oldPlan, newPlan, 0, nil, Assign{0: true}); err == nil {
		t.Fatal("expected conflicting-definition error")
	}
	// Identical duplicates are fine.
	dup := mustPlan(t, Op{Name: "a", Eff: Assign{0: true}})
	res, err := Reconcile(oldPlan, dup, 0, nil, Assign{0: true})
	if err != nil || !res.Reachable {
		t.Fatalf("identical duplicate should merge cleanly: %v %+v", err, res)
	}
}

func TestVariableRangeEnforced(t *testing.T) {
	p := mustPlan(t, Op{Name: "a", Eff: Assign{0: true}})
	if _, err := Reconcile(p, nil, 0, nil, Assign{MaxVars: true}); err == nil {
		t.Fatal("expected out-of-range goal variable error")
	}
	if _, err := NewPlan(Op{Name: "bad", Eff: Assign{-1: true}}); err == nil {
		t.Fatal("expected out-of-range effect variable error")
	}
}
