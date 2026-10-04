package circlecover

import (
	"math/big"
	"testing"
)

func mustRat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("bad rational %q", s)
	}
	return r
}

func checkWeight(t *testing.T, res Result, want int64) {
	t.Helper()
	if res.Weight.Cmp(big.NewInt(want)) != 0 {
		t.Fatalf("weight = %s, want %d (center %s)", res.Weight, want, res.Center)
	}
}

func checkAlg(t *testing.T, got Alg, wantA, wantB, wantS string) {
	t.Helper()
	a, _ := new(big.Rat).SetString(wantA)
	b, _ := new(big.Rat).SetString(wantB)
	s, _ := new(big.Rat).SetString(wantS)
	if got.A.Cmp(a) != 0 || got.B.Cmp(b) != 0 || got.S.Cmp(s) != 0 {
		t.Fatalf("alg = %s, want %s + %s*sqrt(%s)", got, wantA, wantB, wantS)
	}
}

// Radius 0: a disk covers exactly the points at its own location, and
// duplicate coordinates must add their weights.
func TestRadiusZero(t *testing.T) {
	pts := []Point{{0, 0}, {0, 0}, {5, 5}}
	w := []int64{2, 3, 4}
	res, err := Solve(pts, w, big.NewRat(0, 1))
	if err != nil {
		t.Fatal(err)
	}
	checkWeight(t, res, 5)
	checkAlg(t, res.Center.X, "0", "0", "0")
	checkAlg(t, res.Center.Y, "0", "0", "0")
	if got := CoveredWeight(pts, w, res.Center, big.NewRat(0, 1)); got.Cmp(big.NewInt(5)) != 0 {
		t.Fatalf("recomputed weight = %s, want 5", got)
	}

	// A single heavy point beats a cluster of lighter duplicates.
	w2 := []int64{2, 3, 10}
	res2, err := Solve(pts, w2, big.NewRat(0, 1))
	if err != nil {
		t.Fatal(err)
	}
	checkWeight(t, res2, 10)
	checkAlg(t, res2.Center.X, "5", "0", "0")
	checkAlg(t, res2.Center.Y, "5", "0", "0")
}

// Points at distance exactly 2r: the unique boundary center is the
// midpoint (s = 0, purely rational).
func TestExactlyDiameterApart(t *testing.T) {
	pts := []Point{{0, 0}, {10, 0}}
	w := []int64{1, 1}
	res, err := Solve(pts, w, big.NewRat(5, 1))
	if err != nil {
		t.Fatal(err)
	}
	checkWeight(t, res, 2)
	checkAlg(t, res.Center.X, "5", "0", "0")
	checkAlg(t, res.Center.Y, "0", "0", "0")

	// One unit farther apart: no disk of radius 5 covers both.
	pts2 := []Point{{0, 0}, {11, 0}}
	res2, err := Solve(pts2, w, big.NewRat(5, 1))
	if err != nil {
		t.Fatal(err)
	}
	checkWeight(t, res2, 1)
}

// Several disjoint optimal disks with equal weight: the solver must
// return one of them, and the returned center must recompute to the
// reported weight.
func TestTiedOptima(t *testing.T) {
	pts := []Point{{0, 0}, {100, 0}, {0, 100}, {100, 100}}
	w := []int64{7, 7, 7, 7}
	r := big.NewRat(1, 1)
	res, err := Solve(pts, w, r)
	if err != nil {
		t.Fatal(err)
	}
	checkWeight(t, res, 7)
	if got := CoveredWeight(pts, w, res.Center, r); got.Cmp(res.Weight) != 0 {
		t.Fatalf("recomputed weight %s != reported %s", got, res.Weight)
	}

	// Two disjoint pairs, each coverable together, equal weights.
	pts2 := []Point{{0, 0}, {2, 0}, {50, 0}, {52, 0}}
	w2 := []int64{3, 4, 3, 4}
	res2, err := Solve(pts2, w2, big.NewRat(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	checkWeight(t, res2, 7)
}

// Closed disk: a point exactly on the boundary (a 3-4-5 configuration)
// must count.
func TestBoundaryPointIncluded(t *testing.T) {
	pts := []Point{{0, 0}, {6, 0}, {3, 4}}
	w := []int64{1, 1, 1}
	r := big.NewRat(5, 1)
	res, err := Solve(pts, w, r)
	if err != nil {
		t.Fatal(err)
	}
	checkWeight(t, res, 3)
	for _, p := range pts {
		if !Covered(res.Center, p, r) {
			t.Fatalf("point %v should be covered by %s", p, res.Center)
		}
	}
}

// The optimal center is genuinely algebraic: (3, 0) + (0, 6)*sqrt(4/9),
// i.e. (3, 4), represented exactly as a + b*sqrt(s).
func TestAlgebraicCenterForm(t *testing.T) {
	pts := []Point{{0, 0}, {6, 0}}
	w := []int64{1, 1}
	r := big.NewRat(5, 1)
	res, err := Solve(pts, w, r)
	if err != nil {
		t.Fatal(err)
	}
	checkWeight(t, res, 2)
	checkAlg(t, res.Center.X, "3", "0", "0")
	checkAlg(t, res.Center.Y, "0", "6", "4/9")
	// Squared distance to either point is exactly 25 = r^2.
	d2 := distSquared(res.Center, Point{0, 0})
	if d2.A.Cmp(big.NewRat(25, 1)) != 0 || d2.B.Sign() != 0 {
		t.Fatalf("dist^2 = %s, want 25", d2)
	}
}

// No fixed floating-point tolerance: covering both (0,0) and (1,1)
// needs r >= d/2 = sqrt(2)/2. With r just below sqrt(2)/2 the pair is
// not jointly coverable; with r just above, it is. The deciding gap is
// ~3e-13, far below typical float epsilons.
func TestExactComparisonBeyondFloatTolerance(t *testing.T) {
	pts := []Point{{0, 0}, {1, 1}}
	w := []int64{1, 100}
	mid := Center{
		X: Alg{A: big.NewRat(1, 2), B: new(big.Rat), S: new(big.Rat)},
		Y: Alg{A: big.NewRat(1, 2), B: new(big.Rat), S: new(big.Rat)},
	}

	rBelow := mustRat(t, "707106781186/1000000000000") // < sqrt(2)/2
	if Covered(mid, Point{0, 0}, rBelow) {
		t.Fatal("midpoint must be outside: r^2 < 1/2")
	}
	res, err := Solve(pts, w, rBelow)
	if err != nil {
		t.Fatal(err)
	}
	checkWeight(t, res, 100)

	rAbove := mustRat(t, "7071067811866/10000000000000") // > sqrt(2)/2
	if !Covered(mid, Point{0, 0}, rAbove) {
		t.Fatal("midpoint must be inside: r^2 > 1/2")
	}
	res2, err := Solve(pts, w, rAbove)
	if err != nil {
		t.Fatal(err)
	}
	checkWeight(t, res2, 101)
}

// The midpoint of a close pair is not a candidate unless d == 2r, but
// the boundary centers must still capture the optimum.
func TestPairBoundaryCenters(t *testing.T) {
	pts := []Point{{0, 0}, {6, 0}, {3, 1}}
	w := []int64{5, 5, 1}
	r := big.NewRat(4, 1)
	res, err := Solve(pts, w, r)
	if err != nil {
		t.Fatal(err)
	}
	checkWeight(t, res, 11)
	if got := CoveredWeight(pts, w, res.Center, r); got.Cmp(big.NewInt(11)) != 0 {
		t.Fatalf("recomputed weight = %s, want 11", got)
	}
}

func TestInvalidInput(t *testing.T) {
	if _, err := Solve([]Point{{0, 0}}, nil, big.NewRat(1, 1)); err == nil {
		t.Fatal("expected error for length mismatch")
	}
	if _, err := Solve([]Point{{0, 0}}, []int64{-1}, big.NewRat(1, 1)); err == nil {
		t.Fatal("expected error for negative weight")
	}
	if _, err := Solve([]Point{{0, 0}}, []int64{1}, big.NewRat(-1, 1)); err == nil {
		t.Fatal("expected error for negative radius")
	}
}

func TestEmpty(t *testing.T) {
	res, err := Solve(nil, nil, big.NewRat(3, 1))
	if err != nil {
		t.Fatal(err)
	}
	checkWeight(t, res, 0)
}

// Rational radius that is not an integer, with a fractional boundary
// case: points (0,0) and (3,0), r = 3/2 -> midpoint (3/2, 0).
func TestRationalRadius(t *testing.T) {
	pts := []Point{{0, 0}, {3, 0}}
	w := []int64{2, 2}
	res, err := Solve(pts, w, big.NewRat(3, 2))
	if err != nil {
		t.Fatal(err)
	}
	checkWeight(t, res, 4)
	checkAlg(t, res.Center.X, "3/2", "0", "0")
	checkAlg(t, res.Center.Y, "0", "0", "0")
}
