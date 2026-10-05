package frsimp

import (
	"math/rand"
	"testing"
)

func subsequence(points []Point, indices []int) []Point {
	sub := make([]Point, len(indices))
	for t, i := range indices {
		sub[t] = points[i]
	}
	return sub
}

func checkFeasible(t *testing.T, points []Point, eps float64, metric Metric, res Result) {
	t.Helper()
	if res.Indices[0] != 0 || res.Indices[len(res.Indices)-1] != len(points)-1 {
		t.Fatalf("endpoints not kept: %v", res.Indices)
	}
	if err := CheckMatching(points, res.Indices, res.Matching, eps, metric); err != nil {
		t.Fatalf("invalid matching evidence: %v", err)
	}
	df, _ := DiscreteFrechet(points, subsequence(points, res.Indices), metric)
	if df > eps {
		t.Fatalf("discrete Frechet distance %g exceeds epsilon %g", df, eps)
	}
	if res.Distance > eps {
		t.Fatalf("reported distance %g exceeds epsilon %g", res.Distance, eps)
	}
}

func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 400; iter++ {
		n := 2 + rng.Intn(9) // 2..10 vertices
		dim := 1 + rng.Intn(3)
		points := make([]Point, n)
		for i := range points {
			p := make(Point, dim)
			for d := range p {
				p[d] = rng.NormFloat64() * 3
			}
			points[i] = p
		}
		eps := rng.Float64() * 4
		res, err := Verify(points, Options{Epsilon: eps})
		if err != nil {
			t.Fatalf("iter %d n=%d eps=%g: %v", iter, n, eps, err)
		}
		checkFeasible(t, points, eps, nil, res)
		if !res.Optimal {
			t.Fatalf("iter %d: unbounded search must be optimal", iter)
		}
	}
}

func TestRandomAgainstBruteForceManhattan(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 200; iter++ {
		n := 2 + rng.Intn(8)
		points := make([]Point, n)
		for i := range points {
			points[i] = Point{rng.NormFloat64() * 2, rng.NormFloat64() * 2}
		}
		eps := rng.Float64() * 3
		res, err := Verify(points, Options{Epsilon: eps, Metric: Manhattan})
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		checkFeasible(t, points, eps, Manhattan, res)
	}
}

func TestLexicographicTieBreak(t *testing.T) {
	// Collinear points 0..5; eps=1.5 admits the 3-point optima
	// {0,2,5} and {0,3,5}; the lexicographically smaller must win.
	points := []Point{{0, 0}, {1, 0}, {2, 0}, {3, 0}, {4, 0}, {5, 0}}
	res, err := Simplify(points, Options{Epsilon: 1.5})
	if err != nil {
		t.Fatal(err)
	}
	want := []int{0, 2, 5}
	if len(res.Indices) != len(want) {
		t.Fatalf("got %v, want %v", res.Indices, want)
	}
	for i := range want {
		if res.Indices[i] != want[i] {
			t.Fatalf("got %v, want %v", res.Indices, want)
		}
	}
	checkFeasible(t, points, 1.5, nil, res)
}

func TestEndpointsOnlyWhenPossible(t *testing.T) {
	points := []Point{{0, 0}, {1, 0}, {2, 0}, {3, 0}}
	res, err := Simplify(points, Options{Epsilon: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Indices) != 2 || res.Indices[0] != 0 || res.Indices[1] != 3 {
		t.Fatalf("got %v, want [0 3]", res.Indices)
	}
	checkFeasible(t, points, 3, nil, res)
}

// TestOrderMatters ensures the criterion is the discrete Frechet distance
// and not a perpendicular-distance threshold: every interior point lies
// within perpendicular distance 0.5 of the segment from first to last, yet
// any monotone coupling to the two endpoints must match (5,0) to an endpoint
// at distance >= 5.
func TestOrderMatters(t *testing.T) {
	points := []Point{{0, 0}, {5, 0}, {5, 0.5}, {0, 0.5}}
	res, err := Simplify(points, Options{Epsilon: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Indices) <= 2 {
		t.Fatalf("perpendicular-distance shortcut must not satisfy the Frechet guarantee: %v", res.Indices)
	}
	want := []int{0, 1, 3}
	for i := range want {
		if res.Indices[i] != want[i] {
			t.Fatalf("got %v, want %v", res.Indices, want)
		}
	}
	checkFeasible(t, points, 1, nil, res)
}

func TestBudgetExhaustedGivesFeasibleUpperBound(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	points := make([]Point, 12)
	for i := range points {
		points[i] = Point{float64(i), rng.NormFloat64()}
	}
	eps := 0.5
	res, err := Simplify(points, Options{Epsilon: eps, Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Optimal {
		t.Fatal("budget 1 must not certify optimality")
	}
	checkFeasible(t, points, eps, nil, res)

	// The upper bound must be at least the optimum.
	opt, err := Simplify(points, Options{Epsilon: eps})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Indices) < len(opt.Indices) {
		t.Fatalf("fallback used fewer points (%d) than optimal (%d): impossible",
			len(res.Indices), len(opt.Indices))
	}
}

func TestBudgetGenerousMatchesUnbounded(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	points := make([]Point, 10)
	for i := range points {
		points[i] = Point{rng.NormFloat64() * 2, rng.NormFloat64() * 2}
	}
	eps := 1.0
	bounded, err := Simplify(points, Options{Epsilon: eps, Budget: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	unbounded, err := Simplify(points, Options{Epsilon: eps})
	if err != nil {
		t.Fatal(err)
	}
	if !bounded.Optimal || len(bounded.Indices) != len(unbounded.Indices) {
		t.Fatalf("generous budget changed result: %v vs %v", bounded.Indices, unbounded.Indices)
	}
}

func TestDiscreteFrechetKnownValue(t *testing.T) {
	a := []Point{{0, 0}, {1, 0}, {2, 0}}
	b := []Point{{0, 0}, {2, 0}}
	d, matching := DiscreteFrechet(a, b, nil)
	if d != 1 {
		t.Fatalf("got %g, want 1", d)
	}
	if matching[0] != (MatchPair{0, 0}) || matching[len(matching)-1] != (MatchPair{2, 1}) {
		t.Fatalf("bad coupling endpoints: %v", matching)
	}
}

func TestDegenerateInputs(t *testing.T) {
	if _, err := Simplify(nil, Options{Epsilon: 1}); err == nil {
		t.Fatal("empty polyline must error")
	}
	if _, err := Simplify([]Point{{0, 0}}, Options{Epsilon: -1}); err == nil {
		t.Fatal("negative epsilon must error")
	}
	res, err := Simplify([]Point{{4, 5}}, Options{Epsilon: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Indices) != 1 || res.Indices[0] != 0 || !res.Optimal {
		t.Fatalf("single point: %+v", res)
	}
	res, err = Simplify([]Point{{0, 0}, {10, 0}}, Options{Epsilon: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Indices) != 2 {
		t.Fatalf("two points: %v", res.Indices)
	}
}

func TestRandomWithDuplicatePoints(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for iter := 0; iter < 300; iter++ {
		n := 2 + rng.Intn(9)
		// Few distinct locations: many zero-distance pairs, which exercise
		// couplings that match points outside their "own" block.
		pool := make([]Point, 1+rng.Intn(3))
		for i := range pool {
			pool[i] = Point{rng.NormFloat64() * 2, rng.NormFloat64() * 2}
		}
		points := make([]Point, n)
		for i := range points {
			points[i] = pool[rng.Intn(len(pool))]
		}
		eps := rng.Float64() * 2
		res, err := Verify(points, Options{Epsilon: eps})
		if err != nil {
			t.Fatalf("iter %d n=%d eps=%g: %v", iter, n, eps, err)
		}
		checkFeasible(t, points, eps, nil, res)
	}
}

func TestBudgetLadderAlwaysFeasible(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	points := make([]Point, 14)
	for i := range points {
		points[i] = Point{float64(i) * 0.7, rng.NormFloat64()}
	}
	eps := 0.8
	opt, err := Simplify(points, Options{Epsilon: eps})
	if err != nil {
		t.Fatal(err)
	}
	for budget := int64(0); budget <= 40; budget++ {
		res, err := Simplify(points, Options{Epsilon: eps, Budget: budget})
		if err != nil {
			t.Fatalf("budget %d: %v", budget, err)
		}
		checkFeasible(t, points, eps, nil, res)
		if res.Optimal && len(res.Indices) != len(opt.Indices) {
			t.Fatalf("budget %d: certified optimal with wrong count %d != %d",
				budget, len(res.Indices), len(opt.Indices))
		}
		if len(res.Indices) < len(opt.Indices) {
			t.Fatalf("budget %d: fewer points than optimal", budget)
		}
	}
}

func TestZeroEpsilon(t *testing.T) {
	points := []Point{{1, 1}, {2, 2}, {2, 2}, {1, 1}}
	res, err := Simplify(points, Options{Epsilon: 0})
	if err != nil {
		t.Fatal(err)
	}
	// Blocks must be contiguous, so index 2 (equal to index 1) can be
	// absorbed into index 1's block, but nothing else. Both {0,1,3} and
	// {0,2,3} are optimal; the lexicographically smaller must win.
	want := []int{0, 1, 3}
	if len(res.Indices) != len(want) {
		t.Fatalf("got %v, want %v", res.Indices, want)
	}
	for i := range want {
		if res.Indices[i] != want[i] {
			t.Fatalf("got %v, want %v", res.Indices, want)
		}
	}
	checkFeasible(t, points, 0, nil, res)
	if _, err := Verify(points, Options{Epsilon: 0}); err != nil {
		t.Fatal(err)
	}
}
