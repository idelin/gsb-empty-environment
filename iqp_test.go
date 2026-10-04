package iqp

import (
	"math/big"
	"math/rand"
	"testing"
)

func bi(v int64) *big.Int { return big.NewInt(v) }

func bis(vs ...int64) []*big.Int {
	out := make([]*big.Int, len(vs))
	for i, v := range vs {
		out[i] = bi(v)
	}
	return out
}

// enumerate returns the minimum cost over the box and the
// lexicographically smallest point attaining it.
func enumerate(p *Problem, lo, hi []*big.Int) (*big.Int, []*big.Int) {
	n := p.Dim()
	var bestCost *big.Int
	var bestX []*big.Int
	x := make([]*big.Int, n)
	var rec func(i int)
	rec = func(i int) {
		if i == n {
			cost := p.Eval(x)
			if bestCost == nil || cost.Cmp(bestCost) < 0 {
				bestCost = cost
				bestX = cloneSlice(x)
			}
			return
		}
		x[i] = new(big.Int).Set(lo[i])
		for one := big.NewInt(1); x[i].Cmp(hi[i]) <= 0; x[i].Add(x[i], one) {
			rec(i + 1)
		}
	}
	rec(0)
	return bestCost, bestX
}

func randProblem(rng *rand.Rand, maxN, maxCoef, maxBox int64) *Problem {
	n := 1 + rng.Intn(int(maxN))
	q := make([][]*big.Int, n)
	for i := range q {
		q[i] = make([]*big.Int, n)
	}
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			v := bi(rng.Int63n(2*maxCoef+1) - maxCoef)
			q[i][j] = v
			q[j][i] = v
		}
	}
	p := &Problem{
		Q:     q,
		C:     bis(0),
		Const: bi(rng.Int63n(2*maxCoef+1) - maxCoef),
		Lo:    make([]*big.Int, n),
		Hi:    make([]*big.Int, n),
	}
	p.C = make([]*big.Int, n)
	for i := 0; i < n; i++ {
		p.C[i] = bi(rng.Int63n(2*maxCoef+1) - maxCoef)
		lo := rng.Int63n(2*maxBox+1) - maxBox
		hi := lo + rng.Int63n(2*maxBox+1) // may equal lo: degenerate boxes
		if hi > maxBox {
			hi = maxBox
		}
		p.Lo[i] = bi(lo)
		p.Hi[i] = bi(hi)
	}
	return p
}

func checkAgainstEnumeration(t *testing.T, p *Problem) {
	t.Helper()
	wantCost, wantX := enumerate(p, p.Lo, p.Hi)
	res := p.Solve(-1)
	if res.Status != Optimal {
		t.Fatalf("status = %v, want optimal", res.Status)
	}
	if res.Upper.Cmp(wantCost) != 0 {
		t.Fatalf("cost = %v, want %v (Q=%v C=%v Lo=%v Hi=%v)",
			res.Upper, wantCost, p.Q, p.C, p.Lo, p.Hi)
	}
	if cmpSlice(res.X, wantX) != 0 {
		t.Fatalf("argmin = %v, want lex-smallest %v", res.X, wantX)
	}
	if res.Lower.Cmp(wantCost) != 0 {
		t.Fatalf("lower bound at optimality = %v, want %v", res.Lower, wantCost)
	}
}

func TestSolveMatchesEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 400; trial++ {
		checkAgainstEnumeration(t, randProblem(rng, 4, 9, 6))
	}
}

func TestNegativeDefinite(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 200; trial++ {
		p := randProblem(rng, 3, 9, 5)
		n := p.Dim()
		// Force Q negative definite: Q = -D - I with D >= 0 diagonal.
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if i == j {
					p.Q[i][j] = bi(-1 - rng.Int63n(9))
				} else {
					p.Q[i][j] = bi(0)
				}
			}
		}
		checkAgainstEnumeration(t, p)
		// A strictly concave objective attains its box minimum at a vertex.
		res := p.Solve(-1)
		for i := 0; i < n; i++ {
			if res.X[i].Cmp(p.Lo[i]) != 0 && res.X[i].Cmp(p.Hi[i]) != 0 {
				t.Fatalf("concave optimum not at vertex: x=%v box=[%v,%v]",
					res.X, p.Lo, p.Hi)
			}
		}
	}
}

func TestIndefiniteCrossTerms(t *testing.T) {
	// Saddle-shaped objective: x^2 - y^2 + 4xy pushes the optimum to a
	// corner; a PD-only lower bound would be invalid here.
	p := &Problem{
		Q:     [][]*big.Int{{bi(1), bi(2)}, {bi(2), bi(-1)}},
		C:     bis(3, -2),
		Const: bi(5),
		Lo:    bis(-4, -4),
		Hi:    bis(4, 4),
	}
	checkAgainstEnumeration(t, p)
	res := p.Solve(-1)
	wantCost, _ := enumerate(p, p.Lo, p.Hi)
	if res.Upper.Cmp(wantCost) != 0 {
		t.Fatalf("indefinite solve: got %v want %v", res.Upper, wantCost)
	}
}

func TestSinglePointBox(t *testing.T) {
	p := &Problem{
		Q:     [][]*big.Int{{bi(-3), bi(5)}, {bi(5), bi(2)}},
		C:     bis(7, -11),
		Const: bi(13),
		Lo:    bis(4, -6),
		Hi:    bis(4, -6),
	}
	res := p.Solve(-1)
	if res.Status != Optimal {
		t.Fatalf("status = %v", res.Status)
	}
	want := p.Eval(bis(4, -6))
	if res.Upper.Cmp(want) != 0 || res.Lower.Cmp(want) != 0 {
		t.Fatalf("single point: upper=%v lower=%v want %v", res.Upper, res.Lower, want)
	}
	if cmpSlice(res.X, bis(4, -6)) != 0 {
		t.Fatalf("single point: x = %v", res.X)
	}
}

func TestBoundaryOptimum(t *testing.T) {
	// Convex objective with unconstrained minimiser at (10, -7), far
	// outside the box: the constrained optimum lies on the boundary.
	p := &Problem{
		Q:     [][]*big.Int{{bi(2), bi(0)}, {bi(0), bi(2)}},
		C:     bis(-40, 28),
		Const: bi(0),
		Lo:    bis(0, -2),
		Hi:    bis(3, 2),
	}
	res := p.Solve(-1)
	want := bis(3, -2)
	if cmpSlice(res.X, want) != 0 {
		t.Fatalf("boundary optimum = %v, want %v", res.X, want)
	}
	checkAgainstEnumeration(t, p)
}

func TestTieBreakLexicographic(t *testing.T) {
	// f = (x - y)^2 has optima (0,0), (1,1), (2,2) on [0,2]^2.
	p := &Problem{
		Q:     [][]*big.Int{{bi(1), bi(-1)}, {bi(-1), bi(1)}},
		C:     bis(0, 0),
		Const: bi(0),
		Lo:    bis(0, 0),
		Hi:    bis(2, 2),
	}
	res := p.Solve(-1)
	if res.Upper.Cmp(bi(0)) != 0 {
		t.Fatalf("tie cost = %v, want 0", res.Upper)
	}
	if cmpSlice(res.X, bis(0, 0)) != 0 {
		t.Fatalf("tie argmin = %v, want (0,0)", res.X)
	}
}

func TestBudgetExhaustion(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	p := randProblem(rng, 3, 9, 30)
	optCost, _ := enumerate(p, p.Lo, p.Hi)

	res := p.Solve(5)
	if res.Status != BudgetExhausted {
		t.Fatalf("status = %v, want budget_exhausted", res.Status)
	}
	if res.Nodes != 5 {
		t.Fatalf("nodes = %d, want 5", res.Nodes)
	}
	if res.Lower == nil || res.Lower.Cmp(optCost) > 0 {
		t.Fatalf("lower bound %v exceeds optimum %v", res.Lower, optCost)
	}
	if res.Upper != nil && res.Upper.Cmp(optCost) < 0 {
		t.Fatalf("upper bound %v below optimum %v", res.Upper, optCost)
	}
	// Zero budget: no nodes expanded, but the root bound must still hold.
	res0 := p.Solve(0)
	if res0.Nodes != 0 || res0.Status != BudgetExhausted {
		t.Fatalf("zero budget: nodes=%d status=%v", res0.Nodes, res0.Status)
	}
	if res0.Lower.Cmp(optCost) > 0 {
		t.Fatalf("zero-budget lower %v exceeds optimum %v", res0.Lower, optCost)
	}
	// Determinism: identical runs give identical results.
	a, b := p.Solve(17), p.Solve(17)
	if a.Upper.Cmp(b.Upper) != 0 || a.Lower.Cmp(b.Lower) != 0 || a.Nodes != b.Nodes {
		t.Fatalf("non-deterministic: %v vs %v", a, b)
	}
}

// TestPruneBoundsValid verifies, for every node created during the search,
// that its pruning bound never exceeds the true minimum over the node's
// sub-box (checked by full enumeration).
func TestPruneBoundsValid(t *testing.T) {
	rng := rand.New(rand.NewSource(1234))
	for trial := 0; trial < 60; trial++ {
		p := randProblem(rng, 3, 9, 4)
		s := &Solver{P: p}
		s.OnNode = func(lo, hi []*big.Int, lb *big.Int) {
			boxMin, _ := enumerate(p, lo, hi)
			if lb.Cmp(boxMin) > 0 {
				t.Fatalf("invalid bound: lb=%v > box min=%v over [%v,%v]",
					lb, boxMin, lo, hi)
			}
		}
		res := s.Solve(-1)
		if res.Status != Optimal {
			t.Fatalf("status = %v", res.Status)
		}
	}
}

// TestLowerBoundTightOnSingletons: on a single-point box the interval
// bound must equal the exact cost.
func TestLowerBoundTightOnSingletons(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for trial := 0; trial < 100; trial++ {
		p := randProblem(rng, 4, 9, 6)
		pt := make([]*big.Int, p.Dim())
		for i := range pt {
			pt[i] = bi(p.Lo[i].Int64() + rng.Int63n(p.Hi[i].Int64()-p.Lo[i].Int64()+1))
		}
		lb := p.lowerBound(pt, pt)
		if lb.Cmp(p.Eval(pt)) != 0 {
			t.Fatalf("singleton bound %v != exact %v at %v", lb, p.Eval(pt), pt)
		}
	}
}

func TestHugeCoefficients(t *testing.T) {
	// Coefficients around 2^100 exercise big.Int arithmetic; the box is
	// small so enumeration is cheap.
	mk := func(e uint, base int64) *big.Int {
		return new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), e), bi(base))
	}
	p := &Problem{
		Q: [][]*big.Int{
			{mk(100, 3), mk(97, -5)},
			{mk(97, -5), mk(99, -2)}, // indefinite
		},
		C:     []*big.Int{mk(101, -7), mk(98, 11)},
		Const: mk(103, 1),
		Lo:    bis(-3, -3),
		Hi:    bis(3, 3),
	}
	checkAgainstEnumeration(t, p)
}

func TestBudgetEventuallyOptimal(t *testing.T) {
	// With a generous budget the solver must prove optimality and the
	// reported bounds must collapse to the optimum.
	rng := rand.New(rand.NewSource(2024))
	p := randProblem(rng, 3, 6, 8)
	optCost, _ := enumerate(p, p.Lo, p.Hi)
	res := p.Solve(1 << 20)
	if res.Status != Optimal || res.Upper.Cmp(optCost) != 0 || res.Lower.Cmp(optCost) != 0 {
		t.Fatalf("got status=%v upper=%v lower=%v, want optimal at %v",
			res.Status, res.Upper, res.Lower, optCost)
	}
}
