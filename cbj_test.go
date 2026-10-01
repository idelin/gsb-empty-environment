package csp

import "testing"

// Acceptance: on the 10-variable benchmark the dead end at step 8 is
// caused solely by the assignment at step 2, so the solver must jump
// straight from step 8 back to step 2, skipping steps 3..7.
func TestBackjumpSkipsSteps3To7(t *testing.T) {
	p := NewBenchmarkProblem()
	s := NewCBJSolver(p)

	sol, ok := s.Solve()
	if !ok {
		t.Fatal("expected a solution")
	}
	if !p.Validate(sol) {
		t.Fatalf("invalid solution %v", sol)
	}

	if len(s.Stats.Backjumps) != 1 {
		t.Fatalf("expected exactly 1 non-chronological backjump, got %v", s.Stats.Backjumps)
	}
	bj := s.Stats.Backjumps[0]
	if bj.From != 8 || bj.To != 2 {
		t.Fatalf("expected backjump step 8 -> step 2, got %s", bj)
	}
	t.Logf("backjump: %s", bj)
	t.Logf("solution: %v", sol)
}

// The conflict set of X8 must contain only X2 (step 2): X8's single
// value is ruled out exclusively by the X2 != X8 constraint.
func TestConflictSetContainsOnlyStep2(t *testing.T) {
	p := NewBenchmarkProblem()
	s := NewCBJSolver(p)

	// Simulate the search state after steps 1..7 took their first values.
	s.assignment[0] = 1 // X1
	s.assignment[1] = 1 // X2 <- culprit
	s.assignment[2] = 1 // X3
	s.assignment[3] = 2 // X4
	s.assignment[4] = 1 // X5
	s.assignment[5] = 2 // X6
	s.assignment[6] = 1 // X7

	conf := s.conflicts(7, p.Domains[7][0]) // X8 = 1
	if len(conf) != 1 {
		t.Fatalf("expected conflict set of size 1, got %v", conf)
	}
	if _, ok := conf[1]; !ok {
		t.Fatalf("expected conflict set {X2}, got %v", conf)
	}
}

// CBJ must prune the useless re-exploration of steps 3..7 that
// chronological backtracking performs.
func TestCBJPrunesSearch(t *testing.T) {
	cbj := NewCBJSolver(NewBenchmarkProblem())
	if _, ok := cbj.Solve(); !ok {
		t.Fatal("CBJ: expected a solution")
	}
	naive := NewNaiveSolver(NewBenchmarkProblem())
	if _, ok := naive.Solve(); !ok {
		t.Fatal("naive: expected a solution")
	}

	t.Logf("value attempts: CBJ=%d, chronological=%d", cbj.Stats.Assignments, naive.Assignments)
	if naive.Assignments <= cbj.Stats.Assignments*5 {
		t.Fatalf("expected CBJ to prune massively: CBJ=%d, chronological=%d",
			cbj.Stats.Assignments, naive.Assignments)
	}
}

// An unsatisfiable instance must be reported as such.
func TestUnsatisfiable(t *testing.T) {
	p := NewBenchmarkProblem()
	p.Domains[1] = []int{1} // X2 forced to 1, but X8 can only be 1 and X2 != X8
	s := NewCBJSolver(p)
	if sol, ok := s.Solve(); ok {
		t.Fatalf("expected unsatisfiable, got %v", sol)
	}
}

// Validate must reject assignments that violate a constraint.
func TestValidate(t *testing.T) {
	p := NewBenchmarkProblem()
	good := []int{1, 2, 1, 2, 1, 2, 1, 1, 1, 1}
	if !p.Validate(good) {
		t.Fatalf("expected %v to be valid", good)
	}
	bad := []int{1, 1, 1, 2, 1, 2, 1, 1, 1, 1} // X2 = X8 = 1
	if p.Validate(bad) {
		t.Fatalf("expected %v to be invalid", bad)
	}
}
