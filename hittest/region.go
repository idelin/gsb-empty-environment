package hittest

import "math"

// Point is a point in an arbitrary 2D coordinate space.
type Point struct {
	X, Y float64
}

// Rect is an axis-aligned rectangle using half-open bounds [Min, Max).
// A rect is empty when Min.X >= Max.X or Min.Y >= Max.Y.
type Rect struct {
	Min, Max Point
}

// R is shorthand for constructing an axis-aligned Rect.
func R(x0, y0, x1, y1 float64) Rect {
	return Rect{Min: Point{X: x0, Y: y0}, Max: Point{X: x1, Y: y1}}
}

// Empty reports whether the rectangle contains no points.
func (r Rect) Empty() bool {
	return r.Max.X <= r.Min.X || r.Max.Y <= r.Min.Y
}

// Contains reports whether p lies inside the half-open rectangle.
func (r Rect) Contains(p Point) bool {
	return p.X >= r.Min.X && p.X < r.Max.X && p.Y >= r.Min.Y && p.Y < r.Max.Y
}

// Intersect returns the overlap of r and s; the zero Rect is returned when
// the rectangles do not overlap.
func (r Rect) Intersect(s Rect) Rect {
	out := Rect{
		Min: Point{X: math.Max(r.Min.X, s.Min.X), Y: math.Max(r.Min.Y, s.Min.Y)},
		Max: Point{X: math.Min(r.Max.X, s.Max.X), Y: math.Min(r.Max.Y, s.Max.Y)},
	}
	if out.Empty() {
		return Rect{}
	}
	return out
}

// Transform is an affine 2D transform of the form p' = p*Scale + Offset.
// Only per-axis scale plus translation is modeled, which covers window
// placement and HiDPI/fractional scaling while staying exactly invertible
// for any non-zero scale.
type Transform struct {
	ScaleX float64
	ScaleY float64
	Offset Point
}

// Identity is the transform that leaves every point unchanged.
func Identity() Transform {
	return Transform{ScaleX: 1, ScaleY: 1}
}

// Translate returns a transform that shifts points by (dx, dy).
func Translate(dx, dy float64) Transform {
	return Transform{ScaleX: 1, ScaleY: 1, Offset: Point{X: dx, Y: dy}}
}

// Scale returns a transform that scales points by (sx, sy) about the origin.
func Scale(sx, sy float64) Transform {
	return Transform{ScaleX: sx, ScaleY: sy}
}

// normalized treats the zero-value Transform as identity, so plain nodes do
// not have to populate their Transform field. An explicitly zero scale with a
// non-zero offset remains singular and is rejected by Invert.
func (t Transform) normalized() Transform {
	if t.ScaleX == 0 && t.ScaleY == 0 && t.Offset == (Point{}) {
		return Identity()
	}
	return t
}

// Apply maps p through the transform.
func (t Transform) Apply(p Point) Point {
	t = t.normalized()
	return Point{X: p.X*t.ScaleX + t.Offset.X, Y: p.Y*t.ScaleY + t.Offset.Y}
}

// IsZero reports whether t is the zero-value transform, which behaves as
// identity.
func (t Transform) IsZero() bool {
	return t.ScaleX == 0 && t.ScaleY == 0 && t.Offset == (Point{})
}

// ApplyRect maps r through the transform, normalizing it when the scale is
// negative.
func (t Transform) ApplyRect(r Rect) Rect {
	a := t.Apply(r.Min)
	b := t.Apply(r.Max)
	return Rect{
		Min: Point{X: math.Min(a.X, b.X), Y: math.Min(a.Y, b.Y)},
		Max: Point{X: math.Max(a.X, b.X), Y: math.Max(a.Y, b.Y)},
	}
}

// Invert returns the inverse transform. It panics when either scale axis is
// zero, since such a transform collapses points and cannot be reversed.
func (t Transform) Invert() Transform {
	t = t.normalized()
	if t.ScaleX == 0 || t.ScaleY == 0 {
		panic("hittest: singular transform has no inverse")
	}
	return Transform{
		ScaleX: 1 / t.ScaleX,
		ScaleY: 1 / t.ScaleY,
		Offset: Point{X: -t.Offset.X / t.ScaleX, Y: -t.Offset.Y / t.ScaleY},
	}
}

// Then composes transforms: t.Then(u) applies t first and u second, matching
// the conventional mathematical reading. In matrix terms the result is u∘t.
func (t Transform) Then(u Transform) Transform {
	t = t.normalized()
	u = u.normalized()
	return Transform{
		ScaleX: t.ScaleX * u.ScaleX,
		ScaleY: t.ScaleY * u.ScaleY,
		Offset: Point{
			X: u.ScaleX*t.Offset.X + u.Offset.X,
			Y: u.ScaleY*t.Offset.Y + u.Offset.Y,
		},
	}
}

// Region is an immutable set of 2D points represented as the union of
// pairwise disjoint, non-empty axis-aligned rectangles. Storing shapes as
// rectangle unions lets windows carry genuinely non-rectangular shapes
// (L-shapes, crosses, holes) without rasterization.
type Region struct {
	rects []Rect
}

// EmptyRegion is the region containing no points.
var EmptyRegion = Region{}

// NewRegion builds a canonical region from the given rectangles. Empty and
// overlapping inputs are merged into a minimal set of disjoint rectangles.
func NewRegion(rects ...Rect) Region {
	var kept []Rect
	for _, r := range rects {
		if !r.Empty() {
			kept = append(kept, r)
		}
	}
	return canonicalize(kept)
}

// RectRegion wraps a single rectangle.
func RectRegion(r Rect) Region {
	if r.Empty() {
		return EmptyRegion
	}
	return Region{rects: []Rect{r}}
}

// IsEmpty reports whether the region contains no points.
func (g Region) IsEmpty() bool {
	return len(g.rects) == 0
}

// Rects returns the canonical disjoint rectangles making up the region.
// Callers must not modify the returned slice.
func (g Region) Rects() []Rect {
	return g.rects
}

// Bounds returns the smallest axis-aligned rectangle enclosing the region,
// or the zero Rect when the region is empty.
func (g Region) Bounds() Rect {
	if len(g.rects) == 0 {
		return Rect{}
	}
	b := g.rects[0]
	for _, r := range g.rects[1:] {
		b.Min.X = math.Min(b.Min.X, r.Min.X)
		b.Min.Y = math.Min(b.Min.Y, r.Min.Y)
		b.Max.X = math.Max(b.Max.X, r.Max.X)
		b.Max.Y = math.Max(b.Max.Y, r.Max.Y)
	}
	return b
}

// Contains reports whether p belongs to the region.
func (g Region) Contains(p Point) bool {
	for _, r := range g.rects {
		if r.Contains(p) {
			return true
		}
	}
	return false
}

// Union returns the region covering every point in either g or h.
func (g Region) Union(h Region) Region {
	all := make([]Rect, 0, len(g.rects)+len(h.rects))
	all = append(all, g.rects...)
	all = append(all, h.rects...)
	return canonicalize(all)
}

// Intersect returns the region covering only the points in both g and h.
func (g Region) Intersect(h Region) Region {
	var out []Rect
	for _, a := range g.rects {
		for _, b := range h.rects {
			if c := a.Intersect(b); !c.Empty() {
				out = append(out, c)
			}
		}
	}
	return canonicalize(out)
}

// Transform maps the region through t.
func (g Region) Transform(t Transform) Region {
	if t.IsZero() {
		return g
	}
	out := make([]Rect, len(g.rects))
	for i, r := range g.rects {
		out[i] = t.ApplyRect(r)
	}
	return canonicalize(out)
}

// Equals reports whether g and h cover exactly the same points. Both regions
// are canonical, so structural equality is geometric equality.
func (g Region) Equals(h Region) bool {
	if len(g.rects) != len(h.rects) {
		return false
	}
	for i := range g.rects {
		if g.rects[i] != h.rects[i] {
			return false
		}
	}
	return true
}

// canonicalize merges rectangles into the minimal union of disjoint
// rectangles. The plane is sliced at every rectangle's Y edge; within each
// horizontal band the active X intervals are merged, and adjacent bands with
// identical X spans are joined vertically.
func canonicalize(in []Rect) Region {
	if len(in) == 0 {
		return EmptyRegion
	}

	cutSet := make(map[float64]struct{}, len(in)*2)
	for _, r := range in {
		if r.Empty() {
			continue
		}
		cutSet[r.Min.Y] = struct{}{}
		cutSet[r.Max.Y] = struct{}{}
	}
	if len(cutSet) == 0 {
		return EmptyRegion
	}

	cuts := make([]float64, 0, len(cutSet))
	for y := range cutSet {
		cuts = append(cuts, y)
	}
	sortFloats(cuts)

	var bands []yBand
	for i := 0; i+1 < len(cuts); i++ {
		y0, y1 := cuts[i], cuts[i+1]
		if y1 <= y0 {
			continue
		}
		var spans []xSpan
		for _, r := range in {
			if r.Empty() {
				continue
			}
			if r.Min.Y <= y0 && r.Max.Y >= y1 {
				spans = append(spans, xSpan{x0: r.Min.X, x1: r.Max.X})
			}
		}
		bands = append(bands, yBand{y0: y0, y1: y1, spans: mergeXSpans(spans)})
	}

	var out []Rect
	for _, b := range bands {
		for _, sp := range b.spans {
			if n := len(out); n > 0 {
				prev := out[n-1]
				if prev.Max.Y == b.y0 && prev.Min.X == sp.x0 && prev.Max.X == sp.x1 {
					out[n-1].Max.Y = b.y1
					continue
				}
			}
			out = append(out, R(sp.x0, b.y0, sp.x1, b.y1))
		}
	}
	return Region{rects: out}
}

type xSpan struct {
	x0, x1 float64
}

type yBand struct {
	y0, y1 float64
	spans  []xSpan
}

// mergeXSpans unions overlapping or touching X intervals.
func mergeXSpans(spans []xSpan) []xSpan {
	if len(spans) == 0 {
		return nil
	}
	sortSpans(spans)
	merged := []xSpan{spans[0]}
	for _, s := range spans[1:] {
		cur := &merged[len(merged)-1]
		if s.x0 <= cur.x1 {
			if s.x1 > cur.x1 {
				cur.x1 = s.x1
			}
			continue
		}
		merged = append(merged, s)
	}
	return merged
}

func sortFloats(v []float64) {
	// Insertion sort keeps the dependency surface at the standard library
	// only; inputs are small in compositor hit-testing workloads.
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j-1] > v[j]; j-- {
			v[j-1], v[j] = v[j], v[j-1]
		}
	}
}

func sortSpans(v []xSpan) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j-1].x0 > v[j].x0; j-- {
			v[j-1], v[j] = v[j], v[j-1]
		}
	}
}
