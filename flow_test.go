package mincostflow

import (
	"math/big"
	"math/rand"
	"testing"
)

// bruteForce enumerates every integer flow edge-by-edge and returns the
// minimum feasible cost. Ground truth for tiny networks.
func bruteForce(g *Graph) (best *big.Int, feasible bool) {
	m := len(g.Edges)
	flow := make([]int64, m)
	net := make([]int64, g.N) // outflow - inflow so far
	var rec func(i int)
	rec = func(i int) {
		if i == m {
			for v := 0; v < g.N; v++ {
				if net[v] != g.Balance[v] {
					return
				}
			}
			c := new(big.Int)
			for j, e := range g.Edges {
				c.Add(c, new(big.Int).Mul(big.NewInt(flow[j]), e.cost()))
			}
			if !feasible || c.Cmp(best) < 0 {
				best, feasible = c, true
			}
			return
		}
		e := g.Edges[i]
		for f := e.Lower; f <= e.Upper; f++ {
			flow[i] = f
			net[e.From] += f
			net[e.To] -= f
			rec(i + 1)
			net[e.From] -= f
			net[e.To] += f
		}
	}
	rec(0)
	return best, feasible
}

// checkFlow verifies bounds and conservation of a returned flow on the
// ORIGINAL network (this is what rejects accidentally returning the
// auxiliary-network flow f' instead of Lower + f').
func checkFlow(t *testing.T, g *Graph, flow []int64) {
	t.Helper()
	net := make([]int64, g.N)
	for i, e := range g.Edges {
		if flow[i] < e.Lower || flow[i] > e.Upper {
			t.Fatalf("edge %d flow %d outside [%d,%d]", i, flow[i], e.Lower, e.Upper)
		}
		net[e.From] += flow[i]
		net[e.To] -= flow[i]
	}
	for v := 0; v < g.N; v++ {
		if net[v] != g.Balance[v] {
			t.Fatalf("node %d: net outflow %d != balance %d", v, net[v], g.Balance[v])
		}
	}
}

// checkCut verifies the infeasibility certificate: X nonempty and
// net demand of X exceeds u(A->X) - l(X->A).
func checkCut(t *testing.T, g *Graph, r *Result) {
	t.Helper()
	demand, capIn, lowOut, size := int64(0), int64(0), int64(0), 0
	for v := 0; v < g.N; v++ {
		if r.Cut[v] {
			size++
			demand -= g.Balance[v]
		}
	}
	if size == 0 {
		t.Fatalf("empty cut certificate")
	}
	for _, e := range g.Edges {
		if !r.Cut[e.From] && r.Cut[e.To] {
			capIn += e.Upper
		}
		if r.Cut[e.From] && !r.Cut[e.To] {
			lowOut += e.Lower
		}
	}
	if demand != r.CutDemand || capIn-lowOut != r.CutCapacity {
		t.Fatalf("certificate numbers inconsistent: got demand=%d cap=%d, want demand=%d cap=%d",
			r.CutDemand, r.CutCapacity, demand, capIn-lowOut)
	}
	if demand <= capIn-lowOut {
		t.Fatalf("cut not violated: demand %d <= capacity %d", demand, capIn-lowOut)
	}
}

func randomGraph(r *rand.Rand) *Graph {
	n := 2 + r.Intn(4) // 2..5 nodes
	m := r.Intn(8)     // 0..7 edges
	g := &Graph{N: n, Balance: make([]int64, n)}
	for i := 0; i < m; i++ {
		lower := int64(r.Intn(2)) // 0 or 1
		g.Edges = append(g.Edges, Edge{
			From:  r.Intn(n),
			To:    r.Intn(n),
			Lower: lower,
			Upper: lower + int64(r.Intn(3)), // width 0..2
			Cost:  big.NewInt(int64(r.Intn(11) - 5)),
		})
	}
	for v := range g.Balance {
		g.Balance[v] = int64(r.Intn(7) - 3)
	}
	if r.Intn(2) == 0 { // half the time force total balance to zero
		sum := int64(0)
		for _, b := range g.Balance {
			sum += b
		}
		g.Balance[n-1] -= sum
	}
	return g
}

func TestRandomAgainstBruteForce(t *testing.T) {
	for seed := int64(1); seed <= 5; seed++ {
		r := rand.New(rand.NewSource(seed))
		for trial := 0; trial < 300; trial++ {
			g := randomGraph(r)
			sum := int64(0)
			for _, b := range g.Balance {
				sum += b
			}
			res := Solve(g)
			if sum != 0 {
				if res.Status != InvalidInput {
					t.Fatalf("trial %d: unbalanced graph not rejected: %v", trial, res.Status)
				}
				continue
			}
			best, feasible := bruteForce(g)
			if !feasible {
				if res.Status != Infeasible {
					t.Fatalf("trial %d: brute force infeasible, solver says %v", trial, res.Status)
				}
				checkCut(t, g, res)
				continue
			}
			if res.Status != Optimal {
				t.Fatalf("trial %d: brute force feasible, solver says %v (%s)", trial, res.Status, res.Reason)
			}
			checkFlow(t, g, res.Flow)
			if res.Cost.Cmp(best) != 0 {
				t.Fatalf("trial %d: cost %s, brute force optimum %s", trial, res.Cost, best)
			}
		}
	}
}

func TestNegativeCycleCirculation(t *testing.T) {
	// Zero balances: feasibility is trivial (f = Lower). Optimality
	// requires pushing 2 units around the negative cycle 0->1->2->0
	// (cost -3 per unit) despite the positive-cost direct arcs.
	g := &Graph{
		N:       3,
		Balance: []int64{0, 0, 0},
		Edges: []Edge{
			{From: 0, To: 1, Lower: 0, Upper: 2, Cost: big.NewInt(-1)},
			{From: 1, To: 2, Lower: 0, Upper: 2, Cost: big.NewInt(-1)},
			{From: 2, To: 0, Lower: 0, Upper: 2, Cost: big.NewInt(-1)},
			{From: 0, To: 2, Lower: 0, Upper: 5, Cost: big.NewInt(4)},
		},
	}
	res := Solve(g)
	if res.Status != Optimal {
		t.Fatalf("status %v", res.Status)
	}
	checkFlow(t, g, res.Flow)
	if res.Cost.Cmp(big.NewInt(-6)) != 0 {
		t.Fatalf("cost %s, want -6", res.Cost)
	}
}

func TestLowerBoundsMappedBack(t *testing.T) {
	// Lower bounds force flow; the solver must return f = Lower + f',
	// not the raw auxiliary flow f'.
	g := &Graph{
		N:       3,
		Balance: []int64{3, -1, -2},
		Edges: []Edge{
			{From: 0, To: 1, Lower: 1, Upper: 3, Cost: big.NewInt(1)},
			{From: 0, To: 2, Lower: 0, Upper: 3, Cost: big.NewInt(5)},
			{From: 1, To: 2, Lower: 0, Upper: 3, Cost: big.NewInt(1)},
		},
	}
	res := Solve(g)
	if res.Status != Optimal {
		t.Fatalf("status %v", res.Status)
	}
	checkFlow(t, g, res.Flow)
	// Optimum: 0->1 carries 3, 1->2 carries 2, cost 3+2=5.
	if res.Cost.Cmp(big.NewInt(5)) != 0 {
		t.Fatalf("cost %s, want 5", res.Cost)
	}
}

func TestInfeasibleCutCertificate(t *testing.T) {
	// Node 2 demands 5 but only 3 can cross the cut into it.
	g := &Graph{
		N:       3,
		Balance: []int64{5, 0, -5},
		Edges: []Edge{
			{From: 0, To: 1, Lower: 0, Upper: 10, Cost: big.NewInt(1)},
			{From: 1, To: 2, Lower: 0, Upper: 3, Cost: big.NewInt(1)},
		},
	}
	res := Solve(g)
	if res.Status != Infeasible {
		t.Fatalf("status %v", res.Status)
	}
	checkCut(t, g, res)
	if !res.Cut[2] {
		t.Fatalf("certificate should contain the starved node 2: %v", res.Cut)
	}
}

func TestLowerBoundConflict(t *testing.T) {
	g := &Graph{
		N:       2,
		Balance: []int64{0, 0},
		Edges:   []Edge{{From: 0, To: 1, Lower: 3, Upper: 2, Cost: big.NewInt(1)}},
	}
	if res := Solve(g); res.Status != InvalidInput {
		t.Fatalf("lower>upper not rejected: %v", res.Status)
	}
	// Lower bounds alone can also make the network infeasible: node 1
	// must absorb 4 forced units but can only shed 1.
	g2 := &Graph{
		N:       3,
		Balance: []int64{0, 0, 0},
		Edges: []Edge{
			{From: 0, To: 1, Lower: 4, Upper: 4, Cost: big.NewInt(0)},
			{From: 1, To: 2, Lower: 0, Upper: 1, Cost: big.NewInt(0)},
			{From: 2, To: 0, Lower: 0, Upper: 10, Cost: big.NewInt(0)},
		},
	}
	res := Solve(g2)
	if res.Status != Infeasible {
		t.Fatalf("status %v", res.Status)
	}
	checkCut(t, g2, res)
}

func TestBigIntegerCosts(t *testing.T) {
	huge, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	g := &Graph{
		N:       2,
		Balance: []int64{3, -3},
		Edges: []Edge{
			{From: 0, To: 1, Lower: 0, Upper: 2, Cost: huge},
			{From: 0, To: 1, Lower: 0, Upper: 2, Cost: big.NewInt(1)},
		},
	}
	res := Solve(g)
	if res.Status != Optimal {
		t.Fatalf("status %v", res.Status)
	}
	checkFlow(t, g, res.Flow)
	// 2 cheap units + 1 huge unit.
	want := new(big.Int).Add(huge, big.NewInt(2))
	if res.Cost.Cmp(want) != 0 {
		t.Fatalf("cost %s, want %s", res.Cost, want)
	}
}
