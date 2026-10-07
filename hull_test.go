package convexhull

import (
	"math/big"
	"math/rand"
	"reflect"
	"testing"
)

func idsOf(h Hull) []int { return h.Vertices }

func TestSquareWithEdgePointsAndDuplicates(t *testing.T) {
	// Square corners plus midpoints on every edge, with duplicates.
	pts := []Point{
		Pt(0, 0),           // 0
		Pt(2, 0),           // 1
		Pt(2, 2),           // 2
		Pt(0, 2),           // 3
		PtFrac(1, 1, 0, 1), // 4 midpoint of bottom edge
		PtFrac(2, 1, 1, 1), // 5 midpoint of right edge
		PtFrac(1, 1, 2, 1), // 6 midpoint of top edge
		PtFrac(0, 1, 1, 1), // 7 midpoint of left edge
		Pt(1, 1),           // 8 interior
		Pt(0, 0),           // 9 duplicate of vertex 0
		PtFrac(2, 2, 0, 1), // 10 duplicate of 4 via 2/2
		PtFrac(4, 2, 4, 2), // 11 duplicate of 2 via 4/2
	}
	h := ConvexHull(pts)
	if err := Verify(pts, h); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got, want := idsOf(h), []int{0, 1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("vertices = %v, want %v (CCW extremes only)", got, want)
	}
	wantEdgePoints := [][]int{
		{0, 1, 4, 9, 10}, // bottom edge, duplicates included
		{1, 2, 5, 11},    // right edge
		{2, 3, 6, 11},    // top edge
		{0, 3, 7, 9},     // left edge
	}
	for i, want := range wantEdgePoints {
		if !reflect.DeepEqual(h.Edges[i].Points, want) {
			t.Errorf("edge %d points = %v, want %v", i, h.Edges[i].Points, want)
		}
	}
	if area := SignedArea2(pts, h); area.Sign() <= 0 {
		t.Errorf("signed area not positive: %v", area)
	}
}

func TestAllCollinear(t *testing.T) {
	pts := []Point{
		PtFrac(1, 2, 1, 2),
		Pt(0, 0),
		Pt(1, 1),
		PtFrac(2, 4, 2, 4), // duplicate of 0.5,0.5
		Pt(-1, -1),
	}
	h := ConvexHull(pts)
	if err := Verify(pts, h); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(h.Vertices) != 2 {
		t.Fatalf("vertices = %v, want 2 endpoints", h.Vertices)
	}
	if got := []Point{pts[h.Vertices[0]], pts[h.Vertices[1]]}; !(got[0].X.Cmp(big.NewRat(-1, 1)) == 0 && got[1].X.Cmp(big.NewRat(1, 1)) == 0) {
		t.Fatalf("endpoints = %v, want (-1,-1) and (1,1)", got)
	}
	if len(h.Edges) != 1 {
		t.Fatalf("edges = %v, want a single edge", h.Edges)
	}
	if want := []int{0, 1, 2, 3, 4}; !reflect.DeepEqual(h.Edges[0].Points, want) {
		t.Fatalf("edge points = %v, want all ids %v", h.Edges[0].Points, want)
	}
}

func TestAllIdentical(t *testing.T) {
	pts := []Point{Pt(3, 4), PtFrac(6, 2, 8, 2), Pt(3, 4)}
	h := ConvexHull(pts)
	if err := Verify(pts, h); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(h.Vertices) != 1 || h.Vertices[0] != 0 {
		t.Fatalf("vertices = %v, want [0]", h.Vertices)
	}
	if len(h.Edges) != 0 {
		t.Fatalf("edges = %v, want none", h.Edges)
	}
}

func TestEmpty(t *testing.T) {
	h := ConvexHull(nil)
	if err := Verify(nil, h); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(h.Vertices) != 0 || len(h.Edges) != 0 {
		t.Fatalf("got %+v, want empty hull", h)
	}
}

// TestExactPredicates uses rationals whose cross products vanish or stay
// nonzero only far beyond float64 resolution; no epsilon may change the
// outcome.
func TestExactPredicates(t *testing.T) {
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(40), nil)
	tiny := func(num int64) *big.Rat {
		return new(big.Rat).SetFrac(big.NewInt(num), den)
	}

	// p is collinear with (0,0)-(1,1) only in exact arithmetic if its
	// coordinates match; here it overshoots the line x+y=1 of the
	// triangle (0,0),(1,0),(0,1) by 1e-40, so it must be a vertex.
	pts := []Point{
		Pt(0, 0),
		Pt(1, 0),
		Pt(0, 1),
		{X: big.NewRat(1, 2), Y: new(big.Rat).Add(big.NewRat(1, 2), tiny(1))}, // 3: outside by 1e-40
		{X: big.NewRat(1, 2), Y: new(big.Rat).Sub(big.NewRat(1, 2), tiny(1))}, // 4: inside by 1e-40
	}
	h := ConvexHull(pts)
	if err := Verify(pts, h); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	found := false
	for _, v := range h.Vertices {
		if v == 3 {
			found = true
		}
		if v == 4 {
			t.Fatalf("point 4 is inside by 1e-40 and must not be a vertex")
		}
	}
	if !found {
		t.Fatalf("point 3 is outside by 1e-40 and must be a vertex, got %v", h.Vertices)
	}

	// Exactly collinear triple on one edge of a triangle: the middle
	// point (given twice with different denominators) must not appear
	// as a vertex, but its ids must ride on the edge.
	col := []Point{
		{X: big.NewRat(0, 1), Y: big.NewRat(0, 1)},
		{X: big.NewRat(1, 3), Y: big.NewRat(1, 7)},
		{X: big.NewRat(2, 6), Y: big.NewRat(2, 14)}, // == (1/3, 1/7)
		{X: big.NewRat(2, 3), Y: big.NewRat(2, 7)},
		{X: big.NewRat(1, 1), Y: big.NewRat(1, 1)},
	}
	hc := ConvexHull(col)
	if err := Verify(col, hc); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(hc.Vertices) != 3 {
		t.Fatalf("vertices = %v, want the 3 extreme points", hc.Vertices)
	}
	for _, v := range hc.Vertices {
		if v == 1 || v == 2 {
			t.Fatalf("collinear duplicate-coordinate points must not be vertices: %v", hc.Vertices)
		}
	}
	if want := []int{0, 1, 2, 3}; !reflect.DeepEqual(hc.Edges[0].Points, want) {
		t.Fatalf("bottom edge points = %v, want %v", hc.Edges[0].Points, want)
	}
}

func TestVerifyRejectsBadHulls(t *testing.T) {
	pts := []Point{Pt(0, 0), Pt(2, 0), Pt(2, 2), Pt(0, 2), Pt(1, 0)}
	good := ConvexHull(pts)

	// Clockwise order.
	cw := Hull{Vertices: []int{0, 3, 2, 1}}
	cw.Edges = []Edge{
		cw.buildEdge(pts, 0, 3), cw.buildEdge(pts, 3, 2),
		cw.buildEdge(pts, 2, 1), cw.buildEdge(pts, 1, 0),
	}
	if err := Verify(pts, cw); err == nil {
		t.Error("clockwise hull accepted")
	}

	// Missing vertex: triangle instead of square.
	missing := Hull{Vertices: []int{0, 1, 2}}
	missing.Edges = []Edge{
		missing.buildEdge(pts, 0, 1), missing.buildEdge(pts, 1, 2), missing.buildEdge(pts, 2, 0),
	}
	if err := Verify(pts, missing); err == nil {
		t.Error("hull missing a vertex accepted")
	}

	// Edge point list missing a collinear id.
	bad := good
	bad.Edges = make([]Edge, len(good.Edges))
	copy(bad.Edges, good.Edges)
	bad.Edges[0].Points = bad.Edges[0].Points[:len(bad.Edges[0].Points)-1]
	if err := Verify(pts, bad); err == nil {
		t.Error("edge with truncated Points accepted")
	}

	// Extra id not on the segment.
	bad2 := good
	bad2.Edges = make([]Edge, len(good.Edges))
	copy(bad2.Edges, good.Edges)
	bad2.Edges[1].Points = append(append([]int{}, good.Edges[1].Points...), 4)
	if err := Verify(pts, bad2); err == nil {
		t.Error("edge with foreign id in Points accepted")
	}

	// Interior point claimed as vertex.
	inner := append([]Point{}, pts...)
	inner = append(inner, Pt(1, 1))
	fake := Hull{Vertices: []int{0, 1, 2, 5}}
	fake.Edges = []Edge{
		fake.buildEdge(inner, 0, 1), fake.buildEdge(inner, 1, 2),
		fake.buildEdge(inner, 2, 5), fake.buildEdge(inner, 5, 0),
	}
	if err := Verify(inner, fake); err == nil {
		t.Error("hull through an interior point accepted")
	}
}

func TestFuzzAgainstVerify(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(24)
		pts := make([]Point, n)
		for i := range pts {
			// Small coordinate pool forces duplicates and collinearities.
			xn := int64(rng.Intn(7) - 3)
			yn := int64(rng.Intn(7) - 3)
			xd := int64(1 + rng.Intn(4))
			yd := int64(1 + rng.Intn(4))
			pts[i] = PtFrac(xn, xd, yn, yd)
		}
		h := ConvexHull(pts)
		if err := Verify(pts, h); err != nil {
			t.Fatalf("trial %d: Verify: %v\npts=%v", trial, err, pts)
		}
		// Independently brute-force the extreme points: a point is
		// extreme iff it is not a convex combination of the others;
		// equivalently some supporting line touches it. Compare counts
		// via the exhaustive supporting-line structure.
		want := bruteForceVertexCoords(pts)
		got := make(map[coordKey]bool)
		for _, v := range h.Vertices {
			got[keyOf(pts[v])] = true
		}
		if len(got) != len(want) {
			t.Fatalf("trial %d: vertex coords %v, want %v", trial, got, want)
		}
		for k := range want {
			if !got[k] {
				t.Fatalf("trial %d: missing vertex %v", trial, k)
			}
		}
	}
}

// bruteForceVertexCoords derives the hull vertices purely from
// supporting lines: a coordinate is a vertex iff it is an endpoint of
// the on-line point set of some supporting line through two input
// points, or it is the unique point of the set.
func bruteForceVertexCoords(pts []Point) map[coordKey]bool {
	out := make(map[coordKey]bool)
	uniq := make(map[coordKey]Point)
	for _, p := range pts {
		uniq[keyOf(p)] = p
	}
	if len(uniq) == 1 {
		for k := range uniq {
			out[k] = true
		}
		return out
	}
	us := make([]Point, 0, len(uniq))
	for _, p := range uniq {
		us = append(us, p)
	}
	for i := 0; i < len(us); i++ {
		for j := i + 1; j < len(us); j++ {
			neg, pos := false, false
			for _, p := range us {
				switch s := orientSign(us[i], us[j], p); {
				case s < 0:
					neg = true
				case s > 0:
					pos = true
				}
			}
			if neg && pos {
				continue
			}
			// Supporting (or fully collinear) line: extremes among the
			// collinear points on it are vertices.
			var lo, hi Point
			first := true
			for _, p := range us {
				if orientSign(us[i], us[j], p) != 0 {
					continue
				}
				if first || less(p, lo) {
					lo = p
				}
				if first || less(hi, p) {
					hi = p
				}
				first = false
			}
			out[keyOf(lo)] = true
			out[keyOf(hi)] = true
		}
	}
	return out
}

func less(a, b Point) bool {
	if c := a.X.Cmp(b.X); c != 0 {
		return c < 0
	}
	return a.Y.Cmp(b.Y) < 0
}
