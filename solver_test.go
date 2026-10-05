package quadopt

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

func mustQuad(t *testing.T, c *big.Int, g []*big.Int, q [][]*big.Int) *Quadratic {
	t.Helper()
	f, err := NewQuadratic(c, g, q)
	if err != nil {
		t.Fatalf("NewQuadratic: %v", err)
	}
	return f
}

func vecEq(a, b []*big.Int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Cmp(b[i]) != 0 {
			return false
		}
	}
	return true
}

func checkResult(t *testing.T, res *Result, wantPoint []*big.Int, wantVal int64) {
	t.Helper()
	if res.Status != Optimal {
		t.Fatalf("status = %v, want Optimal", res.Status)
	}
	if !vecEq(res.Point, wantPoint) {
		t.Fatalf("point = %v, want %v", res.Point, wantPoint)
	}
	if res.Value.Cmp(bi(wantVal)) != 0 {
		t.Fatalf("value = %v, want %v", res.Value, wantVal)
	}
	if res.LowerBound.Cmp(res.Value) != 0 {
		t.Fatalf("optimal result: lower bound %v != value %v", res.LowerBound, res.Value)
	}
}

// Negative curvature: f(x) = -x² on [-5,5]. Minima at both endpoints (-25);
// the lexicographically smallest one, -5, must win.
func TestNegativeCurvature(t *testing.T) {
	f := mustQuad(t, bi(0), bis(0), [][]*big.Int{bis(-1)})
	res, err := Solve(f, bis(-5), bis(5), nil)
	if err != nil {
		t.Fatal(err)
	}
	checkResult(t, res, bis(-5), -25)
}

// Single-point box: the only feasible point is the optimum.
func TestSinglePointBox(t *testing.T) {
	f := mustQuad(t, bi(0), bis(-10), [][]*big.Int{bis(1)}) // x² - 10x
	res, err := Solve(f, bis(3), bis(3), nil)
	if err != nil {
		t.Fatal(err)
	}
	checkResult(t, res, bis(3), -21)
}

// Unconstrained minimizer far outside the box: optimum lies on the boundary.
func TestBoundaryOptimum(t *testing.T) {
	// f(x) = (x-100)² = x² - 200x + 10000 on [0,5] -> x = 5, f = 9025.
	f := mustQuad(t, bi(10000), bis(-200), [][]*big.Int{bis(1)})
	res, err := Solve(f, bis(0), bis(5), nil)
	if err != nil {
		t.Fatal(err)
	}
	checkResult(t, res, bis(5), 9025)
}

// Indefinite 2D: f = x² - y² on [-2,2]². Minima (0,±2) with value -4;
// lexicographic tie-break picks (0,-2).
func TestIndefinite2D(t *testing.T) {
	f := mustQuad(t, bi(0), bis(0, 0),
		[][]*big.Int{bis(1, 0), bis(0, -1)})
	res, err := Solve(f, bis(-2, -2), bis(2, 2), nil)
	if err != nil {
		t.Fatal(err)
	}
	checkResult(t, res, bis(0, -2), -4)
}

// Pure cross term: f = 2xy on [-3,3]². Minima at (-3,3) and (3,-3) with
// value -18; tie-break picks (-3,3).
func TestCrossTerm(t *testing.T) {
	f := mustQuad(t, bi(0), bis(0, 0),
		[][]*big.Int{bis(0, 1), bis(1, 0)})
	res, err := Solve(f, bis(-3, -3), bis(3, 3), nil)
	if err != nil {
		t.Fatal(err)
	}
	checkResult(t, res, bis(-3, 3), -18)
}

// Constant objective: every point is optimal, the box corner lo wins.
func TestConstantTieBreak(t *testing.T) {
	f := mustQuad(t, bi(7), bis(0, 0),
		[][]*big.Int{bis(0, 0), bis(0, 0)})
	res, err := Solve(f, bis(-2, 1), bis(3, 4), nil)
	if err != nil {
		t.Fatal(err)
	}
	checkResult(t, res, bis(-2, 1), 7)
}

// Coefficients far beyond int64 must be handled exactly.
func TestBigIntegers(t *testing.T) {
	huge := new(big.Int).Lsh(big.NewInt(1), 100) // 2^100
	// f(x,y) = 2^100·(x² - 2xy + y²) - (2^100+1)·x on [-3,3]².
	c := new(big.Int)
	g := []*big.Int{new(big.Int).Neg(new(big.Int).Add(huge, big.NewInt(1))), bi(0)}
	q := [][]*big.Int{
		{new(big.Int).Set(huge), new(big.Int).Neg(huge)},
		{new(big.Int).Neg(huge), new(big.Int).Set(huge)},
	}
	f := mustQuad(t, c, g, q)
	lo, hi := bis(-3, -3), bis(3, 3)
	res, err := Solve(f, lo, hi, nil)
	if err != nil {
		t.Fatal(err)
	}
	bp, bv := BruteForce(f, lo, hi)
	if !vecEq(res.Point, bp) || res.Value.Cmp(bv) != 0 {
		t.Fatalf("got (%v, %v), brute force says (%v, %v)", res.Point, res.Value, bp, bv)
	}
}

// randQuad builds a random quadratic, usually indefinite.
func randQuad(rng *rand.Rand, n int) *Quadratic {
	c := bi(int64(rng.Intn(21) - 10))
	g := make([]*big.Int, n)
	for i := range g {
		g[i] = bi(int64(rng.Intn(21) - 10))
	}
	q := make([][]*big.Int, n)
	for i := range q {
		q[i] = make([]*big.Int, n)
	}
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			v := bi(int64(rng.Intn(19) - 9))
			q[i][j] = v
			q[j][i] = v
		}
	}
	f, err := NewQuadratic(c, g, q)
	if err != nil {
		panic(err)
	}
	return f
}

func randBox(rng *rand.Rand, n int) (lo, hi []*big.Int) {
	lo = make([]*big.Int, n)
	hi = make([]*big.Int, n)
	for i := 0; i < n; i++ {
		l := rng.Intn(9) - 4
		lo[i] = bi(int64(l))
		hi[i] = bi(int64(l + rng.Intn(5))) // range 0..4
	}
	return lo, hi
}

// Random small boxes: Solve must match full enumeration exactly, including
// the lexicographic tie-break, and its lower bound must be valid.
func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(4)
		f := randQuad(rng, n)
		lo, hi := randBox(rng, n)
		res, err := Solve(f, lo, hi, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != Optimal {
			t.Fatalf("trial %d: status %v", trial, res.Status)
		}
		bp, bv := BruteForce(f, lo, hi)
		if res.Value.Cmp(bv) != 0 || !vecEq(res.Point, bp) {
			t.Fatalf("trial %d: got (%v, %v), brute force says (%v, %v)",
				trial, res.Point, res.Value, bp, bv)
		}
		if res.LowerBound.Cmp(bv) > 0 {
			t.Fatalf("trial %d: lower bound %v exceeds optimum %v", trial, res.LowerBound, bv)
		}
	}
}

// Every pruned node's bound must be a valid lower bound for its sub-box:
// recompute the true minimum of each pruned sub-box by enumeration.
func TestPruneBoundsAreLegal(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	type prune struct {
		lo, hi []*big.Int
		lb     *big.Int
	}
	for trial := 0; trial < 100; trial++ {
		n := 1 + rng.Intn(3)
		f := randQuad(rng, n)
		lo, hi := randBox(rng, n)
		var prunes []prune
		opts := &Options{OnPrune: func(lo, hi []*big.Int, lb *big.Int) {
			prunes = append(prunes, prune{cloneVec(lo), cloneVec(hi), new(big.Int).Set(lb)})
		}}
		if _, err := Solve(f, lo, hi, opts); err != nil {
			t.Fatal(err)
		}
		for k, p := range prunes {
			_, subMin := BruteForce(f, p.lo, p.hi)
			if p.lb.Cmp(subMin) > 0 {
				t.Fatalf("trial %d prune %d: bound %v exceeds sub-box minimum %v over [%v, %v]",
					trial, k, p.lb, subMin, p.lo, p.hi)
			}
		}
	}
}

// The raw interval bound must be valid on random boxes directly.
func TestIntervalLowerBoundValid(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(4)
		f := randQuad(rng, n)
		lo, hi := randBox(rng, n)
		_, bv := BruteForce(f, lo, hi)
		if lb := f.lowerBound(lo, hi); lb.Cmp(bv) > 0 {
			t.Fatalf("trial %d: lower bound %v > true minimum %v", trial, lb, bv)
		}
	}
}

// A tiny budget must interrupt the search with a valid bound sandwich.
func TestBudgetInterruption(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 50; trial++ {
		n := 2 + rng.Intn(2)
		f := randQuad(rng, n)
		lo, hi := randBox(rng, n)
		res, err := Solve(f, lo, hi, &Options{MaxNodes: 1 + rng.Intn(3)})
		if err != nil {
			t.Fatal(err)
		}
		_, opt := BruteForce(f, lo, hi)
		if res.Status == BudgetExhausted {
			if res.LowerBound.Cmp(opt) > 0 {
				t.Fatalf("trial %d: lower bound %v exceeds optimum %v", trial, res.LowerBound, opt)
			}
			if res.Value.Cmp(opt) < 0 {
				t.Fatalf("trial %d: incumbent %v below optimum %v (impossible)", trial, res.Value, opt)
			}
		}
		// Incumbent must always be a real point of the box.
		if got := f.Eval(res.Point); got.Cmp(res.Value) != 0 {
			t.Fatalf("trial %d: f(Point) = %v != Value %v", trial, got, res.Value)
		}
		for i := range res.Point {
			if res.Point[i].Cmp(lo[i]) < 0 || res.Point[i].Cmp(hi[i]) > 0 {
				t.Fatalf("trial %d: point %v outside box", trial, res.Point)
			}
		}
	}
}

// With enough budget the interrupted search converges to the exact optimum.
func TestBudgetConvergence(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	f := randQuad(rng, 3)
	lo, hi := bis(-4, -4, -4), bis(4, 4, 4)
	_, opt := BruteForce(f, lo, hi)
	for budget := 1; budget <= 100000; budget *= 4 {
		res, err := Solve(f, lo, hi, &Options{MaxNodes: budget})
		if err != nil {
			t.Fatal(err)
		}
		if res.Status == Optimal {
			if res.Value.Cmp(opt) != 0 {
				t.Fatalf("budget %d: optimal value %v != %v", budget, res.Value, opt)
			}
			return
		}
	}
	t.Fatal("did not converge within budget")
}

func TestInvalidInput(t *testing.T) {
	if _, err := NewQuadratic(bi(0), bis(0), [][]*big.Int{bis(1, 0)}); err == nil {
		t.Fatal("expected dimension error")
	}
	if _, err := NewQuadratic(bi(0), bis(0, 0),
		[][]*big.Int{bis(1, 2), bis(3, 1)}); err == nil {
		t.Fatal("expected symmetry error")
	}
	f := mustQuad(t, bi(0), bis(0), [][]*big.Int{bis(1)})
	if _, err := Solve(f, bis(2), bis(1), nil); err == nil {
		t.Fatal("expected empty-box error")
	}
}
