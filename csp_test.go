package csp

import (
	"reflect"
	"strings"
	"testing"
)

func TestConflictDirectedBackjumpingSkipsIrrelevantVariables(t *testing.T) {
	result, err := Solve(NewTenVariableDemoProblem())
	if err != nil {
		t.Fatalf("Solve returned an error: %v", err)
	}
	if !result.Solved {
		t.Fatal("expected the demo CSP to be solvable")
	}

	expectedAssignment := []int{0, 1, 0, 0, 0, 0, 0, 0, 0, 0}
	if !reflect.DeepEqual(result.Assignment, expectedAssignment) {
		t.Fatalf("assignment = %v, want %v", result.Assignment, expectedAssignment)
	}

	if len(result.Stats.Backjumps) != 1 {
		t.Fatalf("backjump count = %d, want 1", len(result.Stats.Backjumps))
	}
	jump := result.Stats.Backjumps[0]
	if jump.From != 7 || jump.To != 1 {
		t.Fatalf("jump = step %d -> step %d, want step 8 -> step 2", jump.From+1, jump.To+1)
	}
	if !reflect.DeepEqual(jump.ConflictSet, []int{1}) {
		t.Fatalf("conflict set = %v, want [1] (the second variable)", jump.ConflictSet)
	}
	if !reflect.DeepEqual(jump.Skipped, []int{2, 3, 4, 5, 6}) {
		t.Fatalf("skipped variables = %v, want [2 3 4 5 6]", jump.Skipped)
	}

	// A chronological search would try x8 for all 25^5 combinations of
	// x3..x7. CBJ performs only the two failing checks at x8 plus one
	// successful check after x2 is repaired.
	if result.Stats.ConsistencyChecks != 3 {
		t.Fatalf("consistency checks = %d, want 3", result.Stats.ConsistencyChecks)
	}
	if result.Stats.RejectedValues != 2 {
		t.Fatalf("rejected values = %d, want 2", result.Stats.RejectedValues)
	}
	if result.Stats.Assignments != 16 {
		t.Fatalf("assignments = %d, want 16", result.Stats.Assignments)
	}
}

func TestBackjumpMergesConflictSetsWhileUnwinding(t *testing.T) {
	problem := Problem{
		Variables: []Variable{
			{Name: "a", Domain: []int{0}},
			{Name: "b", Domain: []int{0}},
			{Name: "c", Domain: []int{0}},
		},
		Constraints: []BinaryConstraint{
			{Left: 0, Right: 2, Allow: func(a, c int) bool { return false }},
			{Left: 1, Right: 2, Allow: func(b, c int) bool { return false }},
		},
	}

	result, err := Solve(problem)
	if err != nil {
		t.Fatalf("Solve returned an error: %v", err)
	}
	if result.Solved {
		t.Fatalf("expected unsatisfiable problem, got %v", result.Assignment)
	}
	if len(result.Stats.Backjumps) != 2 {
		t.Fatalf("backjump count = %d, want 2", len(result.Stats.Backjumps))
	}

	first := result.Stats.Backjumps[0]
	if first.From != 2 || first.To != 1 || !reflect.DeepEqual(first.ConflictSet, []int{0, 1}) {
		t.Fatalf("first backjump = %+v, want c -> b with conflict set [a b]", first)
	}
	second := result.Stats.Backjumps[1]
	if second.From != 1 || second.To != 0 || !reflect.DeepEqual(second.ConflictSet, []int{0}) {
		t.Fatalf("second backjump = %+v, want b -> a with inherited conflict set [a]", second)
	}
}

func TestSolveValidatesConstraints(t *testing.T) {
	problem := Problem{
		Variables: []Variable{{Name: "x", Domain: []int{1}}},
		Constraints: []BinaryConstraint{
			{Left: 0, Right: 1, Allow: func(left, right int) bool { return true }},
		},
	}

	_, err := Solve(problem)
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("Solve error = %v, want an out-of-range constraint error", err)
	}
}
