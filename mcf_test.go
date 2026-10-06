package mcf

import (
	"math/rand"
	"testing"
)

// randomInstance builds a random small graph plus supplies that are
// feasible with probability ~1/2: feasible ones come from a concrete
// random flow, infeasible ones from random imbalances.
func randomInstance(r *rand.Rand, feasible bool) (Graph, []int) {
	n := 2 + r.Intn(5)  // 2..6 nodes
	m := 1 + r.Intn(10) // 1..10 edges
	g := Graph{N: n}
	for i := 0; i < m; i++ {
		from := r.Intn(n)
		to := r.Intn(n)
		for to == from {
			to = r.Intn(n)
		}
		cap := 1 + r.Intn(4)
		lo := r.Intn(cap + 1)
		g.Edges = append(g.Edges, Edge{
			From: from, To: to, Lo: lo, Cap: cap,
			Cost: r.Intn(15) - 5, // costs in [-5, 9]
		})
	}
	supply := make([]int, n)
	if feasible {
		for _, e := range g.Edges {
			f := e.Lo
			if e.Cap > e.Lo {
				f += r.Intn(e.Cap - e.Lo + 1)
			}
			supply[e.From] += f
			supply[e.To] -= f
		}
	} else {
		for v := range supply {
			supply[v] = r.Intn(7) - 3
		}
		supply[0] = 0
		for v := 1; v < n; v++ {
			supply[0] -= supply[v]
		}
	}
	return g, supply
}

func TestSolveMatchesExhaustive(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for trial := 0; trial < 400; trial++ {
		g, supply := randomInstance(r, trial%2 == 0)
		got := Solve(g, supply)
		want := Exhaustive(g, supply, 1<<26)
		if want.Status == BudgetExhausted {
			t.Fatalf("trial %d: exhaustive budget exceeded; instance too large for test", trial)
		}
		if got.Status != want.Status {
			t.Fatalf("trial %d: status %v, exhaustive %v", trial, got.Status, want.Status)
		}
		if got.Status == Optimal {
			if got.Cost != want.Cost {
				t.Fatalf("trial %d: cost %d, exhaustive %d", trial, got.Cost, want.Cost)
			}
			if !Feasible(g, supply, got.Flow) {
				t.Fatalf("trial %d: returned flow infeasible", trial)
			}
		}
	}
}

// A zero-supply circulation instance whose optimum only reveals itself
// by canceling a negative-cost residual cycle; a solver that stops
// after shortest augmenting paths would report cost 0.
func TestNegativeCycleCanceled(t *testing.T) {
	g := Graph{N: 3, Edges: []Edge{
		{From: 0, To: 1, Cap: 2, Cost: -3},
		{From: 1, To: 2, Cap: 2, Cost: 1},
		{From: 2, To: 0, Cap: 2, Cost: 1},
	}}
	res := Solve(g, []int{0, 0, 0})
	if res.Status != Optimal {
		t.Fatalf("status %v", res.Status)
	}
	if res.Cost != -2 {
		t.Fatalf("cost %d, want -2 (flow 2 around the -1 cycle)", res.Cost)
	}
	for i, f := range res.Flow {
		if f != 2 {
			t.Fatalf("edge %d flow %d, want 2", i, f)
		}
	}
}

func TestInfeasibleReported(t *testing.T) {
	g := Graph{N: 2, Edges: []Edge{{From: 0, To: 1, Cap: 3, Cost: 1}}}
	res := Solve(g, []int{5, -5})
	if res.Status != Infeasible {
		t.Fatalf("status %v, want infeasible (demand 5 > capacity 3)", res.Status)
	}
	if res.Flow != nil {
		t.Fatalf("infeasible result must not carry a flow")
	}
}

func TestLowerBoundsRespected(t *testing.T) {
	g := Graph{N: 3, Edges: []Edge{
		{From: 0, To: 1, Lo: 2, Cap: 4, Cost: 1},
		{From: 1, To: 2, Cap: 5, Cost: 1},
		{From: 0, To: 2, Cap: 5, Cost: 5},
	}}
	res := Solve(g, []int{4, 0, -4})
	if res.Status != Optimal {
		t.Fatalf("status %v", res.Status)
	}
	// edge0 >= 2 forced; cheapest is 4 through the 0->1->2 path.
	want := []int{4, 4, 0}
	for i := range want {
		if res.Flow[i] != want[i] {
			t.Fatalf("flow %v, want %v", res.Flow, want)
		}
	}
	if res.Cost != 8 {
		t.Fatalf("cost %d, want 8", res.Cost)
	}
}
