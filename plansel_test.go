package plansel

import (
	"reflect"
	"testing"
	"time"
)

var noDeadline time.Time

func mustSolve(t *testing.T, inst Instance) Result {
	t.Helper()
	res, err := Solve(inst, noDeadline)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.Status != Optimal {
		t.Fatalf("status = %v, want Optimal", res.Status)
	}
	if err := Verify(inst, View{}, res); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return res
}

// The shared group fee is charged exactly once even when several plans of
// the group are selected.
func TestSharedFeeChargedOnce(t *testing.T) {
	inst := Instance{
		Budget:   100,
		GroupFee: map[int]int64{1: 10},
		Plans: []Plan{
			{ID: 1, Group: 1, Cost: 5, Profit: 8},
			{ID: 2, Group: 1, Cost: 5, Profit: 9},
			{ID: 3, Group: 2, Cost: 4, Profit: 6},
		},
	}
	res := mustSolve(t, inst)
	if want := []int{1, 2, 3}; !reflect.DeepEqual(res.Selected, want) {
		t.Fatalf("Selected = %v, want %v", res.Selected, want)
	}
	// 5+5+4 costs + one fee of 10 for group 1 (not twice).
	if res.Cost != 24 {
		t.Fatalf("Cost = %d, want 24 (group fee charged once)", res.Cost)
	}
	if res.Profit != 23 {
		t.Fatalf("Profit = %d, want 23", res.Profit)
	}
}

// The fee can make a whole group not worth activating.
func TestFeeDiscouragesGroup(t *testing.T) {
	inst := Instance{
		Budget:   10,
		GroupFee: map[int]int64{1: 8},
		Plans: []Plan{
			{ID: 1, Group: 1, Cost: 1, Profit: 4},
			{ID: 2, Group: 1, Cost: 1, Profit: 4},
			{ID: 3, Group: 2, Cost: 5, Profit: 7},
		},
	}
	res := mustSolve(t, inst)
	// {1,2}: profit 8, cost 1+1+8=10. {3}: profit 7, cost 5. {1,2} wins.
	if want := []int{1, 2}; !reflect.DeepEqual(res.Selected, want) {
		t.Fatalf("Selected = %v, want %v", res.Selected, want)
	}
	if res.Cost != 10 {
		t.Fatalf("Cost = %d, want 10", res.Cost)
	}
}

// A plan is never selected without its requirements.
func TestDependencyClosure(t *testing.T) {
	inst := Instance{
		Budget: 60,
		Plans: []Plan{
			{ID: 1, Cost: 1, Profit: 100, Requires: []int{2}},
			{ID: 2, Cost: 50, Profit: 1},
		},
	}
	res := mustSolve(t, inst)
	if want := []int{1, 2}; !reflect.DeepEqual(res.Selected, want) {
		t.Fatalf("Selected = %v, want %v", res.Selected, want)
	}

	// If the requirement does not fit the budget, the dependent plan is dropped.
	inst.Budget = 10
	res = mustSolve(t, inst)
	if len(res.Selected) != 0 {
		t.Fatalf("Selected = %v, want empty (requirement unaffordable)", res.Selected)
	}

	// The verifier rejects a non-closed selection.
	bad := Result{Status: Optimal, Selected: []int{1}, Profit: 100, Cost: 1, Lower: 100, Upper: 100}
	if err := Verify(inst, View{}, bad); err == nil {
		t.Fatal("Verify accepted a selection violating dependency closure")
	}
}

// Transitive requirements are pulled in.
func TestTransitiveDependencies(t *testing.T) {
	inst := Instance{
		Budget: 100,
		Plans: []Plan{
			{ID: 1, Cost: 1, Profit: 10, Requires: []int{2}},
			{ID: 2, Cost: 1, Profit: -5, Requires: []int{3}},
			{ID: 3, Cost: 1, Profit: -5},
			{ID: 4, Cost: 1, Profit: 3},
		},
	}
	res := mustSolve(t, inst)
	// {1,2,3}: profit 0; {4}: profit 3. {4} wins.
	if want := []int{4}; !reflect.DeepEqual(res.Selected, want) {
		t.Fatalf("Selected = %v, want %v", res.Selected, want)
	}
}

// Ties on profit are broken by lower total cost, then by the
// lexicographically smaller ID set.
func TestTieBreak(t *testing.T) {
	// Equal profit, different cost: cheaper wins.
	res := mustSolve(t, Instance{
		Budget: 2,
		Plans: []Plan{
			{ID: 1, Cost: 2, Profit: 5},
			{ID: 2, Cost: 1, Profit: 5},
		},
	})
	if want := []int{2}; !reflect.DeepEqual(res.Selected, want) {
		t.Fatalf("Selected = %v, want %v (cheaper tie)", res.Selected, want)
	}

	// Equal profit and cost: smaller ID set wins.
	res = mustSolve(t, Instance{
		Budget: 2,
		Plans: []Plan{
			{ID: 1, Cost: 2, Profit: 5},
			{ID: 2, Cost: 2, Profit: 5},
		},
	})
	if want := []int{1}; !reflect.DeepEqual(res.Selected, want) {
		t.Fatalf("Selected = %v, want %v (ID tie-break)", res.Selected, want)
	}

	// [1] beats [2,3] on equal profit and cost.
	res = mustSolve(t, Instance{
		Budget: 4,
		Plans: []Plan{
			{ID: 1, Cost: 4, Profit: 6},
			{ID: 2, Cost: 2, Profit: 3},
			{ID: 3, Cost: 2, Profit: 3},
		},
	})
	if want := []int{1}; !reflect.DeepEqual(res.Selected, want) {
		t.Fatalf("Selected = %v, want %v (ID-set tie-break)", res.Selected, want)
	}
}

// Invalid instances are rejected.
func TestValidation(t *testing.T) {
	cases := []Instance{
		{Plans: nil},
		{Plans: make([]Plan, MaxPlans+1)},
		{Plans: []Plan{{ID: 1}, {ID: 1}}},
		{Plans: []Plan{{ID: 1, Cost: -1}}},
		{Plans: []Plan{{ID: 1, Requires: []int{1}}}},
		{Plans: []Plan{{ID: 1, Requires: []int{9}}}},
		{Plans: []Plan{{ID: 1, Requires: []int{2}}, {ID: 2, Requires: []int{1}}}},
		{Plans: []Plan{{ID: 1}}, Budget: -1},
		{Plans: []Plan{{ID: 1}}, GroupFee: map[int]int64{0: -5}},
	}
	for i, inst := range cases {
		if _, err := Solve(inst, noDeadline); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}
