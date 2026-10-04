package diskcover

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

func solve(t *testing.T, pts []Point, ws []int64, r string) (int64, AlgPoint) {
	t.Helper()
	w, c, err := Solve(pts, ws, mustRat(t, r))
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return w, c
}

// Radius 0: the disk is a single point; coincident points each count.
func TestRadiusZero(t *testing.T) {
	pts := []Point{{0, 0}, {0, 0}, {5, 5}, {1, 1}}
	ws := []int64{1, 2, 4, 3}
	w, c := solve(t, pts, ws, "0")
	if w != 4 {
		t.Fatalf("weight = %d, want 4", w)
	}
	if c.X.A.Cmp(ratInt64(5)) != 0 || c.Y.A.Cmp(ratInt64(5)) != 0 || c.X.B.Sign() != 0 || c.Y.B.Sign() != 0 {
		t.Fatalf("center = %v, want (5, 5)", c)
	}
}

// Points exactly one diameter apart must both be covered by the midpoint.
func TestExactlyDiameterApart(t *testing.T) {
	pts := []Point{{0, 0}, {6, 8}, {100, 100}}
	ws := []int64{3, 7, 1}
	w, c := solve(t, pts, ws, "5") // distance is 10 = 2r
	if w != 10 {
		t.Fatalf("weight = %d, want 10", w)
	}
	if c.X.A.Cmp(ratInt64(3)) != 0 || c.Y.A.Cmp(ratInt64(4)) != 0 || c.X.B.Sign() != 0 || c.Y.B.Sign() != 0 {
		t.Fatalf("center = %v, want (3, 4)", c)
	}
	if got := CoveredWeight(c, pts, ws, mustRat(t, "5")); got != 10 {
		t.Fatalf("recomputed weight = %d, want 10", got)
	}
}

// Several disjoint optimal placements: the answer must be deterministic and
// genuinely optimal (recomputed coverage matches the reported weight).
func TestTiedOptima(t *testing.T) {
	pts := []Point{{0, 0}, {10, 0}, {0, 10}, {10, 10}}
	ws := []int64{1, 1, 1, 1}
	r := mustRat(t, "1")
	w1, c1 := solve(t, pts, ws, "1")
	w2, c2 := solve(t, pts, ws, "1")
	if w1 != 1 {
		t.Fatalf("weight = %d, want 1", w1)
	}
	if w1 != w2 || c1.String() != c2.String() {
		t.Fatalf("non-deterministic: (%d,%v) vs (%d,%v)", w1, c1, w2, c2)
	}
	if got := CoveredWeight(c1, pts, ws, r); got != w1 {
		t.Fatalf("center %v recomputes to %d, reported %d", c1, got, w1)
	}
}

// A point exactly on the boundary counts; shrinking the radius by 1/1000
// must exclude it. Checked directly against a rational center, so no pair
// candidate can mask the comparison.
func TestExactBoundaryRational(t *testing.T) {
	pts := []Point{{0, 0}, {3, 4}}
	ws := []int64{5, 5}
	c := rationalCenter(new(big.Rat), new(big.Rat)) // center at (0,0)
	if got := CoveredWeight(c, pts, ws, mustRat(t, "5")); got != 10 {
		t.Fatalf("r=5: covered = %d, want 10", got)
	}
	if got := CoveredWeight(c, pts, ws, mustRat(t, "4999/1000")); got != 5 {
		t.Fatalf("r=4999/1000: covered = %d, want 5", got)
	}
	if w, _ := solve(t, pts, ws, "5"); w != 10 {
		t.Fatalf("solve r=5: weight = %d, want 10", w)
	}
	if w, _ := solve(t, pts, ws, "2"); w != 5 {
		t.Fatalf("solve r=2: weight = %d, want 5", w)
	}
}

// Optimal center is irrational: points (0,0),(3,0) with r=2 force boundary
// centers (3/2, +-sqrt(7)/2), stored as (3/2, +-1/6*sqrt(63)).
func TestIrrationalCenter(t *testing.T) {
	pts := []Point{{0, 0}, {3, 0}, {50, 50}}
	ws := []int64{1, 1, 1}
	r := mustRat(t, "2")
	w, c := solve(t, pts, ws, "2")
	if w != 2 {
		t.Fatalf("weight = %d, want 2", w)
	}
	if c.D.Cmp(big.NewInt(63)) != 0 {
		t.Fatalf("D = %v, want 63 (center %v)", c.D, c)
	}
	if c.X.A.Cmp(big.NewRat(3, 2)) != 0 || c.X.B.Sign() != 0 || c.Y.A.Sign() != 0 {
		t.Fatalf("unexpected center %v", c)
	}
	absB := new(big.Rat).Abs(c.Y.B)
	if absB.Cmp(big.NewRat(1, 6)) != 0 {
		t.Fatalf("|Y.B| = %v, want 1/6", absB)
	}
	if got := CoveredWeight(c, pts, ws, r); got != 2 {
		t.Fatalf("recomputed weight = %d, want 2", got)
	}
}

// Same-coordinate points are weighted separately at every candidate.
func TestCoincidentPointsCountedSeparately(t *testing.T) {
	pts := []Point{{2, 2}, {2, 2}, {2, 2}, {9, 9}}
	ws := []int64{1, 1, 1, 2}
	w, _ := solve(t, pts, ws, "0")
	if w != 3 {
		t.Fatalf("weight = %d, want 3", w)
	}
}

// Best disk covers three points: two exactly a diameter apart on the
// boundary, one strictly inside.
func TestThreePointsTwoOnBoundary(t *testing.T) {
	pts := []Point{{0, 0}, {4, 0}, {2, 1}, {10, 10}}
	ws := []int64{2, 3, 4, 1}
	w, c := solve(t, pts, ws, "2")
	if w != 9 {
		t.Fatalf("weight = %d, want 9", w)
	}
	if got := CoveredWeight(c, pts, ws, mustRat(t, "2")); got != 9 {
		t.Fatalf("recomputed weight = %d, want 9", got)
	}
}

func TestEdgeCases(t *testing.T) {
	// Empty input.
	w, _, err := Solve(nil, nil, mustRat(t, "1"))
	if err != nil || w != 0 {
		t.Fatalf("empty: w=%d err=%v", w, err)
	}
	// Single point, zero radius.
	pts := []Point{{7, -3}}
	ws := []int64{42}
	w, c := solve(t, pts, ws, "0")
	if w != 42 || c.X.A.Cmp(ratInt64(7)) != 0 || c.Y.A.Cmp(ratInt64(-3)) != 0 {
		t.Fatalf("single: w=%d c=%v", w, c)
	}
	// Errors.
	if _, _, err := Solve(pts, []int64{1, 2}, mustRat(t, "1")); err != ErrLengthMismatch {
		t.Fatalf("length mismatch: %v", err)
	}
	if _, _, err := Solve(pts, ws, mustRat(t, "-1")); err != ErrNegativeRadius {
		t.Fatalf("negative radius: %v", err)
	}
	if _, _, err := Solve(pts, []int64{-5}, mustRat(t, "1")); err != ErrNegativeWeight {
		t.Fatalf("negative weight: %v", err)
	}
}
