package maxcsp

import (
	"reflect"
	"testing"
)

// Acceptance test: the scheduling instance must yield the globally optimal
// schedule that violates exactly the two minor soft constraints S3 and S4
// with total penalty 7, while every hard constraint holds.
func TestSchedulingOptimum(t *testing.T) {
	p := NewSchedulingProblem()
	res := Solve(p)

	if res.Assignment == nil {
		t.Fatal("expected a feasible schedule, got none")
	}
	if res.Cost != 7 {
		t.Errorf("cost = %d, want 7", res.Cost)
	}
	wantViolated := []string{"S3-T3-even-slot", "S4-T4-early"}
	if !reflect.DeepEqual(res.Violated, wantViolated) {
		t.Errorf("violated = %v, want %v", res.Violated, wantViolated)
	}
	// Hard constraints must hold absolutely in the returned schedule.
	for _, c := range p.Constraints {
		if !c.Hard {
			continue
		}
		if violated, decided := c.Violated(res.Assignment); !decided || violated {
			t.Errorf("hard constraint %s violated by %v", c.Name, res.Assignment)
		}
	}
	t.Logf("optimal schedule: %s (penalty %d)", Describe(res.Assignment), res.Cost)
}

// The branch-and-bound result must match exhaustive enumeration.
func TestSolveMatchesBruteForce(t *testing.T) {
	p := NewSchedulingProblem()
	res := Solve(p)
	bestCost, best := BruteForce(p)
	if res.Cost != bestCost {
		t.Fatalf("Solve cost %d != brute-force cost %d", res.Cost, bestCost)
	}
	// Every soft constraint violated in the brute-force optimum must carry
	// the same total weight; also sanity-check the assignment is feasible.
	if best == nil {
		t.Fatal("brute force found no feasible assignment")
	}
}

// Branch and bound must actually prune instead of enumerating everything.
func TestPruningHappens(t *testing.T) {
	p := NewSchedulingProblem()
	res := Solve(p)
	if res.Pruned == 0 {
		t.Error("expected the search to prune at least one node")
	}
	// Full enumeration of 5 variables with domain size 5 expands
	// 5 + 5^2 + ... + 5^5 = 3905 inner nodes; pruning must stay far below.
	if res.Nodes >= 3905 {
		t.Errorf("nodes = %d, expected fewer than full enumeration (3905)", res.Nodes)
	}
	t.Logf("nodes=%d pruned=%d", res.Nodes, res.Pruned)
}

// Contradictory hard constraints make the problem infeasible.
func TestInfeasibleHardConstraints(t *testing.T) {
	p := &Problem{
		Variables: []Variable{{Name: "X", Domain: []int{1, 2}}},
		Constraints: []Constraint{
			{
				Name: "H-eq-1", Hard: true,
				Violated: func(a map[string]int) (bool, bool) {
					v, ok := a["X"]
					if !ok {
						return false, false
					}
					return v != 1, true
				},
			},
			{
				Name: "H-eq-2", Hard: true,
				Violated: func(a map[string]int) (bool, bool) {
					v, ok := a["X"]
					if !ok {
						return false, false
					}
					return v != 2, true
				},
			},
		},
	}
	res := Solve(p)
	if res.Assignment != nil {
		t.Errorf("expected infeasible, got %v", res.Assignment)
	}
}

// When every soft constraint can be satisfied, the optimum cost is zero.
func TestAllSoftSatisfiable(t *testing.T) {
	p := &Problem{
		Variables: []Variable{
			{Name: "X", Domain: []int{1, 2}},
			{Name: "Y", Domain: []int{1, 2}},
		},
		Constraints: []Constraint{
			{
				Name: "S-X-is-1", Weight: 5,
				Violated: func(a map[string]int) (bool, bool) {
					v, ok := a["X"]
					if !ok {
						return false, false
					}
					return v != 1, true
				},
			},
			{
				Name: "S-Y-is-2", Weight: 3,
				Violated: func(a map[string]int) (bool, bool) {
					v, ok := a["Y"]
					if !ok {
						return false, false
					}
					return v != 2, true
				},
			},
		},
	}
	res := Solve(p)
	if res.Cost != 0 || len(res.Violated) != 0 {
		t.Errorf("cost=%d violated=%v, want 0 and none", res.Cost, res.Violated)
	}
	if res.Assignment["X"] != 1 || res.Assignment["Y"] != 2 {
		t.Errorf("assignment = %v, want X=1 Y=2", res.Assignment)
	}
}

// The solver must prefer violating a cheap constraint over an expensive one.
func TestWeightTradeoff(t *testing.T) {
	p := &Problem{
		Variables: []Variable{{Name: "X", Domain: []int{1, 2}}},
		Constraints: []Constraint{
			{
				Name: "S-expensive", Weight: 100,
				Violated: func(a map[string]int) (bool, bool) {
					v, ok := a["X"]
					if !ok {
						return false, false
					}
					return v != 1, true
				},
			},
			{
				Name: "S-cheap", Weight: 1,
				Violated: func(a map[string]int) (bool, bool) {
					v, ok := a["X"]
					if !ok {
						return false, false
					}
					return v != 2, true
				},
			},
		},
	}
	res := Solve(p)
	if res.Cost != 1 || res.Assignment["X"] != 1 {
		t.Errorf("cost=%d assign=%v, want cost 1 with X=1", res.Cost, res.Assignment)
	}
	if !reflect.DeepEqual(res.Violated, []string{"S-cheap"}) {
		t.Errorf("violated = %v, want [S-cheap]", res.Violated)
	}
}
