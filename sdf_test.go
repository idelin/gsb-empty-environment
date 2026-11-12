package taskpkg

import (
	"math"
	"testing"
)

// refSegDist is an independent high-precision reference: dense sampling of
// the segment followed by golden-section refinement of the best bracket.
func refSegDist(seg Segment, p Point) (dist, tStar float64) {
	const n = 4096
	bestT, bestD := 0.0, math.Inf(1)
	for i := 0; i <= n; i++ {
		t := float64(i) / n
		if d := seg.eval(t).Sub(p).Len(); d < bestD {
			bestD, bestT = d, t
		}
	}
	a, b := math.Max(0, bestT-1.5/n), math.Min(1, bestT+1.5/n)
	const gr = 0.6180339887498949
	c := b - gr*(b-a)
	d := a + gr*(b-a)
	fc := seg.eval(c).Sub(p).Len()
	fd := seg.eval(d).Sub(p).Len()
	for i := 0; i < 200; i++ {
		if fc < fd {
			b, d, fd = d, c, fc
			c = b - gr*(b-a)
			fc = seg.eval(c).Sub(p).Len()
		} else {
			a, c, fc = c, d, fd
			d = a + gr*(b-a)
			fd = seg.eval(d).Sub(p).Len()
		}
	}
	tStar = (a + b) / 2
	return seg.eval(tStar).Sub(p).Len(), tStar
}

// refShapeDist is the min of refSegDist over every segment of the shape.
func refShapeDist(s Shape, p Point) float64 {
	best := math.Inf(1)
	for _, c := range s.Contours {
		for _, seg := range c.Segments {
			if d, _ := refSegDist(seg, p); d < best {
				best = d
			}
		}
	}
	return best
}

// circleShape approximates a unit circle with n quadratic Bézier arcs.
func circleShape(n int) Shape {
	var segs []Segment
	step := 2 * math.Pi / float64(n)
	for i := 0; i < n; i++ {
		a0 := float64(i) * step
		a1 := float64(i+1) * step
		am := (a0 + a1) / 2
		k := 1 / math.Cos(step/2)
		segs = append(segs, Quad(
			Point{math.Cos(a0), math.Sin(a0)},
			Point{k * math.Cos(am), k * math.Sin(am)},
			Point{math.Cos(a1), math.Sin(a1)},
		))
	}
	return Shape{Contours: []Contour{{Segments: segs}}}
}

func TestEmptyShape(t *testing.T) {
	d, id := Shape{}.SignedDistance(Point{1, 2})
	if !math.IsInf(d, 1) || id != -1 {
		t.Fatalf("empty shape: got (%v, %d), want (+Inf, -1)", d, id)
	}
	// Empty contours among real ones must be skipped, not crash.
	s := Shape{Contours: []Contour{
		{},
		{Segments: []Segment{
			Line(Point{0, 0}, Point{2, 0}),
			Line(Point{2, 0}, Point{2, 2}),
			Line(Point{2, 2}, Point{0, 2}),
			Line(Point{0, 2}, Point{0, 0}),
		}},
	}}
	d, id = s.SignedDistance(Point{1, 1})
	if d != -1 || id != 1 {
		t.Fatalf("shape with empty contour: got (%v, %d), want (-1, 1)", d, id)
	}
}

func TestQuadInteriorRoot(t *testing.T) {
	// Parabola y = 1 - x^2 as a quadratic Bézier; the nearest point to
	// p is interior, so endpoint-only distance would be wrong.
	seg := Quad(Point{-1, 0}, Point{0, 2}, Point{1, 0})
	p := Point{0.3, 0.5}
	ref, tStar := refSegDist(seg, p)
	if tStar < 0.01 || tStar > 0.99 {
		t.Fatalf("reference nearest parameter t=%v is not interior", tStar)
	}
	got := math.Sqrt(seg.distSq(p))
	if math.Abs(got-ref) > 1e-9 {
		t.Fatalf("distSq = %v, reference %v", got, ref)
	}
	// Endpoint-only comparison must be visibly worse.
	endOnly := math.Min(p.Sub(seg.P0).Len(), p.Sub(seg.P1).Len())
	if endOnly < got+1e-3 {
		t.Fatalf("test is vacuous: endpoint distance %v too close to %v", endOnly, got)
	}
}

func TestCircleApproximation(t *testing.T) {
	s := circleShape(16)
	// Radial deviation of the approximating curve from the true unit circle,
	// measured by dense sampling: the SDF may differ from the exact circle
	// SDF by at most this amount.
	maxDev := 0.0
	for _, seg := range s.Contours[0].Segments {
		for i := 0; i <= 2000; i++ {
			r := seg.eval(float64(i) / 2000).Len()
			if d := math.Abs(r - 1); d > maxDev {
				maxDev = d
			}
		}
	}
	for ri, r := range []float64{0.3, 0.9, 1.1, 2.0} {
		for ai := 0; ai < 24; ai++ {
			theta := float64(ai)*2*math.Pi/24 + 0.013 // avoid exact vertices
			p := Point{r * math.Cos(theta), r * math.Sin(theta)}
			d, id := s.SignedDistance(p)
			if id != 0 {
				t.Fatalf("r=%v theta=%v: contour id %d, want 0", r, theta, id)
			}
			want := r - 1
			if math.Abs(d-want) > maxDev+1e-9 {
				t.Fatalf("r[%d]=%v theta=%v: sdf %v, circle ref %v (dev bound %v)",
					ri, r, theta, d, want, maxDev)
			}
			if (r < 1) != (d < 0) {
				t.Fatalf("r=%v theta=%v: wrong sign %v", r, theta, d)
			}
		}
	}
}

func squareContour(x0, y0, x1, y1 float64, ccw bool) Contour {
	pts := []Point{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}
	if !ccw {
		pts[1], pts[3] = pts[3], pts[1]
	}
	var segs []Segment
	for i := range pts {
		segs = append(segs, Line(pts[i], pts[(i+1)%len(pts)]))
	}
	return Contour{Segments: segs}
}

func TestInnerOuterRings(t *testing.T) {
	// Outer square CCW, inner square CW: non-zero winding makes the inner
	// square a hole.
	s := Shape{Contours: []Contour{
		squareContour(-2, -2, 2, 2, true),
		squareContour(-1, -1, 1, 1, false),
	}}
	cases := []struct {
		p    Point
		want float64
		id   int
	}{
		{Point{0, 0}, 1, 1},         // inside the hole: outside the fill
		{Point{1.4, 0}, -0.4, 1},    // between rings, nearer inner contour
		{Point{1.8, 0}, -0.2, 0},    // between rings, nearer outer contour
		{Point{3, 0}, 1, 0},         // outside everything
		{Point{0, 1.5}, -0.5, 0},    // tie between rings: first contour wins
		{Point{-1.4, 0.5}, -0.4, 1}, // beside the inner ring's left edge
	}
	for _, c := range cases {
		d, id := s.SignedDistance(c.p)
		if math.Abs(d-c.want) > 1e-12 {
			t.Errorf("p=%v: sdf %v, want %v", c.p, d, c.want)
		}
		if id != c.id {
			t.Errorf("p=%v: contour id %d, want %d", c.p, id, c.id)
		}
	}
}

func TestSharpCorners(t *testing.T) {
	// Triangle with a sharp apex at the origin (half-angle ~14°).
	tri := Shape{Contours: []Contour{{Segments: []Segment{
		Line(Point{0, 0}, Point{4, 1}),
		Line(Point{4, 1}, Point{4, -1}),
		Line(Point{4, -1}, Point{0, 0}),
	}}}}
	sq17 := math.Sqrt(17)
	cases := []struct {
		p    Point
		want float64
	}{
		{Point{2, 0}, -2 / sq17},    // inside, equidistant to both slanted edges
		{Point{-0.5, 0}, 0.5},       // outside, nearest feature is the apex vertex
		{Point{2, 0.9}, 1.6 / sq17}, // outside above the upper edge
		{Point{4.5, 0}, 0.5},        // outside beyond the base
	}
	for _, c := range cases {
		d, id := tri.SignedDistance(c.p)
		if id != 0 {
			t.Errorf("p=%v: contour id %d, want 0", c.p, id)
		}
		if math.Abs(d-c.want) > 1e-12 {
			t.Errorf("p=%v: sdf %v, want %v", c.p, d, c.want)
		}
	}

	// Dart with a sharp concave notch at (2,0).
	dart := Shape{Contours: []Contour{{Segments: []Segment{
		Line(Point{0, 0}, Point{4, 1}),
		Line(Point{4, 1}, Point{2, 0}),
		Line(Point{2, 0}, Point{4, -1}),
		Line(Point{4, -1}, Point{0, 0}),
	}}}}
	sq5 := math.Sqrt(5)
	dartCases := []struct {
		p    Point
		want float64
	}{
		{Point{1, 0}, -1 / sq17}, // inside the left lobe
		{Point{1, -0.1}, -0.6 / sq17},
		{Point{2.5, 0}, 0.5 / sq5}, // inside the concave notch: outside fill
		{Point{3.5, 0}, 1.5 / sq5}, // deeper in the notch
	}
	for _, c := range dartCases {
		d, _ := dart.SignedDistance(c.p)
		if math.Abs(d-c.want) > 1e-12 {
			t.Errorf("dart p=%v: sdf %v, want %v", c.p, d, c.want)
		}
	}
}

func TestQuadCusp(t *testing.T) {
	// Leaf shape: two quadratic Béziers meeting in a cusp at the origin,
	// closed by a line. Validated against the dense+golden reference.
	leaf := Shape{Contours: []Contour{{Segments: []Segment{
		Quad(Point{1, 0.5}, Point{0.2, 0.1}, Point{0, 0}),
		Quad(Point{0, 0}, Point{0.2, -0.1}, Point{1, -0.5}),
		Line(Point{1, -0.5}, Point{1, 0.5}),
	}}}}
	for _, p := range []Point{
		{-0.1, 0}, {0.05, 0.02}, {0.5, 0}, {0.9, 0.3}, {1.2, 0}, {0.3, -0.2},
	} {
		d, id := leaf.SignedDistance(p)
		if id != 0 {
			t.Fatalf("p=%v: contour id %d, want 0", p, id)
		}
		ref := refShapeDist(leaf, p)
		if math.Abs(math.Abs(d)-ref) > 1e-9 {
			t.Errorf("p=%v: |sdf| %v, reference %v", p, math.Abs(d), ref)
		}
	}
	// Sign checks near the cusp.
	if d, _ := leaf.SignedDistance(Point{-0.1, 0}); d < 0 {
		t.Error("point behind cusp should be outside")
	}
	if d, _ := leaf.SignedDistance(Point{0.5, 0}); d > 0 {
		t.Error("point inside leaf should be inside")
	}
}

func TestRasterizePixelCentres(t *testing.T) {
	s := Shape{Contours: []Contour{
		squareContour(0, 0, 8, 8, true),
		squareContour(3, 3, 5, 5, false),
	}}
	origin := Point{-1, -1}
	const ps = 0.5
	cells := Rasterize(s, origin, ps, 20, 20)
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			p := Point{origin.X + (float64(x)+0.5)*ps, origin.Y + (float64(y)+0.5)*ps}
			d, id := s.SignedDistance(p)
			c := cells[y*20+x]
			if c.Distance != d || c.ContourID != id {
				t.Fatalf("cell (%d,%d): got (%v,%d), want (%v,%d)",
					x, y, c.Distance, c.ContourID, d, id)
			}
		}
	}
	// Centre of the hole is outside the fill; ring interior is inside.
	if c := cells[8*20+8]; c.Distance <= 0 {
		t.Errorf("hole centre should be positive, got %v", c.Distance)
	}
	if c := cells[8*20+2]; c.Distance >= 0 {
		t.Errorf("ring interior should be negative, got %v", c.Distance)
	}
}
