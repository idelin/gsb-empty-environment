package mincostflow

import (
	"math/big"
	"math/rand"
	"testing"
)

func bigInt(s string) *big.Int {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("bad big.Int literal: " + s)
	}
	return v
}

func TestUnbalanced(t *testing.T) {
	g := &Graph{
		NumNodes: 2,
		Edges:    []Edge{{From: 0, To: 1, Lower: 0, Upper: 10, Cost: big.NewInt(1)}},
		Balance:  []int64{5, -3},
	}
	if r := Solve(g); r.Status != Unbalanced {
		t.Fatalf("got %v, want Unbalanced", r.Status)
	}
}

func TestBadBounds(t *testing.T) {
	g := &Graph{
		NumNodes: 2,
		Edges:    []Edge{{From: 0, To: 1, Lower: 4, Upper: 3, Cost: big.NewInt(1)}},
		Balance:  []int64{0, 0},
	}
	if r := Solve(g); r.Status != BadBounds {
		t.Fatalf("got %v, want BadBounds", r.Status)
	}
}

func TestSimpleOptimal(t *testing.T) {
	// Two disjoint paths 0->2: direct cost 5 (cap 1), via 1 cost 1+1 (cap 1).
	// 2 units of supply: one takes each path, total cost 5+2 = 7.
	g := &Graph{
		NumNodes: 3,
		Edges: []Edge{
			{From: 0, To: 2, Lower: 0, Upper: 1, Cost: big.NewInt(5)},
			{From: 0, To: 1, Lower: 0, Upper: 1, Cost: big.NewInt(1)},
			{From: 1, To: 2, Lower: 0, Upper: 1, Cost: big.NewInt(1)},
		},
		Balance: []int64{2, 0, -2},
	}
	r := Solve(g)
	if r.Status != Optimal {
		t.Fatalf("got %v (%s), want Optimal", r.Status, r.Message)
	}
	if err := CheckFlow(g, r.Flow); err != nil {
		t.Fatalf("invalid flow: %v", err)
	}
	if r.Cost.Cmp(big.NewInt(7)) != 0 {
		t.Fatalf("cost = %v, want 7", r.Cost)
	}
}

func TestNegativeCycleCanceling(t *testing.T) {
	// Zero balances: the zero flow is feasible, but the cycle
	// 0->1->2->0 has cost -3 and capacity 2, so the optimum pushes
	// 2 units around it for a total cost of -6.
	g := &Graph{
		NumNodes: 3,
		Edges: []Edge{
			{From: 0, To: 1, Lower: 0, Upper: 2, Cost: big.NewInt(-4)},
			{From: 1, To: 2, Lower: 0, Upper: 2, Cost: big.NewInt(1)},
			{From: 2, To: 0, Lower: 0, Upper: 2, Cost: big.NewInt(0)},
		},
		Balance: []int64{0, 0, 0},
	}
	r := Solve(g)
	if r.Status != Optimal {
		t.Fatalf("got %v, want Optimal", r.Status)
	}
	if err := CheckFlow(g, r.Flow); err != nil {
		t.Fatalf("invalid flow: %v", err)
	}
	if r.Cost.Cmp(big.NewInt(-6)) != 0 {
		t.Fatalf("cost = %v, want -6", r.Cost)
	}
}

func TestInfeasibleCertificate(t *testing.T) {
	// Node 0 must ship 10 but the only outgoing edge carries 5.
	g := &Graph{
		NumNodes: 2,
		Edges:    []Edge{{From: 0, To: 1, Lower: 0, Upper: 5, Cost: big.NewInt(1)}},
		Balance:  []int64{10, -10},
	}
	r := Solve(g)
	if r.Status != Infeasible {
		t.Fatalf("got %v, want Infeasible", r.Status)
	}
	c := r.Certificate
	if c == nil {
		t.Fatal("missing certificate")
	}
	if !c.InSet[0] || c.InSet[1] {
		t.Fatalf("certificate set = %v, want {0}", c.InSet)
	}
	if c.Supply != 10 || c.OutCapacity != 5 || c.InLower != 0 {
		t.Fatalf("certificate numbers wrong: %+v", c)
	}
	if c.Violation() != 5 {
		t.Fatalf("violation = %d, want 5", c.Violation())
	}
	if BruteForce(g).Feasible {
		t.Fatal("brute force disagrees: instance is feasible")
	}
}

func TestLowerBoundsAddedBack(t *testing.T) {
	// Every edge has a positive lower bound. If Solve returned the
	// auxiliary flow without adding the lower bounds back, the
	// balances would be violated.
	g := &Graph{
		NumNodes: 3,
		Edges: []Edge{
			{From: 0, To: 1, Lower: 3, Upper: 6, Cost: big.NewInt(2)},
			{From: 1, To: 2, Lower: 2, Upper: 6, Cost: big.NewInt(3)},
			{From: 0, To: 2, Lower: 1, Upper: 4, Cost: big.NewInt(10)},
		},
		Balance: []int64{7, 0, -7},
	}
	r := Solve(g)
	if r.Status != Optimal {
		t.Fatalf("got %v (%s), want Optimal", r.Status, r.Message)
	}
	if err := CheckFlow(g, r.Flow); err != nil {
		t.Fatalf("returned flow violates original network: %v", err)
	}
	b := BruteForce(g)
	if !b.Feasible || r.Cost.Cmp(b.Cost) != 0 {
		t.Fatalf("cost = %v, brute force optimum = %v", r.Cost, b.Cost)
	}
}

func TestBigCosts(t *testing.T) {
	// Costs beyond 64 bits; the cheaper path differs from the
	// expensive one only in the high bits.
	cheap := bigInt("1267650600228229401496703205371") // 2^100 - 5
	pricy := bigInt("1267650600228229401496703205379") // 2^100 + 3
	g := &Graph{
		NumNodes: 3,
		Edges: []Edge{
			{From: 0, To: 2, Lower: 0, Upper: 4, Cost: pricy},
			{From: 0, To: 1, Lower: 0, Upper: 4, Cost: cheap},
			{From: 1, To: 2, Lower: 0, Upper: 4, Cost: big.NewInt(0)},
		},
		Balance: []int64{4, 0, -4},
	}
	r := Solve(g)
	if r.Status != Optimal {
		t.Fatalf("got %v, want Optimal", r.Status)
	}
	if err := CheckFlow(g, r.Flow); err != nil {
		t.Fatalf("invalid flow: %v", err)
	}
	want := new(big.Int).Mul(big.NewInt(4), cheap)
	if r.Cost.Cmp(want) != 0 {
		t.Fatalf("cost = %v, want %v", r.Cost, want)
	}
	b := BruteForce(g)
	if !b.Feasible || b.Cost.Cmp(want) != 0 {
		t.Fatalf("brute force = %v, want %v", b.Cost, want)
	}
}

// checkCertificate independently recomputes the cut quantities from
// the certificate's node set and confirms the cut is really violated.
func checkCertificate(t *testing.T, g *Graph, c *CutCertificate) {
	t.Helper()
	if len(c.InSet) != g.NumNodes {
		t.Fatalf("certificate set has %d entries, want %d", len(c.InSet), g.NumNodes)
	}
	var supply, outCap, inLower int64
	empty := true
	for v := 0; v < g.NumNodes; v++ {
		if c.InSet[v] {
			empty = false
			supply += g.Balance[v]
		}
	}
	for _, e := range g.Edges {
		if c.InSet[e.From] && !c.InSet[e.To] {
			outCap += e.Upper
		}
		if !c.InSet[e.From] && c.InSet[e.To] {
			inLower += e.Lower
		}
	}
	if empty {
		t.Fatal("certificate set is empty")
	}
	if supply != c.Supply || outCap != c.OutCapacity || inLower != c.InLower {
		t.Fatalf("certificate inconsistent: got (%d,%d,%d), recomputed (%d,%d,%d)",
			c.Supply, c.OutCapacity, c.InLower, supply, outCap, inLower)
	}
	if supply <= outCap-inLower {
		t.Fatalf("cut not violated: supply %d <= capacity %d - lower %d", supply, outCap, inLower)
	}
}

func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261004))
	const trials = 400
	feasible, infeasible := 0, 0
	for trial := 0; trial < trials; trial++ {
		n := 2 + rng.Intn(4) // 2..5 nodes
		m := rng.Intn(9)     // 0..8 edges
		g := &Graph{NumNodes: n, Balance: make([]int64, n)}
		for i := 0; i < m; i++ {
			lower := int64(rng.Intn(3)) // 0..2
			e := Edge{
				From:  rng.Intn(n),
				To:    rng.Intn(n),
				Lower: lower,
				Upper: lower + int64(rng.Intn(4)), // lower..lower+3
				Cost:  big.NewInt(int64(rng.Intn(15) - 5)),
			}
			g.Edges = append(g.Edges, e)
		}
		if rng.Intn(2) == 0 {
			// Derive balances from a random in-bounds flow so the
			// instance is guaranteed feasible.
			for _, e := range g.Edges {
				f := e.Lower
				if e.Upper > e.Lower {
					f += int64(rng.Intn(int(e.Upper - e.Lower + 1)))
				}
				g.Balance[e.From] += f
				g.Balance[e.To] -= f
			}
		} else {
			var sum int64
			for v := 0; v < n-1; v++ {
				g.Balance[v] = int64(rng.Intn(7) - 3)
				sum += g.Balance[v]
			}
			g.Balance[n-1] = -sum // keep the instance balanced
		}

		got := Solve(g)
		want := BruteForce(g)

		if want.Feasible {
			feasible++
			if got.Status != Optimal {
				t.Fatalf("trial %d: status %v, brute force found feasible flow", trial, got.Status)
			}
			if err := CheckFlow(g, got.Flow); err != nil {
				t.Fatalf("trial %d: invalid flow: %v", trial, err)
			}
			if got.Cost.Cmp(want.Cost) != 0 {
				t.Fatalf("trial %d: cost %v, brute force optimum %v", trial, got.Cost, want.Cost)
			}
		} else {
			infeasible++
			if got.Status != Infeasible {
				t.Fatalf("trial %d: status %v, brute force says infeasible", trial, got.Status)
			}
			if got.Certificate == nil {
				t.Fatalf("trial %d: infeasible without certificate", trial)
			}
			checkCertificate(t, g, got.Certificate)
		}
	}
	t.Logf("%d feasible, %d infeasible out of %d trials", feasible, infeasible, trials)
}

func TestRandomBigCostsAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const trials = 100
	for trial := 0; trial < trials; trial++ {
		n := 2 + rng.Intn(3)
		m := 1 + rng.Intn(6)
		g := &Graph{NumNodes: n, Balance: make([]int64, n)}
		for i := 0; i < m; i++ {
			lower := int64(rng.Intn(2))
			// Costs up to ~2^90, positive and negative.
			cost := new(big.Int).Rand(rng, new(big.Int).Lsh(big.NewInt(1), 90))
			if rng.Intn(2) == 0 {
				cost.Neg(cost)
			}
			g.Edges = append(g.Edges, Edge{
				From:  rng.Intn(n),
				To:    rng.Intn(n),
				Lower: lower,
				Upper: lower + int64(rng.Intn(3)),
				Cost:  cost,
			})
		}
		var sum int64
		for v := 0; v < n-1; v++ {
			g.Balance[v] = int64(rng.Intn(5) - 2)
			sum += g.Balance[v]
		}
		g.Balance[n-1] = -sum

		got := Solve(g)
		want := BruteForce(g)
		if want.Feasible != (got.Status == Optimal) {
			t.Fatalf("trial %d: status %v, brute force feasible=%v", trial, got.Status, want.Feasible)
		}
		if want.Feasible {
			if err := CheckFlow(g, got.Flow); err != nil {
				t.Fatalf("trial %d: invalid flow: %v", trial, err)
			}
			if got.Cost.Cmp(want.Cost) != 0 {
				t.Fatalf("trial %d: cost %v, brute force optimum %v", trial, got.Cost, want.Cost)
			}
		} else if got.Status == Infeasible {
			checkCertificate(t, g, got.Certificate)
		}
	}
}
