package taskpkg

import "math"

const geometryEpsilon = 1e-9

// Point is a point in page coordinates. The Y axis points down, as is usual
// for raster page coordinates.
type Point struct {
	X float64
	Y float64
}

// Quad is a four-corner word box. The conventional order is top-left,
// top-right, bottom-right, bottom-left, but the line builder does not depend on
// that order: it derives the bounding interval and the lowest edge.
type Quad [4]Point

// Box is an alias for Quad, useful to hosts that call the value a box.
type Box = Quad

// Bounds is an axis-aligned interval.
type Bounds struct {
	MinX float64
	MinY float64
	MaxX float64
	MaxY float64
}

// Width returns the horizontal extent.
func (b Bounds) Width() float64 {
	return b.MaxX - b.MinX
}

// Height returns the vertical extent.
func (b Bounds) Height() float64 {
	return b.MaxY - b.MinY
}

// CenterX returns the horizontal center.
func (b Bounds) CenterX() float64 {
	return (b.MinX + b.MaxX) / 2
}

// Bounds returns the axis-aligned bounds of q.
func (q Quad) Bounds() Bounds {
	b := Bounds{
		MinX: q[0].X,
		MinY: q[0].Y,
		MaxX: q[0].X,
		MaxY: q[0].Y,
	}
	for _, p := range q[1:] {
		b.MinX = math.Min(b.MinX, p.X)
		b.MinY = math.Min(b.MinY, p.Y)
		b.MaxX = math.Max(b.MaxX, p.X)
		b.MaxY = math.Max(b.MaxY, p.Y)
	}
	return b
}

// NormalizeQuad rotates q around the page origin by -degrees, removing the
// supplied page rotation. Positive degrees denote a clockwise page rotation.
func NormalizeQuad(q Quad, degrees float64) Quad {
	if degrees == 0 {
		return q
	}
	radians := -degrees * math.Pi / 180
	sin, cos := math.Sin(radians), math.Cos(radians)
	var out Quad
	for i, p := range q {
		out[i] = Point{
			X: p.X*cos - p.Y*sin,
			Y: p.X*sin + p.Y*cos,
		}
	}
	return out
}

// Baseline returns the midpoint of the two lowest corners' Y coordinates.
// Using the midpoint, rather than the maximum Y, avoids overestimating the
// baseline of a slightly rotated word and does not depend on corner order.
func Baseline(q Quad) float64 {
	first, second := q[0].Y, q[1].Y
	if first < second {
		first, second = second, first
	}
	for _, p := range q[2:] {
		switch {
		case p.Y > first:
			second = first
			first = p.Y
		case p.Y > second:
			second = p.Y
		}
	}
	return (first + second) / 2
}

func validPoint(p Point) bool {
	return !math.IsNaN(p.X) && !math.IsInf(p.X, 0) &&
		!math.IsNaN(p.Y) && !math.IsInf(p.Y, 0)
}

func validQuad(q Quad) bool {
	for _, p := range q {
		if !validPoint(p) {
			return false
		}
	}
	return true
}

func boxArea(b Bounds) float64 {
	return math.Max(0, b.Width()) * math.Max(0, b.Height())
}

func intersectionArea(a, b Bounds) float64 {
	w := math.Min(a.MaxX, b.MaxX) - math.Max(a.MinX, b.MinX)
	h := math.Min(a.MaxY, b.MaxY) - math.Max(a.MinY, b.MinY)
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}

func nearlyEqual(a, b float64) bool {
	scale := math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
	return math.Abs(a-b) <= geometryEpsilon*scale
}

func clampRatio(v, fallback float64) float64 {
	if v == 0 {
		return fallback
	}
	return v
}
