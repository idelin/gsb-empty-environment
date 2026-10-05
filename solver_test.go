package segsolver

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// table builds an (n+1)x(n+1) cost table from f(i,j); entries with i>=j are 0.
func table(n int, f func(i, j int) int64) [][]int64 {
	c := make([][]int64, n+1)
	for i := range c {
		c[i] = make([]int64, n+1)
		for j := i + 1; j <= n; j++ {
			c[i][j] = f(i, j)
		}
	}
	return c
}

func mustSolve(t *testing.T, n, minLen, maxLen int, cost [][]int64, must, forbid []int) *Solver {
	t.Helper()
	s, err := NewSolver(n, minLen, maxLen, cost, must, forbid)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	return s
}

func TestBasicKnownOptimum(t *testing.T) {
	// n=4, lengths 1..2. cost(i,j)=j-i except cost[0][2]=1 (cheap) and
	// cost[2][4]=1 (cheap): optimum is [0,2)+[2,4) = 2 with cuts [2].
	n := 4
	cost := table(n, func(i, j int) int64 { return int64(j - i) })
	cost[0][2] = 1
	cost[2][4] = 1
	s := mustSolve(t, n, 1, 2, cost, nil, nil)
	res := s.Solve()
	if !res.Feasible || res.Cost != 2 || !reflect.DeepEqual(res.Cuts, []int{2}) {
		t.Fatalf("got %+v, want cost=2 cuts=[2]", res)
	}
	if got, err := s.Evaluate(res.Cuts); err != nil || got != res.Cost {
		t.Fatalf("Evaluate(%v) = %d, %v; want %d", res.Cuts, got, err, res.Cost)
	}
}

func TestLexicographicTieBreakEmptyBeatsNonempty(t *testing.T) {
	// All costs zero, no constraints: every cut list costs 0, and the empty
	// list is a prefix of all others, hence lexicographically smallest.
	n := 5
	s := mustSolve(t, n, 1, n, table(n, func(i, j int) int64 { return 0 }), nil, nil)
	res := s.Solve()
	if !res.Feasible || res.Cost != 0 || len(res.Cuts) != 0 {
		t.Fatalf("got %+v, want cost=0 cuts=[]", res)
	}
}

func TestLexicographicTieBreakPrefixTrap(t *testing.T) {
	// n=6, all costs zero, mandatory cut at 3. Min-cost cut lists include
	// [3], [1,3], [1,2,3], ... The lexicographically smallest is [1,2,3]:
	// [1,2,3] < [1,3] < [3]. This is the case where a forward DP that
	// keeps only the "best" list per position fails, because [2] < [2,5]
	// yet [2,5,...] can extend to the smaller full list.
	n := 6
	s := mustSolve(t, n, 1, n, table(n, func(i, j int) int64 { return 0 }), []int{3}, nil)
	res := s.Solve()
	want := []int{1, 2, 3}
	if !res.Feasible || res.Cost != 0 || !reflect.DeepEqual(res.Cuts, want) {
		t.Fatalf("got %+v, want cost=0 cuts=%v", res, want)
	}
}

func TestMustAndForbid(t *testing.T) {
	// n=6, lengths 1..3, unit costs per segment: fewer segments is cheaper.
	// Must cut at 3, forbid cut at 4. Cheapest legal: [0,3)+[3,6) cost 2.
	n := 6
	cost := table(n, func(i, j int) int64 { return 1 })
	s := mustSolve(t, n, 1, 3, cost, []int{3}, []int{4})
	res := s.Solve()
	if !res.Feasible || res.Cost != 2 || !reflect.DeepEqual(res.Cuts, []int{3}) {
		t.Fatalf("got %+v, want cost=2 cuts=[3]", res)
	}
}

func TestConflictPositionsRejected(t *testing.T) {
	n := 5
	_, err := NewSolver(n, 1, n, table(n, func(i, j int) int64 { return 0 }), []int{2, 4}, []int{4, 2})
	if err == nil || !strings.Contains(err.Error(), "[2 4]") {
		t.Fatalf("want conflict error listing [2 4], got %v", err)
	}
}

func TestNegativeSegmentCosts(t *testing.T) {
	// Negative costs reward splitting. n=4, lengths 1..2.
	cost := table(4, func(i, j int) int64 { return 5 })
	cost[0][1], cost[1][2], cost[2][3], cost[3][4] = -3, -3, -3, -3
	s := mustSolve(t, 4, 1, 2, cost, nil, nil)
	res := s.Solve()
	// All singles: cost -12, cuts [1 2 3].
	if !res.Feasible || res.Cost != -12 || !reflect.DeepEqual(res.Cuts, []int{1, 2, 3}) {
		t.Fatalf("got %+v, want cost=-12 cuts=[1 2 3]", res)
	}
	if err := s.VerifyBruteForce(); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetKeepsLegalUpperBound(t *testing.T) {
	n := 4
	cost := table(n, func(i, j int) int64 { return int64(j - i) })
	cost[0][2], cost[2][4] = 1, 1
	s := mustSolve(t, n, 1, 2, cost, nil, nil)

	ok := s.SolveWithBudget(5)
	if !ok.Feasible || !ok.WithinBudget || ok.Cost != 2 {
		t.Fatalf("budget=5: got %+v", ok)
	}
	over := s.SolveWithBudget(1)
	if !over.Feasible || over.WithinBudget {
		t.Fatalf("budget=1: got %+v, want feasible but over budget", over)
	}
	if over.Cost != 2 || !reflect.DeepEqual(over.Cuts, []int{2}) {
		t.Fatalf("budget=1: upper-bound evidence lost: %+v", over)
	}
	if got, err := s.Evaluate(over.Cuts); err != nil || got != over.Cost {
		t.Fatalf("upper bound not legal: Evaluate=%d, %v", got, err)
	}
}

func TestReviseLocalCost(t *testing.T) {
	n := 4
	cost := table(n, func(i, j int) int64 { return int64(j - i) })
	cost[0][2], cost[2][4] = 1, 1
	s := mustSolve(t, n, 1, 2, cost, nil, nil)
	if res := s.Solve(); res.Cost != 2 {
		t.Fatalf("before revise: %+v", res)
	}
	// Make the two cheap segments expensive, then re-verify against brute
	// force instead of hand-computing the new optimum.
	if err := s.Revise(0, 2, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.Revise(2, 4, 10); err != nil {
		t.Fatal(err)
	}
	res := s.Solve()
	if err := s.VerifyBruteForce(); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Evaluate(res.Cuts); err != nil || got != res.Cost {
		t.Fatalf("evidence mismatch: %+v eval=%d err=%v", res, got, err)
	}
	if err := s.Revise(0, 3, 0); err == nil {
		t.Fatal("Revise beyond maxLen should fail")
	}
}

func TestInfeasible(t *testing.T) {
	// n=5 cannot be tiled by length-2 segments.
	s := mustSolve(t, 5, 2, 2, table(5, func(i, j int) int64 { return 1 }), nil, nil)
	if res := s.Solve(); res.Feasible {
		t.Fatalf("want infeasible, got %+v", res)
	}
	// Mandatory cut at 1 forces a length-1 segment, violating minLen=2.
	s2 := mustSolve(t, 4, 2, 2, table(4, func(i, j int) int64 { return 1 }), []int{1}, nil)
	res := s2.SolveWithBudget(100)
	if res.Feasible || res.WithinBudget {
		t.Fatalf("want infeasible, got %+v", res)
	}
	if err := s2.VerifyBruteForce(); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluateRejectsBadEvidence(t *testing.T) {
	n := 4
	s := mustSolve(t, n, 1, 2, table(n, func(i, j int) int64 { return 1 }), []int{2}, []int{3})
	for _, cuts := range [][]int{{3}, {2, 3}, {1}, {2, 2}, {4}, {1, 3, 2}} {
		if _, err := s.Evaluate(cuts); err == nil {
			t.Fatalf("Evaluate(%v) should fail", cuts)
		}
	}
	if c, err := s.Evaluate([]int{2}); err != nil || c != 2 {
		t.Fatalf("Evaluate([2]) = %d, %v; want 2", c, err)
	}
}

func TestRandomizedAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	conflictsSeen, infeasibleSeen, negativeSeen := 0, 0, 0
	for trial := 0; trial < 3000; trial++ {
		n := 1 + rng.Intn(9)
		minLen := 1 + rng.Intn(n)
		maxLen := minLen + rng.Intn(n-minLen+1)
		cost := table(n, func(i, j int) int64 { return int64(rng.Intn(19) - 9) }) // [-9,9]
		var must, forbid []int
		for p := 1; p < n; p++ {
			switch rng.Intn(8) {
			case 0:
				must = append(must, p)
			case 1:
				forbid = append(forbid, p)
			case 2: // deliberate conflict
				must = append(must, p)
				forbid = append(forbid, p)
			}
		}
		s, err := NewSolver(n, minLen, maxLen, cost, must, forbid)
		if err != nil {
			conflictsSeen++
			continue
		}
		if err := s.VerifyBruteForce(); err != nil {
			t.Fatalf("trial %d n=%d len=[%d,%d] must=%v forbid=%v: %v",
				trial, n, minLen, maxLen, must, forbid, err)
		}
		res := s.Solve()
		if !res.Feasible {
			infeasibleSeen++
			continue
		}
		if res.Cost < 0 {
			negativeSeen++
		}
		// Evidence must re-evaluate to the reported optimum.
		if got, err := s.Evaluate(res.Cuts); err != nil || got != res.Cost {
			t.Fatalf("trial %d: evidence %v evaluates to %d,%v but Cost=%d",
				trial, res.Cuts, got, err, res.Cost)
		}
		// Budget flag consistency around the optimum.
		if got := s.SolveWithBudget(res.Cost); !got.WithinBudget {
			t.Fatalf("trial %d: budget=optimum should be within budget", trial)
		}
		if got := s.SolveWithBudget(res.Cost - 1); got.WithinBudget || !got.Feasible || got.Cost != res.Cost {
			t.Fatalf("trial %d: budget=optimum-1 should keep legal upper bound %+v", trial, got)
		}
		// Local revision, then re-verify everything.
		i := rng.Intn(n)
		j := i + minLen + rng.Intn(maxLen-minLen+1)
		if j <= n {
			if err := s.Revise(i, j, int64(rng.Intn(19)-9)); err != nil {
				t.Fatalf("trial %d: Revise: %v", trial, err)
			}
			if err := s.VerifyBruteForce(); err != nil {
				t.Fatalf("trial %d after revise: %v", trial, err)
			}
		}
	}
	if conflictsSeen == 0 || infeasibleSeen == 0 || negativeSeen == 0 {
		t.Fatalf("weak coverage: conflicts=%d infeasible=%d negative=%d", conflictsSeen, infeasibleSeen, negativeSeen)
	}
	t.Logf("covered: conflicts=%d infeasible=%d negative=%d", conflictsSeen, infeasibleSeen, negativeSeen)
}
