package csp

import (
	"math/rand"
	"testing"
)

// bruteForce enumerates every tuple in the initial domains and returns
// all assignments satisfying every constraint.
func bruteForce(n, lo, hi int, cons []Constraint) [][]int {
	var sols [][]int
	assign := make([]int, n)
	var rec func(i int)
	rec = func(i int) {
		if i == n {
			ok := true
			for _, c := range cons {
				if !c.Check(assign) {
					ok = false
					break
				}
			}
			if ok {
				sol := make([]int, n)
				copy(sol, assign)
				sols = append(sols, sol)
			}
			return
		}
		for v := lo; v <= hi; v++ {
			assign[i] = v
			rec(i + 1)
		}
	}
	rec(0)
	return sols
}

// randomProblem builds a random small problem with the given seed.
func randomProblem(rng *rand.Rand) (n, lo, hi int, cons []Constraint) {
	n = 2 + rng.Intn(3) // 2..4 variables
	lo, hi = 0, 2+rng.Intn(2)
	numCons := 1 + rng.Intn(4)
	for k := 0; k < numCons; k++ {
		switch rng.Intn(4) {
		case 0:
			cons = append(cons, Equal{X: rng.Intn(n), Y: rng.Intn(n)})
		case 1:
			cons = append(cons, EqualConst{X: rng.Intn(n), K: lo + rng.Intn(hi-lo+1)})
		case 2:
			cons = append(cons, DiffLe{X: rng.Intn(n), Y: rng.Intn(n), K: rng.Intn(hi-lo+2) - 1})
		case 3:
			perm := rng.Perm(n)
			m := 2 + rng.Intn(n-1)
			cons = append(cons, AllDifferent{Set: perm[:m]})
		}
	}
	return n, lo, hi, cons
}

func newRandomSolver(rng *rand.Rand) (*Solver, []Constraint, [][]int) {
	n, lo, hi, cons := randomProblem(rng)
	s := NewSolver(n, lo, hi)
	for _, c := range cons {
		s.AddConstraint(c)
	}
	return s, cons, bruteForce(n, lo, hi, cons)
}

// TestPropagationConservative verifies, by exhaustive enumeration over
// small domains, that fixpoint propagation never removes a value that
// belongs to a real solution, and that a propagation failure means the
// problem is truly unsatisfiable.
func TestPropagationConservative(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 2000; iter++ {
		s, _, sols := newRandomSolver(rng)
		ok := s.Propagate()
		if !ok && len(sols) != 0 {
			t.Fatalf("iter %d: propagation reported failure but %d solutions exist: %v", iter, len(sols), sols[0])
		}
		if !ok {
			continue
		}
		for _, sol := range sols {
			for i, v := range sol {
				if _, kept := s.Domain(i)[v]; !kept {
					t.Fatalf("iter %d: propagation removed value %d of var %d used by solution %v", iter, v, i, sol)
				}
			}
		}
	}
}

// TestSolverMatchesBruteForce checks that complete backtracking search
// returns exactly the brute-force solution set, and that Solve agrees
// with the existence of solutions.
func TestSolverMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for iter := 0; iter < 2000; iter++ {
		s, _, want := newRandomSolver(rng)
		got := s.Solutions()
		if len(got) != len(want) {
			t.Fatalf("iter %d: solver found %d solutions, brute force found %d", iter, len(got), len(want))
		}
		for i := range want {
			for j := range want[i] {
				if got[i][j] != want[i][j] {
					t.Fatalf("iter %d: solution %d differs: got %v, want %v", iter, i, got[i], want[i])
				}
			}
		}
		_, ok := s.Solve()
		if ok != (len(want) > 0) {
			t.Fatalf("iter %d: Solve() = %v, brute force has %d solutions", iter, ok, len(want))
		}
	}
}

// TestRollbackRestoresDomains verifies that Restore brings every
// domain back to its snapshotted state after arbitrary updates and
// propagation, and that a snapshot can be reused for multiple
// restores.
func TestRollbackRestoresDomains(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for iter := 0; iter < 2000; iter++ {
		n, lo, hi, cons := randomProblem(rng)
		s := NewSolver(n, lo, hi)
		for _, c := range cons {
			s.AddConstraint(c)
		}
		s.Propagate()
		snap := s.Snapshot()

		for round := 0; round < 2; round++ {
			// Apply random updates, then propagate to a fixpoint.
			for k := 0; k < 1+rng.Intn(3); k++ {
				v := lo + rng.Intn(hi-lo+1)
				s.Assign(rng.Intn(n), v)
			}
			s.Restore(snap)
			for i := 0; i < n; i++ {
				if !s.Domain(i).Equal(snap[i]) {
					t.Fatalf("iter %d round %d: var %d domain = %v after restore, snapshot has %v",
						iter, round, i, s.Domain(i).Values(), snap[i].Values())
				}
			}
		}
	}
}

// TestPropagationIncomplete documents that reaching a fixpoint without
// failure does not imply satisfiability: three variables over {0,1}
// cannot be all-different, yet singleton pruning finds nothing to remove.
// Only complete backtracking confirms unsatisfiability.
func TestPropagationIncomplete(t *testing.T) {
	s := NewSolver(3, 0, 1)
	s.AddConstraint(AllDifferent{Set: []int{0, 1, 2}})
	if !s.Propagate() {
		t.Fatal("propagation should not detect this unsatisfiable problem")
	}
	if _, ok := s.Solve(); ok {
		t.Fatal("backtracking must confirm unsatisfiability")
	}
	if got := s.Solutions(); len(got) != 0 {
		t.Fatalf("expected no solutions, got %v", got)
	}
}

// TestPropagationSoundWhenEmpty checks the useful direction: once
// propagation empties a domain, no solution exists.
func TestPropagationSoundWhenEmpty(t *testing.T) {
	s := NewSolver(2, 0, 1)
	s.AddConstraint(EqualConst{X: 0, K: 0})
	s.AddConstraint(EqualConst{X: 1, K: 1})
	s.AddConstraint(Equal{X: 0, Y: 1})
	if s.Propagate() {
		t.Fatal("propagation should empty a domain for contradictory equalities")
	}
	if _, ok := s.Solve(); ok {
		t.Fatal("no solution can exist after sound propagation fails")
	}
}
