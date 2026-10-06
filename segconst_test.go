package segconst

import (
	"math/big"
	"math/rand"
	"reflect"
	"testing"
)

func checkSame(t *testing.T, label string, got, want Result) {
	t.Helper()
	if got.Feasible != want.Feasible {
		t.Fatalf("%s: feasible %v vs %v", label, got.Feasible, want.Feasible)
	}
	if !got.Feasible {
		return
	}
	if got.Loss.Cmp(want.Loss) != 0 {
		t.Fatalf("%s: loss %s vs %s", label, got.Loss, want.Loss)
	}
	if got.SSE.Cmp(want.SSE) != 0 {
		t.Fatalf("%s: sse %s vs %s", label, got.SSE, want.SSE)
	}
	if !reflect.DeepEqual(got.Cuts, want.Cuts) {
		t.Fatalf("%s: cuts %v vs %v", label, got.Cuts, want.Cuts)
	}
	if len(got.Means) != len(want.Means) {
		t.Fatalf("%s: means %v vs %v", label, got.Means, want.Means)
	}
	for k := range got.Means {
		if got.Means[k].Cmp(want.Means[k]) != 0 {
			t.Fatalf("%s: mean[%d] %s vs %s", label, k, got.Means[k], want.Means[k])
		}
	}
}

// Cross-check the DP against exhaustive cut enumeration on many short
// random sequences and a spread of rational penalties.
func TestSolveMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	lambdas := []*big.Rat{
		big.NewRat(0, 1),
		big.NewRat(1, 3),
		big.NewRat(1, 2),
		big.NewRat(1, 1),
		big.NewRat(7, 4),
		big.NewRat(3, 1),
		big.NewRat(10, 1),
	}
	for trial := 0; trial < 300; trial++ {
		n := rng.Intn(10) // 0..9
		xs := make([]int64, n)
		for i := range xs {
			xs[i] = int64(rng.Intn(7) - 3)
		}
		m := 1 + rng.Intn(3) // 1..3
		c := NewCosts(xs)
		for _, lambda := range lambdas {
			got := c.Solve(lambda, m)
			want := c.BruteForceSolve(lambda, m)
			checkSame(t, "dp vs brute", got, want)
		}
	}
}

// SolveAll must reuse interval costs and match individual Solve calls.
func TestSolveAllMatchesSolve(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	xs := make([]int64, 12)
	for i := range xs {
		xs[i] = int64(rng.Intn(5))
	}
	c := NewCosts(xs)
	lambdas := []*big.Rat{big.NewRat(0, 1), big.NewRat(2, 5), big.NewRat(5, 1)}
	all := c.SolveAll(lambdas, 2)
	for i, lambda := range lambdas {
		checkSame(t, "SolveAll vs Solve", all[i], c.Solve(lambda, 2))
	}
}

func TestExactIntervalCost(t *testing.T) {
	c := NewCosts([]int64{1, 2, 4})
	// mean 7/3, SSE = 21 - 49/3 = 14/3
	if got := c.SSE(0, 3); got.Cmp(big.NewRat(14, 3)) != 0 {
		t.Fatalf("SSE = %s, want 14/3", got)
	}
	if got := c.Mean(0, 3); got.Cmp(big.NewRat(7, 3)) != 0 {
		t.Fatalf("Mean = %s, want 7/3", got)
	}
}

// Equal loss with different segment counts: fewer segments must win.
func TestTieBreakFewerSegments(t *testing.T) {
	c := NewCosts([]int64{0, 2})
	// lambda = 2: one segment gives SSE 2 + 2 = 4; two segments give 0 + 4 = 4.
	got := c.Solve(big.NewRat(2, 1), 1)
	if !got.Feasible || got.NumSegments() != 1 {
		t.Fatalf("got %+v, want single segment", got)
	}
	if got.Loss.Cmp(big.NewRat(4, 1)) != 0 {
		t.Fatalf("loss = %s, want 4", got.Loss)
	}
	if got.Means[0].Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("mean = %s, want 1", got.Means[0])
	}
}

// Equal loss and equal segment count: lexicographically smallest cuts win.
func TestTieBreakLexicographicCuts(t *testing.T) {
	c := NewCosts([]int64{0, 0, 1, 1, 2, 2})
	// lambda = 1: cuts {2,6} and {4,6} both give SSE 1, loss 3; the
	// lexicographically smaller boundary list {2,6} must win.
	got := c.Solve(big.NewRat(1, 1), 1)
	want := []int{2, 6}
	if !reflect.DeepEqual(got.Cuts, want) {
		t.Fatalf("cuts = %v, want %v", got.Cuts, want)
	}
	if got.SSE.Cmp(big.NewRat(1, 1)) != 0 || got.Loss.Cmp(big.NewRat(3, 1)) != 0 {
		t.Fatalf("sse = %s, loss = %s, want 1 and 3", got.SSE, got.Loss)
	}
}

func TestZeroPenaltySplitsEverywhere(t *testing.T) {
	c := NewCosts([]int64{1, 2, 3})
	got := c.Solve(big.NewRat(0, 1), 1)
	if got.NumSegments() != 3 || got.SSE.Sign() != 0 {
		t.Fatalf("got %+v, want 3 zero-error segments", got)
	}
}

func TestZeroPenaltyMergesConstantRuns(t *testing.T) {
	c := NewCosts([]int64{5, 5, 5})
	// lambda = 0: one segment already has SSE 0, so the fewer-segments
	// tie-break keeps it to a single segment.
	got := c.Solve(big.NewRat(0, 1), 1)
	if got.NumSegments() != 1 || got.SSE.Sign() != 0 {
		t.Fatalf("got %+v, want 1 zero-error segment", got)
	}
}

func TestInfeasible(t *testing.T) {
	c := NewCosts([]int64{1, 2})
	if got := c.Solve(big.NewRat(1, 1), 3); got.Feasible {
		t.Fatalf("expected infeasible, got %+v", got)
	}
	if got := c.BruteForceSolve(big.NewRat(1, 1), 3); got.Feasible {
		t.Fatalf("expected infeasible, got %+v", got)
	}
}

func TestEmptySequence(t *testing.T) {
	c := NewCosts(nil)
	got := c.Solve(big.NewRat(1, 1), 1)
	if !got.Feasible || got.NumSegments() != 0 || got.Loss.Sign() != 0 {
		t.Fatalf("got %+v, want empty zero-loss result", got)
	}
}

func TestLargeValuesStayExact(t *testing.T) {
	xs := []int64{1 << 40, -(1 << 40), 1 << 40, 0}
	c := NewCosts(xs)
	got := c.Solve(big.NewRat(1, 1), 1)
	want := c.BruteForceSolve(big.NewRat(1, 1), 1)
	checkSame(t, "large values", got, want)
	if got.SSE.Cmp(want.SSE) != 0 {
		t.Fatalf("sse mismatch: %s vs %s", got.SSE, want.SSE)
	}
}
