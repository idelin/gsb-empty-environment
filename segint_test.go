package segint

import (
	"math/big"
	"math/rand"
	"testing"
)

func pt(x, y int64, ids ...int) PointIntersection {
	return PointIntersection{Point: NewPoint(x, y), SegmentIDs: ids}
}

func ratPt(xn, xd, yn, yd int64, ids ...int) PointIntersection {
	return PointIntersection{
		Point:      Point{X: big.NewRat(xn, xd), Y: big.NewRat(yn, yd)},
		SegmentIDs: ids,
	}
}

func ov(x1, y1, x2, y2 int64, ids ...int) OverlapSegment {
	return OverlapSegment{Start: NewPoint(x1, y1), End: NewPoint(x2, y2), SegmentIDs: ids}
}

// check runs both the sweep and the pairwise reference and compares
// against the expected intersection structure.
func check(t *testing.T, segs []Segment, wantPoints []PointIntersection, wantOverlaps []OverlapSegment) {
	t.Helper()
	sortPoints(wantPoints)
	sortOverlaps(wantOverlaps)
	want := Result{Points: wantPoints, Overlaps: wantOverlaps}.String()
	if got := IntersectSweep(segs).String(); got != want {
		t.Errorf("sweep mismatch\n got: %s\nwant: %s", got, want)
	}
	if got := IntersectPairwise(segs).String(); got != want {
		t.Errorf("pairwise mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestBasicCrossing(t *testing.T) {
	check(t, []Segment{
		NewSegment(1, 0, 0, 4, 2),
		NewSegment(2, 0, 2, 4, 0),
	}, []PointIntersection{pt(2, 1, 1, 2)}, nil)
}

func TestRationalIntersection(t *testing.T) {
	check(t, []Segment{
		NewSegment(1, 0, 0, 3, 1),
		NewSegment(2, 1, 0, 0, 1),
	}, []PointIntersection{ratPt(3, 4, 1, 4, 1, 2)}, nil)
}

func TestEndpointTouching(t *testing.T) {
	// T-junction plus endpoint-to-endpoint contact.
	check(t, []Segment{
		NewSegment(1, 0, 0, 4, 0),
		NewSegment(2, 2, 0, 2, 3),
		NewSegment(3, 4, 0, 4, 2),
	}, []PointIntersection{pt(2, 0, 1, 2), pt(4, 0, 1, 3)}, nil)
}

func TestConcurrentSegments(t *testing.T) {
	// Four segments through one point: reported once with all IDs.
	check(t, []Segment{
		NewSegment(1, 0, 0, 4, 4),
		NewSegment(2, 0, 4, 4, 0),
		NewSegment(3, 2, 0, 2, 4),
		NewSegment(4, 0, 2, 4, 2),
	}, []PointIntersection{pt(2, 2, 1, 2, 3, 4)}, nil)
}

func TestVerticalLines(t *testing.T) {
	check(t, []Segment{
		NewSegment(1, 2, 0, 2, 5), // vertical
		NewSegment(2, 2, 3, 2, 8), // vertical, collinear overlap with 1
		NewSegment(3, 0, 4, 5, 4), // horizontal crossing both inside the overlap
		NewSegment(4, 3, 0, 3, 3), // parallel, distinct: no intersection
	}, []PointIntersection{pt(2, 4, 1, 2, 3)},
		[]OverlapSegment{ov(2, 3, 2, 5, 1, 2)})
}

func TestZeroLengthSegments(t *testing.T) {
	check(t, []Segment{
		NewSegment(1, 0, 0, 4, 0),
		NewSegment(2, 2, 0, 2, 0), // point on segment 1
		NewSegment(3, 5, 0, 5, 0), // isolated point
		NewSegment(4, 0, 0, 0, 0), // point at endpoint of 1
		NewSegment(5, 2, 0, 2, 0), // coincident with 2
	}, []PointIntersection{pt(0, 0, 1, 4), pt(2, 0, 1, 2, 5)}, nil)
}

func TestZeroLengthAtCrossing(t *testing.T) {
	// A zero-length segment exactly where two others cross joins the set.
	check(t, []Segment{
		NewSegment(1, 0, 0, 4, 0),
		NewSegment(2, 2, -1, 2, 1),
		NewSegment(3, 2, 0, 2, 0),
	}, []PointIntersection{pt(2, 0, 1, 2, 3)}, nil)
}

func TestCollinearOverlaps(t *testing.T) {
	// Partial overlap, containment, identical, touching, disjoint.
	check(t, []Segment{
		NewSegment(1, 0, 0, 6, 0),
		NewSegment(2, 3, 0, 9, 0),  // partial overlap with 1
		NewSegment(3, 1, 0, 2, 0),  // contained in 1
		NewSegment(4, 9, 0, 12, 0), // touches 2 at one endpoint
		NewSegment(5, 20, 0, 25, 0),
		NewSegment(6, 30, 0, 35, 0), // disjoint from 5
		NewSegment(7, 20, 0, 25, 0), // identical to 5
	}, []PointIntersection{pt(9, 0, 2, 4)},
		[]OverlapSegment{
			ov(1, 0, 2, 0, 1, 3),
			ov(3, 0, 6, 0, 1, 2),
			ov(20, 0, 25, 0, 5, 7),
		})
}

func TestMultipleCollinearCoverage(t *testing.T) {
	// Coverage set changes along the line: atomic pieces with exact IDs.
	check(t, []Segment{
		NewSegment(1, 0, 0, 5, 0),
		NewSegment(2, 3, 0, 10, 0),
		NewSegment(3, 4, 0, 6, 0),
	}, nil, []OverlapSegment{
		ov(3, 0, 4, 0, 1, 2),
		ov(4, 0, 5, 0, 1, 2, 3),
		ov(5, 0, 6, 0, 2, 3),
	})
}

func TestSweepMatchesPairwiseRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for trial := 0; trial < 500; trial++ {
		n := 2 + rng.Intn(8)
		segs := make([]Segment, n)
		for i := range segs {
			x1 := int64(rng.Intn(7) - 3)
			y1 := int64(rng.Intn(7) - 3)
			x2 := int64(rng.Intn(7) - 3)
			y2 := int64(rng.Intn(7) - 3)
			switch rng.Intn(5) {
			case 0: // force vertical
				x2 = x1
			case 1: // force horizontal
				y2 = y1
			case 2: // force zero-length
				x2, y2 = x1, y1
			}
			segs[i] = NewSegment(i+1, x1, y1, x2, y2)
		}
		sweep := IntersectSweep(segs).String()
		pair := IntersectPairwise(segs).String()
		if sweep != pair {
			t.Fatalf("trial %d mismatch\nsegs: %v\nsweep:    %s\npairwise: %s", trial, segs, sweep, pair)
		}
	}
}
