package taskpkg

import (
	"math"
	"testing"
)

func lineCubic(a, b Point) Cubic {
	third := Point{(b.X - a.X) / 3, (b.Y - a.Y) / 3}
	return Cubic{
		P0: a,
		P1: Point{a.X + third.X, a.Y + third.Y},
		P2: Point{b.X - third.X, b.Y - third.Y},
		P3: b,
	}
}

// circleArcCubic returns a cubic approximating a circular arc of radius r
// spanning angle a (|a| <= pi/2), starting at angle start.
func circleArcCubic(center Point, r, start, a float64) Cubic {
	k := 4.0 / 3.0 * math.Tan(a/4)
	p := func(angle float64) Point {
		return Point{center.X + r*math.Cos(angle), center.Y + r*math.Sin(angle)}
	}
	p0 := p(start)
	p3 := p(start + a)
	t0 := Point{-math.Sin(start), math.Cos(start)}
	t1 := Point{-math.Sin(start + a), math.Cos(start + a)}
	return Cubic{
		P0: p0,
		P1: Point{p0.X + r*k*t0.X, p0.Y + r*k*t0.Y},
		P2: Point{p3.X - r*k*t1.X, p3.Y - r*k*t1.Y},
		P3: p3,
	}
}

func circlePath(r float64, closed bool) Path {
	segs := make([]Cubic, 4)
	for i := 0; i < 4; i++ {
		segs[i] = circleArcCubic(Point{}, r, float64(i)*math.Pi/2, math.Pi/2)
	}
	return Path{Segments: segs, Closed: closed}
}

// distanceToPolyline reports the minimum distance from p to the polyline v.
func distanceToPolyline(p Point, v []Point) float64 {
	best := math.Inf(1)
	for i := 0; i+1 < len(v); i++ {
		best = math.Min(best, distanceToSegment(p, v[i], v[i+1]))
	}
	return best
}

func distanceToSegment(p, a, b Point) float64 {
	ab := sub(b, a)
	length2 := dot(ab, ab)
	if length2 == 0 {
		return dist(p, a)
	}
	t := dot(sub(p, a), ab) / length2
	t = math.Max(0, math.Min(1, t))
	closest := Point{a.X + t*ab.X, a.Y + t*ab.Y}
	return dist(p, closest)
}

func minMaxWidth(contour Contour, centerline []Point) (minW, maxW float64) {
	minW, maxW = math.Inf(1), 0
	for _, p := range contour.Points {
		d := distanceToPolyline(p, centerline)
		minW = math.Min(minW, d)
		maxW = math.Max(maxW, d)
	}
	return minW, maxW
}

func boundingX(c Contour) (minX, maxX float64) {
	minX, maxX = math.Inf(1), math.Inf(-1)
	for _, p := range c.Points {
		minX = math.Min(minX, p.X)
		maxX = math.Max(maxX, p.X)
	}
	return minX, maxX
}

func TestStraightLineButtCap(t *testing.T) {
	const width = 4.0
	path := Path{Segments: []Cubic{lineCubic(Point{0, 0}, Point{10, 0})}}
	opt := Options{Width: width, Join: JoinMiter, Cap: CapButt, MiterLimit: 4, Tolerance: 0.1}

	contours, err := Stroke(path, opt)
	if err != nil || len(contours) != 1 {
		t.Fatalf("Stroke returned %v contours, err=%v", len(contours), err)
	}
	c := contours[0]
	if got := signedArea(c.Points); got >= 0 {
		t.Fatalf("outer contour must wind clockwise, signed area=%v", got)
	}

	centerline := []Point{{0, 0}, {10, 0}}
	minW, maxW := minMaxWidth(c, centerline)
	want := width / 2
	if math.Abs(minW-want) > 1e-9 || math.Abs(maxW-want) > 1e-9 {
		t.Fatalf("half-width deviation: got min=%v max=%v want %v", minW, maxW, want)
	}

	minX, maxX := boundingX(c)
	if math.Abs(minX) > 1e-9 || math.Abs(maxX-10) > 1e-9 {
		t.Fatalf("butt cap must not extend past endpoints, got x in [%v,%v]", minX, maxX)
	}
}

func TestSquareCapExtension(t *testing.T) {
	const width = 4.0
	path := Path{Segments: []Cubic{lineCubic(Point{0, 0}, Point{10, 0})}}
	opt := Options{Width: width, Cap: CapSquare, Tolerance: 0.1}
	contours, err := Stroke(path, opt)
	if err != nil {
		t.Fatal(err)
	}
	minX, maxX := boundingX(contours[0])
	if math.Abs(minX+width/2) > 1e-9 || math.Abs(maxX-(10+width/2)) > 1e-9 {
		t.Fatalf("square cap should extend half width, got x in [%v,%v]", minX, maxX)
	}
}

func TestRoundCapIsErrorDriven(t *testing.T) {
	path := Path{Segments: []Cubic{lineCubic(Point{0, 0}, Point{10, 0})}}
	coarse, _ := Stroke(path, Options{Width: 4, Cap: CapRound, MiterLimit: 4, Tolerance: 0.5})
	fine, _ := Stroke(path, Options{Width: 4, Cap: CapRound, MiterLimit: 4, Tolerance: 0.02})
	if len(coarse[0].Points) >= len(fine[0].Points) {
		t.Fatalf("tighter tolerance must add vertices: coarse=%d fine=%d",
			len(coarse[0].Points), len(fine[0].Points))
	}

	// Round-cap vertices stay on the true end semicircles within tolerance.
	for _, tol := range []float64{0.5, 0.1, 0.02} {
		cs, err := Stroke(path, Options{Width: 4, Cap: CapRound, MiterLimit: 4, Tolerance: tol})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range cs[0].Points {
			if math.Abs(p.X) > 1e-9 && math.Abs(p.X-10) > 1e-9 {
				continue
			}
			center := Point{0, 0}
			if p.X > 5 {
				center = Point{10, 0}
			}
			if d := math.Abs(dist(p, center) - 2); d > tol*1.01+1e-9 {
				t.Fatalf("round cap sagitta %v exceeds tolerance %v", d, tol)
			}
		}
	}
}

func TestClosedCircleWidthError(t *testing.T) {
	const r, width = 10.0, 2.0
	path := circlePath(r, true)
	for _, tol := range []float64{0.2, 0.05} {
		opt := Options{Width: width, Tolerance: tol, MiterLimit: 4, Join: JoinRound}
		contours, err := Stroke(path, opt)
		if err != nil || len(contours) != 2 {
			t.Fatalf("closed circle needs 2 contours, got %d err=%v", len(contours), err)
		}
		outer, inner := contours[0], contours[1]
		if signedArea(outer.Points) >= 0 || signedArea(inner.Points) <= 0 {
			t.Fatalf("contour directions wrong: outer=%v inner=%v",
				signedArea(outer.Points), signedArea(inner.Points))
		}
		for _, c := range []Contour{outer, inner} {
			for _, p := range c.Points {
				d := math.Hypot(p.X, p.Y)
				if math.Abs(d-(r+width/2)) > tol && math.Abs(d-(r-width/2)) > tol {
					t.Fatalf("tol=%v radial offset %v outside both bands", tol, d)
				}
			}
		}
	}
}

func TestMiterJoinExactGeometry(t *testing.T) {
	// 90-degree corner walking east then north.
	path := Path{Segments: []Cubic{
		lineCubic(Point{0, 0}, Point{10, 0}),
		lineCubic(Point{10, 0}, Point{10, 10}),
	}}
	const width = 2.0
	opt := Options{Width: width, Join: JoinMiter, Cap: CapButt, MiterLimit: 4, Tolerance: 0.1}
	contours, err := Stroke(path, opt)
	if err != nil {
		t.Fatal(err)
	}

	r := width / 2
	wantMiter := Point{10 + r/math.Sqrt2, 0 - r/math.Sqrt2}
	found := false
	for _, p := range contours[0].Points {
		if dist(p, wantMiter) < 1e-7 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected miter point %v in contour %v", wantMiter, contours[0].Points)
	}
}

func TestMiterLimitFallsBackToBevel(t *testing.T) {
	// Tight 30-degree corner; bevel endpoints lie exactly r away from vertex.
	p1 := Point{10, 0}
	dir := Point{math.Cos(math.Pi / 6), math.Sin(math.Pi / 6)}
	p2 := Point{p1.X + 10*dir.X, p1.Y + 10*dir.Y}
	path := Path{Segments: []Cubic{lineCubic(Point{0, 0}, p1), lineCubic(p1, p2)}}

	const width = 2.0
	opt := Options{Width: width, Join: JoinMiter, Cap: CapButt, MiterLimit: 1, Tolerance: 0.1}
	contours, err := Stroke(path, opt)
	if err != nil {
		t.Fatal(err)
	}
	r := width / 2
	for _, p := range contours[0].Points {
		nearJoin := math.Abs(p.X-p1.X) < width && math.Abs(p.Y-p1.Y) < width
		if !nearJoin {
			continue
		}
		if d := dist(p, p1); d > r+1e-9 {
			t.Fatalf("miter limit violated: point %v is %v from corner (r=%v)", p, d, r)
		}
	}
}

func TestMiterRatioBoundary(t *testing.T) {
	// 30-degree turn: the SVG miter ratio is 1/sin(15deg) ~= 3.8637.
	p1 := Point{10, 0}
	dir := Point{math.Cos(math.Pi / 6), math.Sin(math.Pi / 6)}
	p2 := Point{p1.X + 10*dir.X, p1.Y + 10*dir.Y}
	path := Path{Segments: []Cubic{lineCubic(Point{0, 0}, p1), lineCubic(p1, p2)}}
	const width, r = 2.0, 1.0

	beveled, err := Stroke(path, Options{Width: width, Join: JoinMiter, Cap: CapButt, MiterLimit: 3, Tolerance: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range beveled[0].Points {
		if math.Abs(p.X-p1.X) < width && math.Abs(p.Y-p1.Y) < width && dist(p, p1) > r+1e-9 {
			t.Fatalf("limit 3 should bevel, but point %v protrudes", p)
		}
	}

	miters, err := Stroke(path, Options{Width: width, Join: JoinMiter, Cap: CapButt, MiterLimit: 4, Tolerance: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	// With limit 4 the miter survives: its outer tip is farther than r.
	foundTip := false
	for _, p := range miters[0].Points {
		if dist(p, p1) > r+0.5 {
			foundTip = true
		}
	}
	if !foundTip {
		t.Fatal("limit 4 should preserve the miter tip")
	}
}

func TestRoundJoinChordError(t *testing.T) {
	// Open L-path so the requested join only appears on the outer side; the
	// inner side of any turn is structurally a bevel.
	path := Path{Segments: []Cubic{
		lineCubic(Point{0, 0}, Point{10, 0}),
		lineCubic(Point{10, 0}, Point{10, 10}),
	}}
	const width, r = 4.0, 2.0
	coarse, _ := Stroke(path, Options{Width: width, Join: JoinRound, Cap: CapButt, MiterLimit: 4, Tolerance: 0.5})
	fine, _ := Stroke(path, Options{Width: width, Join: JoinRound, Cap: CapButt, MiterLimit: 4, Tolerance: 0.02})
	if len(coarse[0].Points) >= len(fine[0].Points) {
		t.Fatalf("round join must refine with tolerance: coarse=%d fine=%d",
			len(coarse[0].Points), len(fine[0].Points))
	}
	for _, c := range []Contour{coarse[0], fine[0]} {
		for _, p := range c.Points {
			// Outer round-join arc lives in the southeast quadrant.
			if p.X >= 10-r-1e-9 && p.Y <= 1e-9 && p.X <= 10+r+1e-9 && p.Y >= -r-1e-9 {
				if d := math.Abs(dist(p, Point{10, 0}) - r); d > 1e-9 {
					t.Fatalf("round join point off pivot circle by %v", d)
				}
			}
		}
	}
}

func TestExactReverseFoldbackRoundCaps(t *testing.T) {
	// Walk out and straight back along the same line: a 180-degree foldback.
	path := Path{Segments: []Cubic{
		lineCubic(Point{0, 0}, Point{10, 0}),
		lineCubic(Point{10, 0}, Point{0, 0}),
	}}
	opt := Options{Width: 2, Join: JoinMiter, Cap: CapButt, MiterLimit: 4, Tolerance: 0.1}
	contours, err := Stroke(path, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(contours) != 2 {
		t.Fatalf("foldback must split into two round-capped contours, got %d", len(contours))
	}
	centerline := []Point{{0, 0}, {10, 0}}
	for _, c := range contours {
		if signedArea(c.Points) >= 0 {
			t.Fatalf("foldback contour must be a directed loop, area=%v", signedArea(c.Points))
		}
		for _, p := range c.Points {
			if d := distanceToPolyline(p, centerline); math.Abs(d-1) > opt.Tolerance+1e-9 {
				t.Fatalf("foldback contour spur: distance %v", d)
			}
		}
	}
}

func TestCuspCubicFoldback(t *testing.T) {
	// Exact interior cusp: B'(0.5) = 0 and the two branches leave the cusp
	// in opposite tangent directions.
	seg := Cubic{
		P0: Point{0, 0},
		P1: Point{4, 0},
		P2: Point{4, 4},
		P3: Point{0, -4},
	}
	path := Path{Segments: []Cubic{seg}}
	opt := Options{Width: 2, Cap: CapButt, Tolerance: 0.1, MiterLimit: 4}
	contours, err := Stroke(path, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(contours) != 2 {
		t.Fatalf("interior cusp must split the stroke into two round-capped contours, got %d", len(contours))
	}
	for _, c := range contours {
		for _, p := range c.Points {
			// No point may protrude beyond half width from the actual
			// centerline; check against the true curve densely.
			best := math.Inf(1)
			for k := 0; k <= 400; k++ {
				best = math.Min(best, dist(p, evalCubic(seg, float64(k)/400)))
			}
			if best > 1+opt.Tolerance {
				t.Fatalf("cusp produced a spur at %v, curve distance %v", p, best)
			}
		}
	}
}

func TestZeroLengthSegment(t *testing.T) {
	point := Point{5, 5}
	zero := Cubic{P0: point, P1: point, P2: point, P3: point}

	// Entirely zero-length path: a round dot of radius half width.
	dot, err := Stroke(Path{Segments: []Cubic{zero}}, Options{Width: 4, Tolerance: 0.1})
	if err != nil || len(dot) != 1 {
		t.Fatalf("zero-length path: contours=%d err=%v", len(dot), err)
	}
	for _, p := range dot[0].Points {
		if d := math.Abs(dist(p, point) - 2); d > 0.11 {
			t.Fatalf("dot radius deviation %v", d)
		}
	}

	// Zero-width path of any shape yields no contours.
	empty, err := Stroke(Path{Segments: []Cubic{zero}}, Options{Width: 0, Tolerance: 0.1})
	if err != nil || len(empty) != 0 {
		t.Fatalf("zero width should yield no contours, got %d err=%v", len(empty), err)
	}

	// A zero-length segment embedded between real ones must not break output.
	mixed := Path{Segments: []Cubic{
		lineCubic(Point{0, 0}, Point{5, 0}),
		zero,
		lineCubic(Point{5, 0}, Point{10, 0}),
	}}
	mixedOut, err := Stroke(mixed, Options{Width: 2, Cap: CapButt, Tolerance: 0.1, MiterLimit: 4})
	if err != nil || len(mixedOut) != 1 {
		t.Fatalf("embedded zero segment: contours=%d err=%v", len(mixedOut), err)
	}
}

func TestAdaptiveSubdivisionIsErrorDriven(t *testing.T) {
	// A strongly curved cubic: looser tolerance must produce strictly fewer
	// flattened vertices, and every chord must respect the tolerance.
	seg := circleArcCubic(Point{}, 10, 0, math.Pi/2)

	count := func(tol float64) int {
		sub := subpath{segs: []Cubic{seg}}
		return len(flattenSubpath(sub, tol))
	}
	loose, tight := count(0.5), count(0.01)
	if loose >= tight {
		t.Fatalf("subdivision count must grow as tolerance shrinks: loose=%d tight=%d", loose, tight)
	}

	for _, tol := range []float64{0.5, 0.1, 0.01} {
		sub := subpath{segs: []Cubic{seg}}
		s := flattenSubpath(sub, tol)
		poly := make([]Point, len(s))
		for i, v := range s {
			poly[i] = v.point
		}
		// Dense sampling of the true curve against the flattened polyline:
		// the approximation error must stay within the requested tolerance.
		for k := 0; k <= 500; k++ {
			tt := float64(k) / 500
			exact := evalCubic(seg, tt)
			if d := distanceToPolyline(exact, poly); d > tol*1.05+1e-9 {
				t.Fatalf("tol=%v flatten error %v at t=%v exceeds tolerance", tol, d, tt)
			}
		}
	}
}

func evalCubic(c Cubic, t float64) Point {
	u := 1 - t
	b0, b1, b2, b3 := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
	return Point{
		X: b0*c.P0.X + b1*c.P1.X + b2*c.P2.X + b3*c.P3.X,
		Y: b0*c.P0.Y + b1*c.P1.Y + b2*c.P2.Y + b3*c.P3.Y,
	}
}

func TestInvalidOptions(t *testing.T) {
	valid := Options{Width: 1, Tolerance: 0.1, MiterLimit: 4}
	if _, err := Stroke(Path{}, valid); err != nil {
		t.Fatalf("valid options rejected: %v", err)
	}
	bad := []Options{
		{Width: -1, Tolerance: 0.1},
		{Width: 1, Tolerance: 0},
		{Width: 1, Tolerance: -0.1},
		{Width: 1, Tolerance: 0.1, MiterLimit: -2},
		{Width: math.NaN(), Tolerance: 0.1},
	}
	for i, opt := range bad {
		if _, err := Stroke(Path{}, opt); err == nil {
			t.Fatalf("case %d expected error for %+v", i, opt)
		}
	}
}

func TestMultipleSubpaths(t *testing.T) {
	path := Path{Segments: []Cubic{
		lineCubic(Point{0, 0}, Point{5, 0}),
		// Discontinuity: starts far from the previous endpoint.
		lineCubic(Point{20, 20}, Point{25, 20}),
	}}
	contours, err := Stroke(path, Options{Width: 2, Cap: CapButt, Tolerance: 0.1, MiterLimit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(contours) != 2 {
		t.Fatalf("two disconnected subpaths need two contours, got %d", len(contours))
	}
}

func TestClosedPolylineTwoDirectedLoops(t *testing.T) {
	path := Path{Segments: []Cubic{
		lineCubic(Point{0, 0}, Point{10, 0}),
		lineCubic(Point{10, 0}, Point{10, 10}),
		lineCubic(Point{10, 10}, Point{0, 10}),
		lineCubic(Point{0, 10}, Point{0, 0}),
	}, Closed: true}
	contours, err := Stroke(path, Options{Width: 2, Join: JoinMiter, Tolerance: 0.1, MiterLimit: 4})
	if err != nil || len(contours) != 2 {
		t.Fatalf("closed path needs outer+inner contours, got %d err=%v", len(contours), err)
	}
	outer, inner := contours[0], contours[1]
	if signedArea(outer.Points) >= 0 || signedArea(inner.Points) <= 0 {
		t.Fatalf("expected clockwise outer and CCW inner, got %v %v",
			signedArea(outer.Points), signedArea(inner.Points))
	}
	// Outer vertices stay at half width from the nearest centerline edge;
	// the inner boundary's corner bevels legitimately approach a vertex, so
	// assert the band on the outer loop only.
	edges := [][2]Point{
		{{0, 0}, {10, 0}}, {{10, 0}, {10, 10}},
		{{10, 10}, {0, 10}}, {{0, 10}, {0, 0}},
	}
	for _, p := range outer.Points {
		best := math.Inf(1)
		for _, e := range edges {
			best = math.Min(best, distanceToSegment(p, e[0], e[1]))
		}
		if math.Abs(best-1) > 1e-9 {
			t.Fatalf("outer half width deviation %v at %v", best, p)
		}
	}
}

func TestClosedPathSeamJoin(t *testing.T) {
	// The closing seam sits between the last and first segment; a round join
	// there must produce arc vertices on the pivot circle, not a miter.
	path := Path{Segments: []Cubic{
		lineCubic(Point{0, 0}, Point{10, 0}),
		lineCubic(Point{10, 0}, Point{10, 10}),
		lineCubic(Point{10, 10}, Point{0, 10}),
		lineCubic(Point{0, 10}, Point{0, 0}),
	}, Closed: true}
	const r = 1.5
	cs, err := Stroke(path, Options{Width: 2 * r, Join: JoinRound, Tolerance: 0.05, MiterLimit: 4})
	if err != nil || len(cs) != 2 {
		t.Fatalf("closed round-join path: contours=%d err=%v", len(cs), err)
	}
	pivot := Point{0, 0}
	arcPoints := 0
	for _, p := range cs[0].Points {
		if math.Abs(p.X) <= r+1e-9 && math.Abs(p.Y) <= r+1e-9 && dist(p, pivot) > r*0.6 {
			if d := math.Abs(dist(p, pivot) - r); d > 1e-9 {
				t.Fatalf("seam round join off pivot circle by %v at %v", d, p)
			}
			arcPoints++
		}
	}
	if arcPoints < 3 {
		t.Fatalf("seam round join should add arc vertices, found %d", arcPoints)
	}
}

func TestRoundArcSagittaBounded(t *testing.T) {
	// A semicircle centerline built from two cubic arcs; every outline point
	// must respect the requested half width against the true arc reference.
	center := Point{0, 10}
	path := Path{Segments: []Cubic{circleArcCubic(center, 10, -math.Pi/2, math.Pi/2)}}
	for _, tol := range []float64{0.2, 0.02} {
		cs, err := Stroke(path, Options{Width: 2, Cap: CapRound, Tolerance: tol, MiterLimit: 4})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			for _, p := range c.Points {
				radial := math.Hypot(p.X-center.X, p.Y-center.Y)
				// Points on the two offset bands sit at R +/- r; points on
				// the round end caps lie between the bands by construction.
				outerErr := math.Abs(radial - 11)
				innerErr := math.Abs(radial - 9)
				best := math.Min(outerErr, innerErr)
				if radial > 11+tol || radial < 9-tol {
					t.Fatalf("tol=%v radial %v outside stroked annulus (best band err %v)", tol, radial, best)
				}
			}
		}
	}
}
