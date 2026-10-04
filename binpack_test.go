package binpack

import (
	"errors"
	"reflect"
	"testing"
)

// Classic first-fit failure: greedy needs 3 bins, the optimum is 2.
// The solver must not stop at the first (greedy-shaped) solution.
func TestOptimalBeatsFirstSolution(t *testing.T) {
	inst := Instance{Capacity: 10, Sizes: []int{4, 4, 3, 3, 3, 3}}
	sol, err := Solve(inst, -1)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !sol.Optimal {
		t.Fatal("unlimited budget must prove optimality")
	}
	want := [][]int{{0, 2, 3}, {1, 4, 5}}
	if !reflect.DeepEqual(sol.Bins, want) {
		t.Fatalf("bins = %v, want %v", sol.Bins, want)
	}
	if !reflect.DeepEqual(sol.Loads, []int{10, 10}) {
		t.Fatalf("loads = %v, want [10 10]", sol.Loads)
	}
	if sol.LowerBound != 2 {
		t.Fatalf("lower bound = %d, want 2", sol.LowerBound)
	}
	if err := inst.Verify(sol); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Conflicting items must not share a bin even when capacity allows it.
func TestConflictsForceSeparation(t *testing.T) {
	inst := Instance{
		Capacity:  10,
		Sizes:     []int{6, 4, 4},
		Conflicts: [][2]int{{0, 1}},
	}
	sol, err := Solve(inst, -1)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	want := [][]int{{0}, {1, 2}}
	if !reflect.DeepEqual(sol.Bins, want) {
		t.Fatalf("bins = %v, want %v", sol.Bins, want)
	}
	if !reflect.DeepEqual(sol.Loads, []int{6, 8}) {
		t.Fatalf("loads = %v, want [6 8]", sol.Loads)
	}
	if sol.LowerBound != 2 {
		t.Fatalf("lower bound = %d, want 2", sol.LowerBound)
	}
	if err := inst.Verify(sol); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Zero-size items still obey the conflict graph: a triangle of
// conflicting zero-size items needs 3 bins, and the clique lower
// bound must reflect that even though the capacity bound is 0.
func TestZeroSizeItemsRespectConflicts(t *testing.T) {
	inst := Instance{
		Capacity:  5,
		Sizes:     []int{0, 0, 0},
		Conflicts: [][2]int{{0, 1}, {0, 2}, {1, 2}},
	}
	sol, err := Solve(inst, -1)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	want := [][]int{{0}, {1}, {2}}
	if !reflect.DeepEqual(sol.Bins, want) {
		t.Fatalf("bins = %v, want %v", sol.Bins, want)
	}
	if !reflect.DeepEqual(sol.Loads, []int{0, 0, 0}) {
		t.Fatalf("loads = %v, want [0 0 0]", sol.Loads)
	}
	if sol.LowerBound != 3 {
		t.Fatalf("lower bound = %d, want 3 (clique bound)", sol.LowerBound)
	}
	if err := inst.Verify(sol); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Zero-size items without conflicts may share a single bin.
func TestZeroSizeItemsShareBin(t *testing.T) {
	inst := Instance{Capacity: 5, Sizes: []int{0, 0, 0}}
	sol, err := Solve(inst, -1)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(sol.Bins) != 1 {
		t.Fatalf("bins = %v, want a single bin", sol.Bins)
	}
	if sol.LowerBound != 1 {
		t.Fatalf("lower bound = %d, want 1 (non-empty instance)", sol.LowerBound)
	}
}

// A single item larger than the capacity makes the instance infeasible.
func TestSingleItemExceedsCapacity(t *testing.T) {
	inst := Instance{Capacity: 10, Sizes: []int{5, 11}}
	if _, err := Solve(inst, -1); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("err = %v, want ErrInfeasible", err)
	}
}

// A tight node budget must not pass the incumbent off as optimal.
func TestBudgetNeverClaimsOptimality(t *testing.T) {
	inst := Instance{Capacity: 10, Sizes: []int{4, 4, 3, 3, 3, 3}}

	tight, err := Solve(inst, 2)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if tight.Optimal {
		t.Fatal("budget-exhausted run reported optimal")
	}
	if tight.Nodes > 2 {
		t.Fatalf("expanded %d nodes over budget 2", tight.Nodes)
	}
	// The incumbent is still feasible evidence, just not proven.
	if err := inst.Verify(tight); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	full, err := Solve(inst, -1)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !full.Optimal || len(full.Bins) != 2 {
		t.Fatalf("full search: optimal=%v bins=%v, want optimal with 2 bins", full.Optimal, full.Bins)
	}
	if len(tight.Bins) < len(full.Bins) {
		t.Fatalf("incumbent %d bins beats proven optimum %d bins", len(tight.Bins), len(full.Bins))
	}
}

// Among minimum-bin packings, return the lexicographically smallest
// normalized grouping.
func TestLexicographicTieBreak(t *testing.T) {
	inst := Instance{Capacity: 10, Sizes: []int{5, 5, 5, 5}}
	sol, err := Solve(inst, -1)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	want := [][]int{{0, 1}, {2, 3}}
	if !reflect.DeepEqual(sol.Bins, want) {
		t.Fatalf("bins = %v, want %v", sol.Bins, want)
	}
}

// Lower bound must be valid: never above the proven optimum.
func TestLowerBoundValidity(t *testing.T) {
	instances := []Instance{
		{Capacity: 10, Sizes: []int{6, 5, 4, 3, 2}},
		{Capacity: 7, Sizes: []int{4, 4, 4, 4}, Conflicts: [][2]int{{0, 1}, {2, 3}}},
		{Capacity: 3, Sizes: []int{1, 1, 1, 1, 1}},
		{Capacity: 10, Sizes: []int{9, 8, 7}, Conflicts: [][2]int{{0, 1}, {1, 2}, {0, 2}}},
	}
	for i, inst := range instances {
		sol, err := Solve(inst, -1)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if !sol.Optimal {
			t.Fatalf("case %d: not proven optimal", i)
		}
		if sol.LowerBound > len(sol.Bins) {
			t.Fatalf("case %d: lower bound %d exceeds optimum %d", i, sol.LowerBound, len(sol.Bins))
		}
		if err := inst.Verify(sol); err != nil {
			t.Fatalf("case %d: Verify: %v", i, err)
		}
	}
}

// Edge cases: empty instance and zero-capacity zero-size instance.
func TestEdgeCases(t *testing.T) {
	sol, err := Solve(Instance{Capacity: 10}, -1)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !sol.Optimal || len(sol.Bins) != 0 || sol.LowerBound != 0 {
		t.Fatalf("empty instance: %+v", sol)
	}

	sol, err = Solve(Instance{Capacity: 0, Sizes: []int{0, 0}}, -1)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !sol.Optimal || len(sol.Bins) != 1 {
		t.Fatalf("zero-capacity zero-size: %+v", sol)
	}
	if err := (Instance{Capacity: 0, Sizes: []int{0, 0}}).Verify(sol); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Verify must reject tampered evidence.
func TestVerifyRejectsInvalid(t *testing.T) {
	inst := Instance{
		Capacity:  10,
		Sizes:     []int{6, 4, 4},
		Conflicts: [][2]int{{0, 1}},
	}
	cases := []Solution{
		{Bins: [][]int{{0, 1}, {2}}, Loads: []int{10, 4}},                // conflict violation
		{Bins: [][]int{{0}, {1, 2}}, Loads: []int{6, 12}},                // capacity violation
		{Bins: [][]int{{0, 2}}, Loads: []int{10}},                        // item 1 missing
		{Bins: [][]int{{0, 2}, {1, 1}}, Loads: []int{10, 8}},             // duplicate item
		{Bins: [][]int{{0, 2}, {1}}, Loads: []int{10, 4}, LowerBound: 3}, // bound above bin count
	}
	for i, sol := range cases {
		if err := inst.Verify(sol); err == nil {
			t.Fatalf("case %d: invalid solution passed Verify", i)
		}
	}
}
