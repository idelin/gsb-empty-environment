package polysimp

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
)

func randomPolyline(r *rand.Rand, n int) []Point {
	p := make([]Point, n)
	for i := range p {
		// Integer coordinates create frequent ties, exercising the
		// lexicographic tie-break.
		p[i] = Point{X: float64(r.Intn(8)), Y: float64(r.Intn(8))}
	}
	return p
}

func checkEvidence(t *testing.T, p []Point, res Result, eps float64) {
	t.Helper()
	if res.Indices[0] != 0 || res.Indices[len(res.Indices)-1] != len(p)-1 {
		t.Fatalf("endpoints not preserved: %v", res.Indices)
	}
	for i := 1; i < len(res.Indices); i++ {
		if res.Indices[i] <= res.Indices[i-1] {
			t.Fatalf("indices not increasing: %v", res.Indices)
		}
	}
	if res.Error > eps {
		t.Fatalf("reported error %v exceeds eps %v", res.Error, eps)
	}
	maxd, err := CheckMatching(p, res.Points, res.Matching, eps)
	if err != nil {
		t.Fatalf("invalid matching evidence: %v", err)
	}
	if math.Abs(maxd-res.Error) > 1e-9 {
		t.Fatalf("matching max distance %v != reported error %v", maxd, res.Error)
	}
	// Reported error must equal the true discrete Fréchet distance.
	d, _ := DiscreteFrechet(p, res.Points)
	if math.Abs(d-res.Error) > 1e-9 {
		t.Fatalf("reported error %v != true distance %v", res.Error, d)
	}
}

// Small polylines: Simplify must agree exactly with subsequence
// enumeration judged by the true discrete Fréchet distance.
func TestCrossCheckWithEnumeration(t *testing.T) {
	epsilons := []float64{0, 0.5, 1, 1.5, 3, 100}
	for seed := int64(0); seed < 60; seed++ {
		r := rand.New(rand.NewSource(seed))
		n := 2 + r.Intn(10) // 2..11 points
		p := randomPolyline(r, n)
		for _, eps := range epsilons {
			got, err := Simplify(p, eps, Options{})
			if err != nil {
				t.Fatalf("seed %d eps %v: %v", seed, eps, err)
			}
			want, err := BruteForce(p, eps)
			if err != nil {
				t.Fatalf("seed %d eps %v: brute force: %v", seed, eps, err)
			}
			if !got.Optimal {
				t.Fatalf("seed %d eps %v: unlimited budget but not optimal", seed, eps)
			}
			if !reflect.DeepEqual(got.Indices, want.Indices) {
				t.Fatalf("seed %d eps %v: indices %v, enumeration says %v",
					seed, eps, got.Indices, want.Indices)
			}
			if math.Abs(got.Error-want.Error) > 1e-9 {
				t.Fatalf("seed %d eps %v: error %v, enumeration says %v",
					seed, eps, got.Error, want.Error)
			}
			checkEvidence(t, p, got, eps)
		}
	}
}

// A shortcut whose perpendicular deviation is tiny can still have a large
// discrete Fréchet error. The library must not accept it.
func TestNoPerpendicularDistanceProxy(t *testing.T) {
	p := []Point{{0, 0}, {5, 0.5}, {10, 0}}
	// Perpendicular distance of the middle point to segment (0,0)-(10,0)
	// is 0.5, well under eps; the discrete Fréchet distance is 5.
	eps := 1.0
	res, err := Simplify(p, eps, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Indices, []int{0, 1, 2}) {
		t.Fatalf("shortcut wrongly accepted (perpendicular proxy?): %v", res.Indices)
	}
	d, _ := DiscreteFrechet(p, []Point{p[0], p[2]})
	if d <= eps {
		t.Fatalf("test premise broken: shortcut error %v <= %v", d, eps)
	}
	if perp := 0.5; perp >= eps {
		t.Fatalf("test premise broken: perpendicular distance %v >= %v", perp, eps)
	}
}

func TestLexicographicTieBreak(t *testing.T) {
	// Both {0,1,3} and {0,2,3} are feasible with 3 points; {0,3} is not.
	p := []Point{{0, 0}, {1, 0}, {2, 0}, {4, 0}}
	res, err := Simplify(p, 1.0, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Indices, []int{0, 1, 3}) {
		t.Fatalf("got %v, want lexicographically smallest [0 1 3]", res.Indices)
	}
}

func TestBudgetExhaustionGivesFeasibleUpperBound(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	p := randomPolyline(r, 40)
	eps := 2.0
	optimal, err := Simplify(p, eps, Options{})
	if err != nil {
		t.Fatal(err)
	}
	bounded, err := Simplify(p, eps, Options{Budget: 5})
	if err != nil {
		t.Fatal(err)
	}
	if bounded.Optimal && bounded.BudgetExhausted {
		t.Fatal("inconsistent flags")
	}
	// Feasible: endpoints kept, error bound holds, evidence valid.
	checkEvidence(t, p, bounded, eps)
	// Upper bound: never fewer points than the optimum.
	if len(bounded.Indices) < len(optimal.Indices) {
		t.Fatalf("budget result %d points < optimal %d", len(bounded.Indices), len(optimal.Indices))
	}
	if _, err := Verify(p, bounded.Indices, eps); err != nil {
		t.Fatalf("Verify rejected budget result: %v", err)
	}
}

func TestZeroBudgetStillFeasible(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	p := randomPolyline(r, 30)
	res, err := Simplify(p, 1.0, Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Optimal {
		t.Fatal("budget 1 must not claim optimality")
	}
	checkEvidence(t, p, res, 1.0)
}

func TestDiscreteFrechetKnownValue(t *testing.T) {
	p := []Point{{0, 0}, {1, 0}, {2, 0}}
	q := []Point{{0, 0}, {2, 0}}
	d, matching := DiscreteFrechet(p, q)
	if d != 1 {
		t.Fatalf("got %v, want 1", d)
	}
	if _, err := CheckMatching(p, q, matching, 1); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejectsBadInput(t *testing.T) {
	p := []Point{{0, 0}, {1, 0}, {2, 0}}
	if _, err := Verify(p, []int{0, 1}, 1); err == nil {
		t.Fatal("expected endpoint error")
	}
	if _, err := Verify(p, []int{0, 2}, 0.5); err == nil {
		t.Fatal("expected epsilon violation")
	}
	if _, err := Simplify(p, -1, Options{}); err == nil {
		t.Fatal("expected negative epsilon error")
	}
}
