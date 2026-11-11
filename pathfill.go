package pathfill

import "math"

// FillRule selects how overlapping contour regions are combined.
type FillRule int

const (
	// FillNonZero fills a point whenever its winding number is non-zero.
	FillNonZero FillRule = iota
	// FillEvenOdd fills a point whenever an odd number of contours enclose it.
	FillEvenOdd
)

// Point is a point in user (pixel) coordinate space. Pixel (x, y) occupies
// the unit square [x, x+1) x [y, y+1); fractional coordinates are allowed and
// coordinates may lie outside the canvas.
type Point struct {
	X, Y float64
}

// Path is a collection of subpaths (contours). Subpaths are implicitly closed
// when the path is rasterized; an explicit Close is only needed to start a new
// subpath without an implicit closing segment.
type Path struct {
	verbs  []verb
	points []Point

	start  Point // first point of the current subpath
	cur    Point // current point of the current subpath
	active bool  // whether a subpath is in progress
}

type verb uint8

const (
	verbMove verb = iota
	verbLine
	verbQuad
	verbCubic
	verbClose
)

// MoveTo begins a new subpath at p. Any subpath in progress is implicitly
// closed first.
func (p *Path) MoveTo(pt Point) {
	if p.active {
		p.verbs = append(p.verbs, verbClose)
	}
	p.verbs = append(p.verbs, verbMove)
	p.points = append(p.points, pt)
	p.start, p.cur, p.active = pt, pt, true
}

// LineTo appends a straight segment from the current point to pt.
func (p *Path) LineTo(pt Point) {
	p.verbs = append(p.verbs, verbLine)
	p.points = append(p.points, pt)
	p.cur = pt
}

// QuadTo appends a quadratic Bézier from the current point through control to
// end.
func (p *Path) QuadTo(control, end Point) {
	p.verbs = append(p.verbs, verbQuad)
	p.points = append(p.points, control, end)
	p.cur = end
}

// CubicTo appends a cubic Bézier from the current point through c1 and c2 to
// end.
func (p *Path) CubicTo(c1, c2, end Point) {
	p.verbs = append(p.verbs, verbCubic)
	p.points = append(p.points, c1, c2, end)
	p.cur = end
}

// Close ends the current subpath with a straight segment back to its starting
// point. The next drawing command begins a new subpath.
func (p *Path) Close() {
	if !p.active {
		return
	}
	p.verbs = append(p.verbs, verbClose)
	p.active = false
}

// Reset removes all path content.
func (p *Path) Reset() {
	p.verbs = p.verbs[:0]
	p.points = p.points[:0]
	p.start, p.cur, p.active = Point{}, Point{}, false
}

// flatten converts the path into closed contours made of line segments. Every
// Bézier segment is subdivided until the maximum deviation of its control
// polygon from the chord is below tolerance.
func flatten(path *Path, tolerance float64) [][]Point {
	if tolerance <= 0 {
		tolerance = 0.25
	}
	var contours [][]Point
	var cur []Point
	var start Point
	pi := 0

	flush := func() {
		if len(cur) > 1 {
			if cur[len(cur)-1] != cur[0] {
				cur = append(cur, cur[0])
			}
			contours = append(contours, cur)
		}
		cur = nil
	}

	for _, v := range path.verbs {
		switch v {
		case verbMove:
			flush()
			pt := path.points[pi]
			pi++
			start, cur = pt, append(cur, pt)
		case verbLine:
			pt := path.points[pi]
			pi++
			cur = append(cur, pt)
		case verbQuad:
			control := path.points[pi]
			end := path.points[pi+1]
			pi += 2
			from := cur[len(cur)-1]
			cur = append(cur, flattenQuad(from, control, end, tolerance, 0)...)
		case verbCubic:
			c1 := path.points[pi]
			c2 := path.points[pi+1]
			end := path.points[pi+2]
			pi += 3
			from := cur[len(cur)-1]
			cur = append(cur, flattenCubic(from, c1, c2, end, tolerance, 0)...)
		case verbClose:
			if len(cur) > 0 {
				cur = append(cur, start)
			}
			flush()
		}
	}
	flush()
	return contours
}

const maxFlattenDepth = 18

func flattenQuad(p0, p1, p2 Point, tol float64, depth int) []Point {
	if depth >= maxFlattenDepth || quadFlat(p0, p1, p2) <= tol {
		return []Point{p2}
	}
	p01 := midpoint(p0, p1)
	p12 := midpoint(p1, p2)
	p012 := midpoint(p01, p12)
	left := flattenQuad(p0, p01, p012, tol, depth+1)
	right := flattenQuad(p012, p12, p2, tol, depth+1)
	return append(left, right...)
}

func flattenCubic(p0, p1, p2, p3 Point, tol float64, depth int) []Point {
	if depth >= maxFlattenDepth || cubicFlat(p0, p1, p2, p3) <= tol {
		return []Point{p3}
	}
	p01 := midpoint(p0, p1)
	p12 := midpoint(p1, p2)
	p23 := midpoint(p2, p3)
	p012 := midpoint(p01, p12)
	p123 := midpoint(p12, p23)
	p0123 := midpoint(p012, p123)
	left := flattenCubic(p0, p01, p012, p0123, tol, depth+1)
	right := flattenCubic(p0123, p123, p23, p3, tol, depth+1)
	return append(left, right...)
}

// quadFlat returns the perpendicular distance from the control point to the
// chord, an upper bound on the deviation of the quadratic curve from the
// chord (the true maximum is half of it).
func quadFlat(p0, p1, p2 Point) float64 {
	return pointLineDistance(p1, p0, p2)
}

// cubicFlat returns the maximum perpendicular distance of either control point
// from the chord, the standard flatness estimate for cubic curves.
func cubicFlat(p0, p1, p2, p3 Point) float64 {
	d1 := pointLineDistance(p1, p0, p3)
	d2 := pointLineDistance(p2, p0, p3)
	if d1 > d2 {
		return d1
	}
	return d2
}

func pointLineDistance(p, a, b Point) float64 {
	dx, dy := b.X-a.X, b.Y-a.Y
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return math.Hypot(p.X-a.X, p.Y-a.Y)
	}
	return math.Abs(dy*p.X-dx*p.Y+b.X*a.Y-b.Y*a.X) / math.Sqrt(l2)
}

func midpoint(a, b Point) Point {
	return Point{X: (a.X + b.X) * 0.5, Y: (a.Y + b.Y) * 0.5}
}
