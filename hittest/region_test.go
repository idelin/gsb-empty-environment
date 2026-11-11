package hittest

import (
	"math"
	"testing"
)

func approxEq(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestRegionCanonicalizesOverlappingRects(t *testing.T) {
	// Touching rectangles that together form one rectangle merge into a
	// single canonical rectangle.
	g := NewRegion(R(0, 0, 10, 10), R(10, 0, 15, 10))
	want := NewRegion(R(0, 0, 15, 10))
	if !g.Equals(want) {
		t.Fatalf("union = %v, want %v", g.Rects(), want.Rects())
	}

	// Overlapping rectangles whose union is genuinely L-shaped stay split
	// into the minimal three-rectangle disjoint representation.
	l := NewRegion(R(0, 0, 10, 10), R(5, 5, 15, 15))
	if got := len(l.Rects()); got != 3 {
		t.Fatalf("overlap union rect count = %d, want 3: %v", got, l.Rects())
	}
	if l.Contains(Point{X: 12, Y: 2}) {
		t.Fatal("upper-right area outside both rects must be transparent")
	}
	if !l.Contains(Point{X: 7, Y: 7}) {
		t.Fatal("overlap center must be inside")
	}
}

func TestRegionLShapeKeepsHole(t *testing.T) {
	// An L-shape is two touching rectangles; the upper-right stays a hole.
	g := NewRegion(R(0, 0, 10, 30), R(10, 20, 30, 30))

	cases := []struct {
		name string
		p    Point
		want bool
	}{
		{"inside vertical bar", Point{X: 5, Y: 5}, true},
		{"inside horizontal bar", Point{X: 25, Y: 25}, true},
		{"inside the hole", Point{X: 25, Y: 5}, false},
		{"on left edge is inside", Point{X: 0, Y: 5}, true},
		{"on right edge is outside (half-open)", Point{X: 10, Y: 5}, false},
		{"on top edge is inside", Point{X: 5, Y: 0}, true},
		{"on bottom edge is outside", Point{X: 5, Y: 30}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := g.Contains(tc.p); got != tc.want {
				t.Fatalf("Contains(%v) = %v, want %v", tc.p, got, tc.want)
			}
		})
	}
	if b := g.Bounds(); b != R(0, 0, 30, 30) {
		t.Fatalf("Bounds = %v, want full 30x30 box", b)
	}
}

func TestRegionIntersect(t *testing.T) {
	a := NewRegion(R(0, 0, 10, 10), R(20, 0, 30, 10))
	b := NewRegion(R(5, -5, 25, 15))
	got := a.Intersect(b)
	want := NewRegion(R(5, 0, 10, 10), R(20, 0, 25, 10))
	if !got.Equals(want) {
		t.Fatalf("intersect = %v, want %v", got.Rects(), want.Rects())
	}
}

func TestRegionTransformAndInverse(t *testing.T) {
	g := NewRegion(R(0, 0, 10, 20))
	// Scale by 1.5 then translate; fractional scaling must stay exact enough
	// for boundary hit tests.
	x := Scale(1.5, 2.0).Then(Translate(100, 200))
	moved := g.Transform(x)
	want := NewRegion(R(100, 200, 115, 240))
	if !moved.Equals(want) {
		t.Fatalf("transform = %v, want %v", moved.Rects(), want.Rects())
	}

	inv := x.Invert()
	origin := inv.Apply(Point{X: 100, Y: 200})
	if !approxEq(origin.X, 0) || !approxEq(origin.Y, 0) {
		t.Fatalf("inverse origin = %v, want (0,0)", origin)
	}
	corner := inv.Apply(Point{X: 115, Y: 240})
	if !approxEq(corner.X, 10) || !approxEq(corner.Y, 20) {
		t.Fatalf("inverse corner = %v, want (10,20)", corner)
	}
}

func TestRegionUnionWithHole(t *testing.T) {
	// A cross: vertical and horizontal bars overlap at the center only.
	cross := NewRegion(R(40, 0, 60, 100), R(0, 40, 100, 60))
	if cross.Contains(Point{X: 10, Y: 10}) {
		t.Fatal("corner of cross must be transparent")
	}
	if !cross.Contains(Point{X: 50, Y: 50}) {
		t.Fatal("center of cross must be inside")
	}
	if !cross.Contains(Point{X: 99, Y: 50}) {
		t.Fatal("right arm must be inside")
	}
	if cross.Contains(Point{X: 100, Y: 50}) {
		t.Fatal("half-open outer edge must be outside")
	}
}
