package rcsp

import (
	"math/rand"
	"reflect"
	"testing"
)

func mustGraph(t *testing.T, n int, edges []Edge) *Graph {
	t.Helper()
	g, err := NewGraph(n, edges)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	return g
}

// The cheap prefix 0->1 (cost 1) carries risk 3 and cannot be completed
// within budget 3; the expensive prefix (cost 4, risk 0) can. A solver
// that keeps only one distance per node would wrongly report no path.
func TestExpensivePrefixIsKept(t *testing.T) {
	g := mustGraph(t, 4, []Edge{
		{ID: 1, From: 0, To: 1, Cost: 1, Risk: 3},
		{ID: 2, From: 0, To: 1, Cost: 4, Risk: 0},
		{ID: 3, From: 1, To: 3, Cost: 1, Risk: 1},
	})
	res, err := Solve(g, 0, 3, 3)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Found {
		t.Fatal("expected a feasible path via the expensive low-risk prefix")
	}
	if res.TotalCost != 5 || res.TotalRisk != 1 {
		t.Fatalf("got cost=%d risk=%d, want cost=5 risk=1", res.TotalCost, res.TotalRisk)
	}
	if !reflect.DeepEqual(res.EdgeIDs, []int{2, 3}) {
		t.Fatalf("got path %v, want [2 3]", res.EdgeIDs)
	}
}

// Spend more early to save more later: the direct cheap first hop leads
// to an expensive suffix, so the optimum starts with the pricier hop.
func TestExpensiveFirstHopSavesLater(t *testing.T) {
	g := mustGraph(t, 4, []Edge{
		{ID: 1, From: 0, To: 1, Cost: 1, Risk: 0},
		{ID: 2, From: 1, To: 3, Cost: 10, Risk: 0},
		{ID: 3, From: 0, To: 2, Cost: 5, Risk: 0},
		{ID: 4, From: 2, To: 3, Cost: 1, Risk: 0},
	})
	res, err := Solve(g, 0, 3, 0)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Found || res.TotalCost != 6 {
		t.Fatalf("got found=%v cost=%d, want found=true cost=6", res.Found, res.TotalCost)
	}
	if !reflect.DeepEqual(res.EdgeIDs, []int{3, 4}) {
		t.Fatalf("got path %v, want [3 4]", res.EdgeIDs)
	}
}

// Equal cost: fewer hops wins.
func TestTieBreakFewerHops(t *testing.T) {
	g := mustGraph(t, 4, []Edge{
		{ID: 1, From: 0, To: 1, Cost: 2, Risk: 0},
		{ID: 2, From: 1, To: 3, Cost: 2, Risk: 0},
		{ID: 3, From: 0, To: 3, Cost: 4, Risk: 0},
	})
	res, err := Solve(g, 0, 3, 0)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Found || res.TotalCost != 4 || res.Hops != 1 {
		t.Fatalf("got found=%v cost=%d hops=%d, want true/4/1", res.Found, res.TotalCost, res.Hops)
	}
	if !reflect.DeepEqual(res.EdgeIDs, []int{3}) {
		t.Fatalf("got path %v, want [3]", res.EdgeIDs)
	}
}

// Equal cost and hops: lexicographically smaller edge-ID sequence wins.
func TestTieBreakEdgeIDOrder(t *testing.T) {
	g := mustGraph(t, 4, []Edge{
		{ID: 5, From: 0, To: 1, Cost: 1, Risk: 0},
		{ID: 6, From: 1, To: 3, Cost: 1, Risk: 0},
		{ID: 1, From: 0, To: 2, Cost: 1, Risk: 0},
		{ID: 2, From: 2, To: 3, Cost: 1, Risk: 0},
	})
	res, err := Solve(g, 0, 3, 0)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Found || !reflect.DeepEqual(res.EdgeIDs, []int{1, 2}) {
		t.Fatalf("got path %v, want [1 2]", res.EdgeIDs)
	}
}

// Zero-cost (and zero-risk) cycles must not cause non-termination or
// appear in the answer.
func TestZeroCostCycle(t *testing.T) {
	g := mustGraph(t, 4, []Edge{
		{ID: 1, From: 0, To: 1, Cost: 0, Risk: 0},
		{ID: 2, From: 1, To: 0, Cost: 0, Risk: 0},
		{ID: 3, From: 1, To: 3, Cost: 1, Risk: 0},
		{ID: 4, From: 0, To: 3, Cost: 5, Risk: 0},
	})
	res, err := Solve(g, 0, 3, 0)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Found || res.TotalCost != 1 {
		t.Fatalf("got found=%v cost=%d, want true/1", res.Found, res.TotalCost)
	}
	if !reflect.DeepEqual(res.EdgeIDs, []int{1, 3}) {
		t.Fatalf("got path %v, want [1 3]", res.EdgeIDs)
	}
}

// Risk exactly equal to the budget is allowed; one less budget is not.
func TestBudgetBoundary(t *testing.T) {
	edges := []Edge{
		{ID: 1, From: 0, To: 1, Cost: 2, Risk: 2},
		{ID: 2, From: 1, To: 2, Cost: 2, Risk: 2},
	}
	g := mustGraph(t, 3, edges)
	res, err := Solve(g, 0, 2, 4)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Found || res.TotalRisk != 4 {
		t.Fatalf("budget=4: got found=%v risk=%d, want true/4", res.Found, res.TotalRisk)
	}
	res, err = Solve(g, 0, 2, 3)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.Found {
		t.Fatalf("budget=3: expected infeasible, got cost=%d", res.TotalCost)
	}
}

func TestSourceEqualsTarget(t *testing.T) {
	g := mustGraph(t, 2, []Edge{{ID: 1, From: 0, To: 1, Cost: 1, Risk: 0}})
	res, err := Solve(g, 1, 1, 0)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Found || res.TotalCost != 0 || res.Hops != 0 || len(res.EdgeIDs) != 0 {
		t.Fatalf("got %+v, want empty zero-cost path", res)
	}
}

// The ledger must accumulate cost and risk edge by edge and agree with
// the totals.
func TestLedger(t *testing.T) {
	g := mustGraph(t, 4, []Edge{
		{ID: 1, From: 0, To: 1, Cost: 3, Risk: 2},
		{ID: 2, From: 1, To: 2, Cost: 0, Risk: 1},
		{ID: 3, From: 2, To: 3, Cost: 4, Risk: 0},
	})
	res, err := Solve(g, 0, 3, 5)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Found {
		t.Fatal("expected feasible path")
	}
	want := []LedgerEntry{
		{EdgeID: 1, From: 0, To: 1, Cost: 3, Risk: 2, CumCost: 3, CumRisk: 2},
		{EdgeID: 2, From: 1, To: 2, Cost: 0, Risk: 1, CumCost: 3, CumRisk: 3},
		{EdgeID: 3, From: 2, To: 3, Cost: 4, Risk: 0, CumCost: 7, CumRisk: 3},
	}
	if !reflect.DeepEqual(res.Ledger, want) {
		t.Fatalf("ledger mismatch:\n got %+v\nwant %+v", res.Ledger, want)
	}
	if !reflect.DeepEqual(res.Nodes, []int{0, 1, 2, 3}) {
		t.Fatalf("got nodes %v, want [0 1 2 3]", res.Nodes)
	}
	if res.TotalCost != 7 || res.TotalRisk != 3 || res.Hops != 3 {
		t.Fatalf("totals cost=%d risk=%d hops=%d, want 7/3/3", res.TotalCost, res.TotalRisk, res.Hops)
	}
}

func TestValidation(t *testing.T) {
	if _, err := NewGraph(2, []Edge{{ID: 1, From: 0, To: 1, Cost: -1}}); err == nil {
		t.Fatal("expected error for negative cost")
	}
	if _, err := NewGraph(2, []Edge{{ID: 1, From: 0, To: 1, Risk: -1}}); err == nil {
		t.Fatal("expected error for negative risk")
	}
	if _, err := NewGraph(2, []Edge{{ID: 1, From: 0, To: 1}, {ID: 1, From: 1, To: 0}}); err == nil {
		t.Fatal("expected error for duplicate edge id")
	}
	if _, err := NewGraph(2, []Edge{{ID: 1, From: 0, To: 5}}); err == nil {
		t.Fatal("expected error for endpoint out of range")
	}
}

// Cross-check the Pareto-label solver against the independent
// (node, risk) state-expansion solver on many random small graphs,
// including parallel edges, self-loops and zero-cost cycles.
func TestCrossCheckRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 400; trial++ {
		n := 2 + rng.Intn(5)
		m := rng.Intn(11)
		edges := make([]Edge, 0, m)
		for i := 0; i < m; i++ {
			edges = append(edges, Edge{
				ID:   i + 1,
				From: rng.Intn(n),
				To:   rng.Intn(n),
				Cost: rng.Intn(6),
				Risk: rng.Intn(4),
			})
		}
		g, err := NewGraph(n, edges)
		if err != nil {
			t.Fatalf("trial %d: NewGraph: %v", trial, err)
		}
		src, dst := rng.Intn(n), rng.Intn(n)
		budget := rng.Intn(7)

		got, err := Solve(g, src, dst, budget)
		if err != nil {
			t.Fatalf("trial %d: Solve: %v", trial, err)
		}
		want, err := SolveByStateExpansion(g, src, dst, budget)
		if err != nil {
			t.Fatalf("trial %d: SolveByStateExpansion: %v", trial, err)
		}
		if got.Found != want.Found {
			t.Fatalf("trial %d: found mismatch: %v vs %v\nedges=%v src=%d dst=%d B=%d",
				trial, got.Found, want.Found, edges, src, dst, budget)
		}
		if !got.Found {
			continue
		}
		if got.TotalCost != want.TotalCost || got.TotalRisk != want.TotalRisk || got.Hops != want.Hops {
			t.Fatalf("trial %d: got cost=%d risk=%d hops=%d, want cost=%d risk=%d hops=%d\nedges=%v src=%d dst=%d B=%d",
				trial, got.TotalCost, got.TotalRisk, got.Hops, want.TotalCost, want.TotalRisk, want.Hops, edges, src, dst, budget)
		}
		if !reflect.DeepEqual(got.EdgeIDs, want.EdgeIDs) {
			t.Fatalf("trial %d: path mismatch: got %v want %v\nedges=%v src=%d dst=%d B=%d",
				trial, got.EdgeIDs, want.EdgeIDs, edges, src, dst, budget)
		}
		if want.TotalRisk > budget {
			t.Fatalf("trial %d: verifier returned risk %d > budget %d", trial, want.TotalRisk, budget)
		}
	}
}
