package taskpkg

import "math"

// Point is a 2D point or vector.
type Point struct{ X, Y float64 }

func (p Point) Add(q Point) Point     { return Point{p.X + q.X, p.Y + q.Y} }
func (p Point) Sub(q Point) Point     { return Point{p.X - q.X, p.Y - q.Y} }
func (p Point) Scale(s float64) Point { return Point{p.X * s, p.Y * s} }
func (p Point) Dot(q Point) float64   { return p.X*q.X + p.Y*q.Y }
func (p Point) Len() float64          { return math.Hypot(p.X, p.Y) }

// SegKind identifies the type of a Segment.
type SegKind int

const (
	// LineSeg is a straight segment from P0 to P1.
	LineSeg SegKind = iota
	// QuadSeg is a quadratic Bézier from P0 to P1 with control point C.
	QuadSeg
)

// Segment is one edge of a closed contour. Consecutive segments are expected
// to share endpoints (seg[i].P1 == seg[i+1].P0) and the last segment's P1 to
// coincide with the first segment's P0.
type Segment struct {
	Kind      SegKind
	P0, C, P1 Point
}

// Line returns a straight segment from p0 to p1.
func Line(p0, p1 Point) Segment { return Segment{Kind: LineSeg, P0: p0, P1: p1} }

// Quad returns a quadratic Bézier segment from p0 to p1 with control point c.
func Quad(p0, c, p1 Point) Segment { return Segment{Kind: QuadSeg, P0: p0, C: c, P1: p1} }

// Contour is a closed loop of segments.
type Contour struct {
	Segments []Segment
}

// Shape is a glyph outline: a set of closed contours. Filling follows the
// non-zero winding rule, so holes are expressed as contours wound opposite
// to their surrounding contour.
type Shape struct {
	Contours []Contour
}

// eval returns the point on the segment at parameter t in [0,1].
func (s Segment) eval(t float64) Point {
	if s.Kind == QuadSeg {
		u := 1 - t
		return s.P0.Scale(u * u).Add(s.C.Scale(2 * u * t)).Add(s.P1.Scale(t * t))
	}
	return s.P0.Scale(1 - t).Add(s.P1.Scale(t))
}

// distSq returns the squared Euclidean distance from p to the segment.
// For quadratic Béziers it minimises |B(t)-p| over t in [0,1] by solving the
// cubic derivative polynomial B'(t)·(B(t)-p) = 0 and evaluating every real
// root inside the interval, in addition to both endpoints.
func (s Segment) distSq(p Point) float64 {
	if s.Kind != QuadSeg {
		return lineDistSq(s.P0, s.P1, p)
	}
	// B(t) = a t^2 + b t + P0
	a := s.P0.Sub(s.C.Scale(2)).Add(s.P1)
	b := s.C.Sub(s.P0).Scale(2)
	if a.Dot(a) == 0 {
		// Control point is the midpoint: the curve is a straight line.
		return lineDistSq(s.P0, s.P1, p)
	}
	d := s.P0.Sub(p)
	// f(t) = B'(t)·(B(t)-p) = c3 t^3 + c2 t^2 + c1 t + c0
	c3 := 2 * a.Dot(a)
	c2 := 3 * a.Dot(b)
	c1 := 2*a.Dot(d) + b.Dot(b)
	c0 := b.Dot(d)
	best := math.Min(d.Dot(d), s.P1.Sub(p).Dot(s.P1.Sub(p)))
	for _, t := range cubicRootsOpen01(c3, c2, c1, c0) {
		q := s.eval(t).Sub(p)
		best = math.Min(best, q.Dot(q))
	}
	return best
}

// lineDistSq returns the squared distance from p to the segment a-b.
func lineDistSq(a, b, p Point) float64 {
	ab := b.Sub(a)
	l2 := ab.Dot(ab)
	if l2 == 0 {
		return p.Sub(a).Dot(p.Sub(a))
	}
	t := p.Sub(a).Dot(ab) / l2
	t = math.Max(0, math.Min(1, t))
	q := a.Add(ab.Scale(t)).Sub(p)
	return q.Dot(q)
}

// winding returns the segment's contribution to the winding number at p,
// counting signed crossings of the horizontal ray from p towards +x.
// Parameter roots use the half-open interval (0,1] so vertices shared by
// consecutive segments are counted exactly once.
func (s Segment) winding(p Point) int {
	if s.Kind != QuadSeg {
		dy := s.P1.Y - s.P0.Y
		if dy == 0 {
			return 0
		}
		t := (p.Y - s.P0.Y) / dy
		if t <= 0 || t > 1 {
			return 0
		}
		if s.P0.X+t*(s.P1.X-s.P0.X) <= p.X {
			return 0
		}
		if dy > 0 {
			return 1
		}
		return -1
	}
	// y(t) = ay t^2 + by t + P0.Y
	ay := s.P0.Y - 2*s.C.Y + s.P1.Y
	by := 2 * (s.C.Y - s.P0.Y)
	var roots []float64
	switch {
	case ay == 0 && by == 0:
		return 0 // horizontal curve
	case ay == 0:
		roots = []float64{(p.Y - s.P0.Y) / by}
	default:
		roots = quadRoots(ay, by, s.P0.Y-p.Y)
	}
	wn := 0
	for _, t := range roots {
		if t <= 0 || t > 1 {
			continue
		}
		dy := 2*ay*t + by
		if dy == 0 {
			continue // tangent touch: curve stays on one side, no crossing
		}
		if s.eval(t).X <= p.X {
			continue
		}
		if dy > 0 {
			wn++
		} else {
			wn--
		}
	}
	return wn
}

// windingNumber returns the total winding number of the shape at p.
func (s Shape) windingNumber(p Point) int {
	wn := 0
	for _, c := range s.Contours {
		for _, seg := range c.Segments {
			wn += seg.winding(p)
		}
	}
	return wn
}

// SignedDistance returns the signed Euclidean distance from p to the shape's
// outline together with the index of the contour containing the closest
// point. The distance is negative where the non-zero winding rule fills the
// shape (inside) and positive outside. A shape with no segments at all
// yields +Inf and contour ID -1.
func (s Shape) SignedDistance(p Point) (float64, int) {
	best := math.Inf(1)
	id := -1
	for i, c := range s.Contours {
		for _, seg := range c.Segments {
			if d := seg.distSq(p); d < best {
				best = d
				id = i
			}
		}
	}
	if id < 0 {
		return math.Inf(1), -1
	}
	dist := math.Sqrt(best)
	if s.windingNumber(p) != 0 {
		dist = -dist
	}
	return dist, id
}

// Cell holds the SDF sample at one pixel centre.
type Cell struct {
	Distance  float64 // signed distance, negative inside
	ContourID int     // index of the nearest contour, -1 if none
}

// Rasterize evaluates the signed distance field on a width*height pixel grid.
// The pixel (x, y) is sampled at its centre,
// origin + (x+0.5, y+0.5)*pixelSize. Cells are row-major: cells[y*width+x].
func Rasterize(s Shape, origin Point, pixelSize float64, width, height int) []Cell {
	cells := make([]Cell, width*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			p := Point{
				origin.X + (float64(x)+0.5)*pixelSize,
				origin.Y + (float64(y)+0.5)*pixelSize,
			}
			d, id := s.SignedDistance(p)
			cells[y*width+x] = Cell{Distance: d, ContourID: id}
		}
	}
	return cells
}
