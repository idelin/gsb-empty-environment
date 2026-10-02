// Package delaunay builds a Delaunay triangulation of a 2D point set.
//
// The triangulation is constructed incrementally: points are inserted one at
// a time into a triangle containing them, and the mesh is restored to a
// legal (Delaunay) state locally with Lawson's edge-flip algorithm. The
// result satisfies the empty circumcircle property: no input point lies
// strictly inside the circumcircle of any triangle.
package delaunay

import "math"

// Point is a 2D vertex.
type Point struct {
	X, Y float64
}

// Triangle is a compact topological record.
//
//	V[i]   vertex indices in counter-clockwise order
//	N[i]   index of the neighbour triangle across the edge opposite V[i],
//	       or -1 when that edge lies on the convex hull boundary
type Triangle struct {
	V [3]int
	N [3]int
}

// Triangulation is the finished, compacted mesh: vertices plus triangles
// with full adjacency information.
type Triangulation struct {
	Points []Point
	Tris   []Triangle
}

// Builder incrementally maintains a Delaunay triangulation. Points may be
// added one at a time with Insert; the current mesh can be extracted at any
// moment with Triangulation.
type Builder struct {
	pts    []Point // pts[0:3] are the super-triangle vertices
	tris   []Triangle
	span   float64 // characteristic length of the point set, for tolerances
	margin float64 // super-triangle size as a multiple of span
}

// NewBuilder returns a Builder preloaded with the optional seed points.
func NewBuilder(seed ...Point) *Builder {
	b := &Builder{margin: 64}
	b.reset(seed)
	for _, p := range seed {
		b.Insert(p)
	}
	return b
}

// reset (re)initialises the builder with a super-triangle that comfortably
// contains all given points.
func (b *Builder) reset(pts []Point) {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		minX, minY = math.Min(minX, p.X), math.Min(minY, p.Y)
		maxX, maxY = math.Max(maxX, p.X), math.Max(maxY, p.Y)
	}
	if len(pts) == 0 {
		minX, minY, maxX, maxY = 0, 0, 0, 0
	}
	span := math.Max(maxX-minX, maxY-minY)
	if span <= 0 {
		span = 1
	}
	b.span = span
	cx, cy := (minX+maxX)/2, (minY+maxY)/2
	m := b.margin * span
	b.pts = []Point{
		{cx - m, cy - 0.625*m},
		{cx + m, cy - 0.625*m},
		{cx, cy + m},
	}
	b.tris = []Triangle{{V: [3]int{0, 1, 2}, N: [3]int{-1, -1, -1}}}
}

func (b *Builder) epsArea() float64 { return 1e-12 * b.span * b.span }
func (b *Builder) epsDist() float64 { return 1e-9 * b.span }

// Insert adds p to the triangulation and returns its vertex index.
// A point coinciding with an existing vertex is ignored; the index of that
// vertex is returned instead.
func (b *Builder) Insert(p Point) int {
	for i := 3; i < len(b.pts); i++ {
		if math.Hypot(p.X-b.pts[i].X, p.Y-b.pts[i].Y) <= b.epsDist() {
			return i - 3
		}
	}
	if ti, edge := b.locate(p); ti >= 0 {
		b.pts = append(b.pts, p)
		if edge >= 0 {
			b.splitEdge(ti, edge)
		} else {
			b.splitTriangle(ti)
		}
		return len(b.pts) - 4
	}
	// p lies outside the current super-triangle: rebuild with a frame
	// that contains every point seen so far, then re-insert all of them.
	b.rebuild(append(b.realPoints(), p))
	return len(b.pts) - 4
}

// realPoints returns the inserted points, excluding the super-triangle.
func (b *Builder) realPoints() []Point {
	return append([]Point(nil), b.pts[3:]...)
}

// rebuild resets the builder and re-inserts all given points.
func (b *Builder) rebuild(all []Point) {
	b.reset(all)
	for _, p := range all {
		b.insert(p)
	}
}

// hullDent reports whether the boundary of the mesh dents inward, i.e. fails
// to be the convex hull of the input points. Boundary triangles with nearly
// collinear vertices have huge circumcircles that can reach a super-triangle
// vertex; the affected triangles then go missing and the boundary dents. On
// a dent, the circumradius of the largest missing (prev, cur, next) triple
// is returned so the super-triangle can be resized beyond it.
func (t *Triangulation) hullDent() (radius float64, dented bool) {
	hull := t.ConvexHull()
	if len(hull) < 3 {
		return 0, false
	}
	span := 0.0
	for _, p := range t.Points {
		span = math.Max(span, math.Max(math.Abs(p.X), math.Abs(p.Y)))
	}
	eps := 1e-9 * span * span
	n := len(hull)
	for i, v := range hull {
		a := t.Points[hull[(i-1+n)%n]]
		b := t.Points[v]
		c := t.Points[hull[(i+1)%n]]
		if orient(a, b, c) < -eps {
			_, r2 := Circumcircle(a, b, c)
			if r := math.Sqrt(r2); r > radius {
				radius = r
			}
			dented = true
		}
	}
	return radius, dented
}

// insert adds a point known to lie inside the super-triangle.
func (b *Builder) insert(p Point) {
	ti, edge := b.locate(p)
	if ti < 0 {
		return // unreachable: super-triangle contains all points
	}
	b.pts = append(b.pts, p)
	if edge >= 0 {
		b.splitEdge(ti, edge)
	} else {
		b.splitTriangle(ti)
	}
}

// locate finds the triangle containing p. It returns the triangle index and
// the index of the edge p lies on (edge i is opposite vertex V[i]), or -1
// when p is strictly interior. Returns (-1, -1) when p is outside the mesh.
func (b *Builder) locate(p Point) (tri int, edgeOn int) {
	eps := b.epsArea()
	for t := range b.tris {
		v := b.tris[t].V
		o0 := orient(b.pts[v[0]], b.pts[v[1]], p)
		o1 := orient(b.pts[v[1]], b.pts[v[2]], p)
		o2 := orient(b.pts[v[2]], b.pts[v[0]], p)
		if o0 < -eps || o1 < -eps || o2 < -eps {
			continue
		}
		switch {
		case math.Abs(o1) <= eps:
			return t, 0
		case math.Abs(o2) <= eps:
			return t, 1
		case math.Abs(o0) <= eps:
			return t, 2
		}
		return t, -1
	}
	return -1, -1
}

// splitTriangle inserts the most recently appended point, which lies
// strictly inside triangle t, by splitting t into three triangles and
// legalising the three affected edges.
func (b *Builder) splitTriangle(t int) {
	pi := len(b.pts) - 1
	tr := b.tris[t]
	a, bb, c := tr.V[0], tr.V[1], tr.V[2]
	t1 := b.addTriangle()
	t2 := b.addTriangle()
	b.tris[t] = Triangle{V: [3]int{pi, bb, c}, N: [3]int{tr.N[0], t1, t2}}
	b.tris[t1] = Triangle{V: [3]int{a, pi, c}, N: [3]int{t, tr.N[1], t2}}
	b.tris[t2] = Triangle{V: [3]int{a, bb, pi}, N: [3]int{t, t1, tr.N[2]}}
	b.setBackRef(tr.N[1], t, t1)
	b.setBackRef(tr.N[2], t, t2)
	b.rotate(t1, 1) // move pi to V[0]
	b.rotate(t2, 2)
	b.legalize(t)
	b.legalize(t1)
	b.legalize(t2)
}

// splitEdge inserts the most recently appended point, which lies on edge e
// of triangle t, by splitting the two triangles sharing that edge into four
// and legalising the four affected edges.
func (b *Builder) splitEdge(t, e int) {
	pi := len(b.pts) - 1
	b.rotate(t, e) // shared edge is now opposite V[0]
	s := b.tris[t].N[0]
	for j := 0; j < 3; j++ {
		if b.tris[s].N[j] == t {
			b.rotate(s, j)
			break
		}
	}
	tr, sr := b.tris[t], b.tris[s]
	// tr = (c, a, bb), sr = (d, bb, a); p lies on edge (a, bb).
	c, a, bb := tr.V[0], tr.V[1], tr.V[2]
	d := sr.V[0]
	tB := b.addTriangle()
	tD := b.addTriangle()
	b.tris[t] = Triangle{V: [3]int{c, a, pi}, N: [3]int{tD, tB, tr.N[2]}}
	b.tris[tB] = Triangle{V: [3]int{c, pi, bb}, N: [3]int{s, tr.N[1], t}}
	b.tris[s] = Triangle{V: [3]int{d, bb, pi}, N: [3]int{tB, tD, sr.N[2]}}
	b.tris[tD] = Triangle{V: [3]int{d, pi, a}, N: [3]int{t, sr.N[1], s}}
	b.setBackRef(tr.N[1], t, tB)
	b.setBackRef(sr.N[1], s, tD)
	b.rotate(t, 2)  // (c, a, pi)  -> (pi, c, a)
	b.rotate(tB, 1) // (c, pi, bb) -> (pi, bb, c)
	b.rotate(s, 2)  // (d, bb, pi) -> (pi, d, bb)
	b.rotate(tD, 1) // (d, pi, a)  -> (pi, a, d)
	b.legalize(t)
	b.legalize(tB)
	b.legalize(s)
	b.legalize(tD)
}

// legalize applies Lawson's flip algorithm to the edge opposite V[0] of
// triangle t, where V[0] must be the most recently inserted point. If the
// quadrilateral formed with the neighbour across that edge fails the
// in-circle test, the shared edge is flipped and the two new edges opposite
// the inserted point are legalised recursively.
func (b *Builder) legalize(t int) {
	tr := b.tris[t]
	s := tr.N[0]
	if s < 0 {
		return
	}
	j := 0
	for ; j < 3; j++ {
		if b.tris[s].N[j] == t {
			break
		}
	}
	q := b.tris[s].V[j]
	if !inCircle(b.pts[tr.V[0]], b.pts[tr.V[1]], b.pts[tr.V[2]], b.pts[q]) {
		return
	}
	b.rotate(s, j)
	// t = (p, a, bb), s = (q, bb, a); flip edge (a, bb) to (p, q).
	sr := b.tris[s]
	p, a, bb := tr.V[0], tr.V[1], tr.V[2]
	nBP, nPA := tr.N[1], tr.N[2]
	nAQ, nQB := sr.N[1], sr.N[2]
	b.tris[t] = Triangle{V: [3]int{p, a, q}, N: [3]int{nAQ, s, nPA}}
	b.tris[s] = Triangle{V: [3]int{p, q, bb}, N: [3]int{nQB, nBP, t}}
	b.setBackRef(nAQ, s, t)
	b.setBackRef(nBP, t, s)
	b.legalize(t)
	b.legalize(s)
}

// addTriangle appends a triangle slot and returns its index.
func (b *Builder) addTriangle() int {
	b.tris = append(b.tris, Triangle{})
	return len(b.tris) - 1
}

// rotate cyclically shifts the vertex and neighbour arrays of triangle t by
// k positions, preserving orientation and edge/neighbour correspondence.
func (b *Builder) rotate(t, k int) {
	tr := &b.tris[t]
	for ; k > 0; k-- {
		tr.V = [3]int{tr.V[1], tr.V[2], tr.V[0]}
		tr.N = [3]int{tr.N[1], tr.N[2], tr.N[0]}
	}
}

// setBackRef replaces the reference to old in the neighbour list of n.
func (b *Builder) setBackRef(n, old, new int) {
	if n < 0 {
		return
	}
	for j := 0; j < 3; j++ {
		if b.tris[n].N[j] == old {
			b.tris[n].N[j] = new
			return
		}
	}
}

// Triangulation extracts the current mesh with the super-triangle removed:
// vertices are renumbered from 0 and edges that touched the super-triangle
// become convex hull boundary edges (neighbour -1).
func (b *Builder) Triangulation() *Triangulation {
	// Boundary triangles can have very large circumcircles; if one reaches
	// a super-triangle vertex the combined triangulation no longer contains
	// the full Delaunay triangulation of the input and the mesh boundary
	// dents inward. Rebuild with a frame sized beyond the largest offending
	// circumcircle until the boundary is the convex hull of the input.
	for i := 0; i < 8; i++ {
		out := b.extract()
		r, dented := out.hullDent()
		if !dented {
			return out
		}
		need := (2*r + b.span) / (0.625 * b.span)
		b.margin = math.Max(2*b.margin, 1.5*need)
		b.rebuild(b.realPoints())
	}
	return b.extract()
}

// extract compacts the current mesh into a Triangulation.
func (b *Builder) extract() *Triangulation {
	remap := make([]int, len(b.tris))
	for i := range remap {
		remap[i] = -1
	}
	out := &Triangulation{Points: append([]Point(nil), b.pts[3:]...)}
	for i, tr := range b.tris {
		if tr.V[0] < 3 || tr.V[1] < 3 || tr.V[2] < 3 {
			continue
		}
		remap[i] = len(out.Tris)
		out.Tris = append(out.Tris, tr)
	}
	for i := range out.Tris {
		for j := 0; j < 3; j++ {
			out.Tris[i].V[j] -= 3
			if n := out.Tris[i].N[j]; n < 0 || remap[n] < 0 {
				out.Tris[i].N[j] = -1
			} else {
				out.Tris[i].N[j] = remap[n]
			}
		}
	}
	return out
}

// ConvexHull returns the indices of the convex hull vertices in
// counter-clockwise order, obtained by chaining the boundary edges of the
// mesh (edges with no neighbour).
func (t *Triangulation) ConvexHull() []int {
	next := make(map[int]int)
	start := -1
	for _, tr := range t.Tris {
		for j := 0; j < 3; j++ {
			if tr.N[j] < 0 {
				from := tr.V[(j+1)%3]
				next[from] = tr.V[(j+2)%3]
				if start < 0 {
					start = from
				}
			}
		}
	}
	var hull []int
	for cur := start; cur >= 0; {
		hull = append(hull, cur)
		to, ok := next[cur]
		if !ok || to == start {
			break
		}
		cur = to
	}
	return hull
}

// IsDelaunay verifies the empty circumcircle property: no point of the mesh
// lies strictly inside the circumcircle of any triangle.
func (t *Triangulation) IsDelaunay() bool {
	for _, tr := range t.Tris {
		center, r2 := Circumcircle(t.Points[tr.V[0]], t.Points[tr.V[1]], t.Points[tr.V[2]])
		for i, p := range t.Points {
			if i == tr.V[0] || i == tr.V[1] || i == tr.V[2] {
				continue
			}
			dx, dy := p.X-center.X, p.Y-center.Y
			if dx*dx+dy*dy < r2*(1-1e-9) {
				return false
			}
		}
	}
	return true
}

// Circumcircle returns the center and squared radius of the circle through
// the three given points.
func Circumcircle(a, b, c Point) (center Point, r2 float64) {
	d := 2 * (a.X*(b.Y-c.Y) + b.X*(c.Y-a.Y) + c.X*(a.Y-b.Y))
	a2 := a.X*a.X + a.Y*a.Y
	b2 := b.X*b.X + b.Y*b.Y
	c2 := c.X*c.X + c.Y*c.Y
	center = Point{
		X: (a2*(b.Y-c.Y) + b2*(c.Y-a.Y) + c2*(a.Y-b.Y)) / d,
		Y: (a2*(c.X-b.X) + b2*(a.X-c.X) + c2*(b.X-a.X)) / d,
	}
	dx, dy := a.X-center.X, a.Y-center.Y
	return center, dx*dx + dy*dy
}

// orient returns twice the signed area of triangle (a, b, c); positive when
// the vertices are counter-clockwise.
func orient(a, b, c Point) float64 {
	return (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X)
}

// inCircle reports whether p lies strictly inside the circumcircle of the
// counter-clockwise triangle (a, b, c).
func inCircle(a, b, c, p Point) bool {
	ax, ay := a.X-p.X, a.Y-p.Y
	bx, by := b.X-p.X, b.Y-p.Y
	cx, cy := c.X-p.X, c.Y-p.Y
	return (ax*ax+ay*ay)*(bx*cy-cx*by)-
		(bx*bx+by*by)*(ax*cy-cx*ay)+
		(cx*cx+cy*cy)*(ax*by-bx*ay) > 0
}
