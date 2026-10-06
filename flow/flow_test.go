package flow

import (
	"math/rand"
	"testing"
)

// checkFlow validates a flow against bounds and node balances and
// recomputes its cost.
func checkFlow(t *testing.T, p *Problem, flows []int64) int64 {
	t.Helper()
	lo, hi, err := Bounds(p)
	if err != nil {
		t.Fatalf("Bounds: %v", err)
	}
	net := make([]int64, p.NumNodes)
	var cost int64
	for i, e := range p.Edges {
		if flows[i] < lo[i] || flows[i] > hi[i] {
			t.Fatalf("edge %d flow %d outside [%d,%d]", i, flows[i], lo[i], hi[i])
		}
		net[e.From] += flows[i]
		net[e.To] -= flows[i]
		cost += flows[i] * e.Cost
	}
	for v := 0; v < p.NumNodes; v++ {
		if net[v] != p.Net[v] {
			t.Fatalf("node %d balance %d, want %d", v, net[v], p.Net[v])
		}
	}
	return cost
}

// TestNegativeCycleNotIgnored is the case a naive shortest-augmenting-
// path solver gets wrong: the optimum routes one unit s->t (cost 0)
// and additionally circulates one unit around the negative cycle
// 1->2->1 (cost -2), for a total of -2.
func TestNegativeCycleNotIgnored(t *testing.T) {
	p := NewTransportation(4, 0, 3, 1, []Edge{
		{From: 0, To: 1, Cap: 1, Cost: 0},
		{From: 1, To: 3, Cap: 1, Cost: 0},
		{From: 1, To: 2, Cap: 1, Cost: -3},
		{From: 2, To: 1, Cap: 1, Cost: 1},
	})
	sol, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if !sol.Feasible {
		t.Fatalf("expected feasible, got cut %+v", sol.Cut)
	}
	if sol.Cost != -2 {
		t.Fatalf("cost = %d, want -2 (residual negative cycle must be cancelled)", sol.Cost)
	}
	if got := checkFlow(t, p, sol.Flows); got != sol.Cost {
		t.Fatalf("reported cost %d != recomputed %d", sol.Cost, got)
	}
}

// TestCommitmentsAndCancellation: executed flow cannot be reduced and
// a cancelled edge keeps its executed amount but accepts no new flow.
func TestCommitmentsAndCancellation(t *testing.T) {
	edges := []Edge{
		{From: 0, To: 1, Cap: 5, Cost: 1, Lower: 3},                   // 3 already executed
		{From: 0, To: 2, Cap: 5, Cost: 1},                             //
		{From: 1, To: 3, Cap: 5, Cost: 1},                             //
		{From: 2, To: 3, Cap: 5, Cost: -1, Lower: 2, Cancelled: true}, // cancelled, 2 executed stay
	}
	p := NewTransportation(4, 0, 3, 5, edges)
	sol, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if !sol.Feasible {
		t.Fatalf("expected feasible, cut %+v", sol.Cut)
	}
	if sol.Flows[0] < 3 {
		t.Fatalf("executed flow on edge 0 was reduced: %d", sol.Flows[0])
	}
	if sol.Flows[3] != 2 {
		t.Fatalf("cancelled edge 3 flow = %d, want frozen at executed 2", sol.Flows[3])
	}
	checkFlow(t, p, sol.Flows)
	// Optimal: 3 committed on 0->1->3 (cost 2 each), 2 frozen on
	// 0->2->3 (cost 0 each). Total 3*2 + 2*0 = 6.
	if sol.Cost != 6 {
		t.Fatalf("cost = %d, want 6", sol.Cost)
	}
}

// TestInfeasibleCut checks the min-cut witness on an under-capacitated
// network.
func TestInfeasibleCut(t *testing.T) {
	p := NewTransportation(4, 0, 3, 10, []Edge{
		{From: 0, To: 1, Cap: 3, Cost: 1},
		{From: 0, To: 2, Cap: 2, Cost: 1},
		{From: 1, To: 3, Cap: 3, Cost: 1},
		{From: 2, To: 3, Cap: 2, Cost: 1},
	})
	sol, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if sol.Feasible {
		t.Fatal("expected infeasible")
	}
	if sol.Cut == nil || sol.Cut.Deficit != 5 {
		t.Fatalf("cut = %+v, want deficit 5", sol.Cut)
	}
	if len(sol.Cut.SNodes) == 0 || len(sol.Cut.TNodes) == 0 {
		t.Fatalf("cut sides empty: %+v", sol.Cut)
	}
}

func randomProblem(r *rand.Rand) *Problem {
	n := 2 + r.Intn(5)
	m := r.Intn(9)
	edges := make([]Edge, m)
	for i := range edges {
		from, to := r.Intn(n), r.Intn(n)
		cap := int64(r.Intn(4))
		e := Edge{From: from, To: to, Cap: cap, Cost: int64(r.Intn(11) - 5)}
		if cap > 0 && r.Intn(2) == 0 {
			e.Lower = int64(r.Intn(int(cap) + 1))
		}
		if r.Intn(5) == 0 {
			e.Cancelled = true
		}
		edges[i] = e
	}
	s, t := r.Intn(n), r.Intn(n)
	for t == s {
		t = r.Intn(n)
	}
	return NewTransportation(n, s, t, int64(r.Intn(7)), edges)
}

// TestSolveMatchesExhaustive cross-checks the exact solver against the
// exhaustive branch-and-bound on random small instances.
func TestSolveMatchesExhaustive(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for iter := 0; iter < 400; iter++ {
		p := randomProblem(r)
		sol, err := Solve(p)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		res, err := Search(p, Budget{})
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		if res.Status == "budget_exceeded" {
			t.Fatalf("iter %d: unbudgeted search exhausted", iter)
		}
		if sol.Feasible != (res.Status == "optimal") {
			t.Fatalf("iter %d: feasibility mismatch solver=%v search=%s", iter, sol.Feasible, res.Status)
		}
		if !sol.Feasible {
			if sol.Cut == nil || sol.Cut.Deficit <= 0 {
				t.Fatalf("iter %d: infeasible without valid cut", iter)
			}
			continue
		}
		if sol.Cost != res.Cost {
			t.Fatalf("iter %d: solver cost %d != exhaustive cost %d", iter, sol.Cost, res.Cost)
		}
		if got := checkFlow(t, p, sol.Flows); got != sol.Cost {
			t.Fatalf("iter %d: solver flow invalid", iter)
		}
		if got := checkFlow(t, p, res.Flows); got != res.Cost {
			t.Fatalf("iter %d: search flow invalid", iter)
		}
	}
}

// TestFrontierResume: an interrupted search resumed from its frontier
// must reach the same optimum as an uninterrupted one.
func TestFrontierResume(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for iter := 0; iter < 30; iter++ {
		p := randomProblem(r)
		full, err := Search(p, Budget{})
		if err != nil {
			t.Fatal(err)
		}
		res, err := Search(p, Budget{MaxNodes: 5})
		if err != nil {
			t.Fatal(err)
		}
		for res.Status == "budget_exceeded" {
			res, err = ResumeSearch(p, res.Frontier, Budget{MaxNodes: res.Frontier.NodesExplored + 50})
			if err != nil {
				t.Fatal(err)
			}
		}
		if res.Status != full.Status {
			t.Fatalf("iter %d: resumed %s != full %s", iter, res.Status, full.Status)
		}
		if full.Status == "optimal" && res.Cost != full.Cost {
			t.Fatalf("iter %d: resumed cost %d != full cost %d", iter, res.Cost, full.Cost)
		}
	}
}

// TestBudgetBounds: a budget-stopped search must bracket the optimum.
func TestBudgetBounds(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	checked := 0
	for iter := 0; iter < 200 && checked < 20; iter++ {
		p := randomProblem(r)
		full, err := Search(p, Budget{})
		if err != nil {
			t.Fatal(err)
		}
		if full.Status != "optimal" {
			continue
		}
		res, err := Search(p, Budget{MaxNodes: 3})
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != "budget_exceeded" {
			continue
		}
		checked++
		if res.LowerBound > full.Cost {
			t.Fatalf("iter %d: lower bound %d above optimum %d", iter, res.LowerBound, full.Cost)
		}
		if res.HasUpperBound && res.UpperBound < full.Cost {
			t.Fatalf("iter %d: upper bound %d below optimum %d", iter, res.UpperBound, full.Cost)
		}
	}
	if checked == 0 {
		t.Fatal("no budget-exceeded case exercised")
	}
}
