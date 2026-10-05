package segsweep

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func canonical(r Result) string {
	var sb strings.Builder
	sb.WriteString("points:\n")
	for _, p := range r.Points {
		fmt.Fprintf(&sb, "  %s %v\n", p.P, p.IDs)
	}
	sb.WriteString("overlaps:\n")
	for _, o := range r.Overlaps {
		fmt.Fprintf(&sb, "  %s -> %s %v\n", o.A, o.B, o.IDs)
	}
	return sb.String()
}

func checkResult(t *testing.T, name string, segs []IntSegment, want string) {
	t.Helper()
	got := canonical(Sweep(segs))
	if got != want {
		t.Errorf("%s: Sweep mismatch\ngot:\n%s\nwant:\n%s", name, got, want)
	}
	// Every handcrafted case must also agree with the reference.
	if bf := canonical(BruteForce(segs)); bf != got {
		t.Errorf("%s: Sweep vs BruteForce mismatch\nsweep:\n%s\nbrute:\n%s", name, got, bf)
	}
}

func TestCrossVerticalHorizontal(t *testing.T) {
	checkResult(t, "cross", []IntSegment{
		{ID: 1, X1: 0, Y1: 0, X2: 10, Y2: 0},
		{ID: 2, X1: 5, Y1: -5, X2: 5, Y2: 5},
	}, `points:
  (5,0) [1 2]
overlaps:
`)
}

func TestConcurrentPoint(t *testing.T) {
	// Three segments through one point: listed once, all IDs kept.
	checkResult(t, "concurrent", []IntSegment{
		{ID: 1, X1: 0, Y1: 0, X2: 10, Y2: 10},
		{ID: 2, X1: 0, Y1: 10, X2: 10, Y2: 0},
		{ID: 3, X1: 0, Y1: 5, X2: 10, Y2: 5},
		{ID: 4, X1: 5, Y1: -3, X2: 5, Y2: 8}, // vertical through (5,5)
	}, `points:
  (5,5) [1 2 3 4]
overlaps:
`)
}

func TestEndpointTouch(t *testing.T) {
	checkResult(t, "endpoint touch", []IntSegment{
		{ID: 1, X1: 0, Y1: 0, X2: 3, Y2: 0},
		{ID: 2, X1: 3, Y1: 0, X2: 5, Y2: 2},
	}, `points:
  (3,0) [1 2]
overlaps:
`)
}

func TestCollinearEndpointTouch(t *testing.T) {
	// Same supporting line, touching at one endpoint: a point, not an
	// overlap.
	checkResult(t, "collinear touch", []IntSegment{
		{ID: 1, X1: 0, Y1: 0, X2: 3, Y2: 0},
		{ID: 2, X1: 3, Y1: 0, X2: 5, Y2: 0},
	}, `points:
  (3,0) [1 2]
overlaps:
`)
}

func TestMultiCollinearCoverage(t *testing.T) {
	checkResult(t, "multi collinear", []IntSegment{
		{ID: 1, X1: 0, Y1: 0, X2: 10, Y2: 0},
		{ID: 2, X1: 2, Y1: 0, X2: 4, Y2: 0},
		{ID: 3, X1: 3, Y1: 0, X2: 8, Y2: 0},
	}, `points:
overlaps:
  (2,0) -> (3,0) [1 2]
  (3,0) -> (4,0) [1 2 3]
  (4,0) -> (8,0) [1 3]
`)
}

func TestVerticalOverlap(t *testing.T) {
	checkResult(t, "vertical overlap", []IntSegment{
		{ID: 1, X1: 0, Y1: 0, X2: 0, Y2: 5},
		{ID: 2, X1: 0, Y1: 3, X2: 0, Y2: 8},
	}, `points:
overlaps:
  (0,3) -> (0,5) [1 2]
`)
}

func TestZeroLengthSegments(t *testing.T) {
	checkResult(t, "zero length", []IntSegment{
		{ID: 1, X1: 0, Y1: 0, X2: 10, Y2: 10},
		{ID: 2, X1: 5, Y1: 5, X2: 5, Y2: 5},     // on segment 1
		{ID: 3, X1: 20, Y1: 20, X2: 20, Y2: 20}, // isolated
		{ID: 4, X1: 5, Y1: 5, X2: 5, Y2: 5},     // coincides with 2
	}, `points:
  (5,5) [1 2 4]
overlaps:
`)
}

func TestRationalIntersection(t *testing.T) {
	checkResult(t, "rational", []IntSegment{
		{ID: 1, X1: 0, Y1: 0, X2: 2, Y2: 2},
		{ID: 2, X1: 0, Y1: 1, X2: 2, Y2: 0},
	}, `points:
  (2/3,2/3) [1 2]
overlaps:
`)
}

func TestVerticalEndpointTouchAndCross(t *testing.T) {
	checkResult(t, "vertical mix", []IntSegment{
		{ID: 1, X1: 0, Y1: 0, X2: 0, Y2: 10},  // vertical
		{ID: 2, X1: 0, Y1: 4, X2: 6, Y2: 4},   // endpoint on vertical
		{ID: 3, X1: -3, Y1: 7, X2: 3, Y2: 7},  // crosses vertical interior
		{ID: 4, X1: 0, Y1: 20, X2: 0, Y2: 30}, // disjoint vertical
	}, `points:
  (0,4) [1 2]
  (0,7) [1 3]
overlaps:
`)
}

func TestDuplicateSegments(t *testing.T) {
	checkResult(t, "duplicates", []IntSegment{
		{ID: 1, X1: 0, Y1: 0, X2: 4, Y2: 4},
		{ID: 2, X1: 0, Y1: 0, X2: 4, Y2: 4},
	}, `points:
overlaps:
  (0,0) -> (4,4) [1 2]
`)
}

func TestOverlapPlusCross(t *testing.T) {
	// Collinear overlap on y=0 plus a crossing segment through the
	// overlap's interior.
	checkResult(t, "overlap plus cross", []IntSegment{
		{ID: 1, X1: 0, Y1: 0, X2: 10, Y2: 0},
		{ID: 2, X1: 2, Y1: 0, X2: 8, Y2: 0},
		{ID: 3, X1: 5, Y1: -2, X2: 5, Y2: 2},
	}, `points:
  (5,0) [1 2 3]
overlaps:
  (2,0) -> (8,0) [1 2]
`)
}

// TestSweepMatchesBruteForce cross-checks the sweep against the
// all-pairs reference on randomized inputs dense with degeneracies:
// verticals, zero-length segments, shared endpoints, and many collinear
// segments on a small coordinate grid.
func TestSweepMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 3000; iter++ {
		n := 1 + rng.Intn(8)
		coord := func() int64 { return int64(rng.Intn(9) - 4) }
		segs := make([]IntSegment, 0, n)
		for i := 0; i < n; i++ {
			segs = append(segs, IntSegment{
				ID: i,
				X1: coord(), Y1: coord(),
				X2: coord(), Y2: coord(),
			})
		}
		got := canonical(Sweep(segs))
		want := canonical(BruteForce(segs))
		if got != want {
			t.Fatalf("iter %d mismatch\nsegs: %v\nsweep:\n%s\nbrute:\n%s",
				iter, segs, got, want)
		}
	}
}

// Larger coordinates exercise exact big-integer/rational arithmetic.
func TestSweepMatchesBruteForceBigCoords(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 300; iter++ {
		n := 1 + rng.Intn(6)
		coord := func() int64 { return rng.Int63n(2_000_000_001) - 1_000_000_000 }
		segs := make([]IntSegment, 0, n)
		for i := 0; i < n; i++ {
			segs = append(segs, IntSegment{
				ID: i,
				X1: coord(), Y1: coord(),
				X2: coord(), Y2: coord(),
			})
		}
		got := canonical(Sweep(segs))
		want := canonical(BruteForce(segs))
		if got != want {
			t.Fatalf("iter %d mismatch\nsegs: %v\nsweep:\n%s\nbrute:\n%s",
				iter, segs, got, want)
		}
	}
}

// Degenerate-heavy generator: many verticals, zero-length segments, and
// shared endpoints on a tiny grid.
func TestSweepMatchesBruteForceDegenerate(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 3000; iter++ {
		n := 1 + rng.Intn(7)
		coord := func() int64 { return int64(rng.Intn(5) - 2) }
		segs := make([]IntSegment, 0, n)
		for i := 0; i < n; i++ {
			s := IntSegment{ID: i, X1: coord(), Y1: coord(), X2: coord(), Y2: coord()}
			switch rng.Intn(4) {
			case 0: // force vertical
				s.X2 = s.X1
			case 1: // force zero-length
				s.X2, s.Y2 = s.X1, s.Y1
			}
			segs = append(segs, s)
		}
		got := canonical(Sweep(segs))
		want := canonical(BruteForce(segs))
		if got != want {
			t.Fatalf("iter %d mismatch\nsegs: %v\nsweep:\n%s\nbrute:\n%s",
				iter, segs, got, want)
		}
	}
}
