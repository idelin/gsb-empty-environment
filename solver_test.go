package binsolve

import (
	"math/rand"
	"reflect"
	"testing"
)

const unlimited = -1

func mustSolve(t *testing.T, p Problem, budget int) Solution {
	t.Helper()
	sol := Solve(p, budget)
	if !sol.Feasible {
		t.Fatalf("expected feasible solution, got reason %q", sol.Reason)
	}
	if err := sol.Verify(p); err != nil {
		t.Fatalf("solution failed verification: %v", err)
	}
	return sol
}

func TestZeroSizeItemsRespectConflicts(t *testing.T) {
	p := Problem{
		Sizes:     []int{0, 0, 0},
		Capacity:  5,
		Conflicts: [][2]int{{0, 1}, {1, 2}},
	}
	sol := mustSolve(t, p, unlimited)
	if !sol.Optimal {
		t.Fatal("expected optimal solution")
	}
	if len(sol.Bins) != 2 {
		t.Fatalf("zero-size conflicting items must use 2 bins, got %v", sol.Bins)
	}
	want := [][]int{{0, 2}, {1}}
	if !reflect.DeepEqual(sol.Bins, want) {
		t.Fatalf("got %v, want %v", sol.Bins, want)
	}
	if sol.LowerBound != 2 {
		t.Fatalf("clique lower bound should be 2, got %d", sol.LowerBound)
	}
}

func TestZeroCapacityWithZeroSizes(t *testing.T) {
	sol := mustSolve(t, Problem{Sizes: []int{0, 0}, Capacity: 0}, unlimited)
	if len(sol.Bins) != 1 {
		t.Fatalf("zero-size items without conflicts fit one bin, got %v", sol.Bins)
	}
	sol = mustSolve(t, Problem{Sizes: []int{0, 0}, Capacity: 0, Conflicts: [][2]int{{0, 1}}}, unlimited)
	if len(sol.Bins) != 2 {
		t.Fatalf("conflicting zero-size items need 2 bins even at capacity 0, got %v", sol.Bins)
	}
}

func TestOversizeItemInfeasible(t *testing.T) {
	sol := Solve(Problem{Sizes: []int{11}, Capacity: 10}, unlimited)
	if sol.Feasible {
		t.Fatalf("single item above capacity must be infeasible: %+v", sol)
	}
	if sol.Reason == "" {
		t.Fatal("infeasible solution must carry a reason")
	}
	if err := sol.Verify(Problem{Sizes: []int{11}, Capacity: 10}); err != nil {
		t.Fatalf("infeasible verdict should verify against invalid problem: %v", err)
	}
}

func TestBasicPacking(t *testing.T) {
	p := Problem{Sizes: []int{6, 5, 4, 3, 2}, Capacity: 10}
	sol := mustSolve(t, p, unlimited)
	want := [][]int{{0, 2}, {1, 3, 4}}
	if !reflect.DeepEqual(sol.Bins, want) {
		t.Fatalf("got %v, want %v", sol.Bins, want)
	}
	if sol.LowerBound != 2 {
		t.Fatalf("lower bound = %d, want 2", sol.LowerBound)
	}
	total := 0
	for _, ev := range sol.Evidence {
		total += ev.Load
		if ev.Load > p.Capacity {
			t.Fatalf("bin load %d exceeds capacity", ev.Load)
		}
	}
	if total != 20 {
		t.Fatalf("evidence loads sum to %d, want 20", total)
	}
}

func TestLexicographicTieBreak(t *testing.T) {
	p := Problem{Sizes: []int{5, 5, 5, 5}, Capacity: 10}
	sol := mustSolve(t, p, unlimited)
	want := [][]int{{0, 1}, {2, 3}}
	if !reflect.DeepEqual(sol.Bins, want) {
		t.Fatalf("lexicographically smallest grouping = %v, got %v", want, sol.Bins)
	}
}

func TestConflictForcesExtraBin(t *testing.T) {
	p := Problem{Sizes: []int{4, 4, 4}, Capacity: 8, Conflicts: [][2]int{{0, 1}}}
	sol := mustSolve(t, p, unlimited)
	want := [][]int{{0}, {1, 2}}
	if !reflect.DeepEqual(sol.Bins, want) {
		t.Fatalf("got %v, want %v", sol.Bins, want)
	}
	if sol.LowerBound != 2 {
		t.Fatalf("lower bound = %d, want 2", sol.LowerBound)
	}
}

// The greedy incumbent uses 3 bins here; the optimum is 2. A budget of one
// node must therefore yield a feasible but non-optimal flagged incumbent, and
// the full search must not stop at that first solution.
func TestFirstIncumbentIsNotReportedOptimal(t *testing.T) {
	p := Problem{Sizes: []int{3, 3, 2, 2, 4}, Capacity: 7}

	limited := mustSolve(t, p, 1)
	if limited.Optimal {
		t.Fatal("budget of one node must not be reported optimal")
	}
	if !limited.BudgetExhausted {
		t.Fatal("expected BudgetExhausted")
	}
	if len(limited.Bins) != 3 {
		t.Fatalf("incumbent after one node should be the 3-bin greedy seed, got %v", limited.Bins)
	}

	full := mustSolve(t, p, unlimited)
	if !full.Optimal {
		t.Fatal("unlimited search must be optimal")
	}
	want := [][]int{{0, 2, 3}, {1, 4}}
	if !reflect.DeepEqual(full.Bins, want) {
		t.Fatalf("got %v, want %v", full.Bins, want)
	}
	if len(full.Bins) >= len(limited.Bins) {
		t.Fatal("full search must improve on the first incumbent")
	}
}

func TestEmptyProblem(t *testing.T) {
	sol := mustSolve(t, Problem{Capacity: 10}, unlimited)
	if len(sol.Bins) != 0 || sol.LowerBound != 0 || !sol.Optimal {
		t.Fatalf("empty problem: %+v", sol)
	}
}

func TestVerifyRejectsInvalidLoadings(t *testing.T) {
	p := Problem{Sizes: []int{4, 4}, Capacity: 8, Conflicts: [][2]int{{0, 1}}}
	bad := Solution{Feasible: true, Optimal: true, Bins: [][]int{{0, 1}}, LowerBound: 2}
	if err := bad.Verify(p); err == nil {
		t.Fatal("Verify must reject conflicting items sharing a bin")
	}
	over := Solution{Feasible: true, Bins: [][]int{{0}, {1}}, LowerBound: 3}
	if err := over.Verify(p); err == nil {
		t.Fatal("Verify must reject bin count below the lower bound")
	}
	unnormalized := Solution{Feasible: true, Bins: [][]int{{1}, {0}}, LowerBound: 2}
	if err := unnormalized.Verify(p); err == nil {
		t.Fatal("Verify must reject non-canonical bin order")
	}
}

func randomProblem(r *rand.Rand, n, maxSize, cap int, conflictProb float64) Problem {
	p := Problem{Capacity: cap}
	for i := 0; i < n; i++ {
		p.Sizes = append(p.Sizes, r.Intn(maxSize+1))
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if r.Float64() < conflictProb {
				p.Conflicts = append(p.Conflicts, [2]int{i, j})
			}
		}
	}
	return p
}

func TestLowerBoundAlwaysValid(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		p := randomProblem(r, r.Intn(9), 6, 10, 0.3)
		sol := mustSolve(t, p, unlimited)
		if !sol.Optimal {
			t.Fatalf("trial %d: unlimited budget must prove optimality", trial)
		}
		if len(sol.Bins) < sol.LowerBound {
			t.Fatalf("trial %d: bins %d below lower bound %d", trial, len(sol.Bins), sol.LowerBound)
		}
		inst, _ := newInstance(p)
		sum := 0
		for _, sz := range p.Sizes {
			sum += sz
		}
		want := ceilDiv(sum, p.Capacity)
		if c := maxClique(inst.conflict); c > want {
			want = c
		}
		if sol.LowerBound != want {
			t.Fatalf("trial %d: lower bound %d, want max(ceil, clique)=%d", trial, sol.LowerBound, want)
		}
	}
}

// The exhaustive (natural order) and branch-and-bound (size-descending order)
// paths explore different trees but must agree on the optimum and on the
// lexicographically smallest canonical grouping.
func TestSearchOrdersAgree(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 200; trial++ {
		p := randomProblem(r, 2+r.Intn(9), 6, 10, 0.4)
		inst, err := newInstance(p)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		ex := solveOrdered(inst, naturalOrder(inst.n), unlimited)
		bb := solveOrdered(inst, sizeDescendingOrder(inst), unlimited)
		if !reflect.DeepEqual(ex.Bins, bb.Bins) {
			t.Fatalf("trial %d: exhaustive %v vs bnb %v", trial, ex.Bins, bb.Bins)
		}
	}
}

// A tight budget must never fabricate optimality, and every returned
// incumbent must still be a valid loading.
func TestBudgetedIncumbentsStayValid(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	for trial := 0; trial < 200; trial++ {
		p := randomProblem(r, 4+r.Intn(7), 6, 10, 0.4)
		budget := r.Intn(6)
		sol := mustSolve(t, p, budget)
		if sol.Optimal && sol.BudgetExhausted {
			t.Fatalf("trial %d: optimal claimed with exhausted budget", trial)
		}
		if sol.NodesExpanded > budget+1 {
			t.Fatalf("trial %d: expanded %d nodes with budget %d", trial, sol.NodesExpanded, budget)
		}
		full := Solve(p, unlimited)
		if len(sol.Bins) < len(full.Bins) {
			t.Fatalf("trial %d: incumbent %d bins beats proven optimum %d", trial, len(sol.Bins), len(full.Bins))
		}
	}
}
