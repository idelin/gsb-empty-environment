// Package delaunay builds a Delaunay triangulation of a 2D point set.
//
// The triangulation is constructed incrementally: points are inserted one at
// a time into an enclosing super-triangle, the containing triangle is split,
// and the Delaunay property (empty circumcircle) is restored locally with
// Lawson's edge-flip algorithm. The result is emitted as a compact
// topological mesh with per-triangle adjacency and the outer convex hull.
package delaunay

// Point is a 2D vertex.
type Point struct {
	X, Y float64
}

// Triangle is a compact topological record.
//
//	V: indices of the three vertices, counter-clockwise.
//	N: N[i] is the index of the neighbour triangle across the edge opposite
//	   V[i], or -1 when that edge lies on the convex hull boundary.
type Triangle struct {
	V [3]int
	N [3]int
}

// Mesh is the resulting triangle topology.
type Mesh struct {
	Points    []Point
	Triangles []Triangle
	Hull      []int // convex hull vertex indices, counter-clockwise
}

// tri is the internal mutable triangle record.
type tri struct {
	v     [3]int // vertex indices, CCW
	n     [3]int // neighbour across the edge opposite v[i]; -1 = none
	alive bool
}

// Triangulator incrementally builds a Delaunay triangulation.
// The first three entries of pts are the super-triangle vertices.
type Triangulator struct {
	pts  []Point
	tris []tri
	last int // walk start hint for point location
}

// NewTriangulator creates a triangulator whose super-triangle encloses the
// bounding box [min, max]. All subsequently inserted points must lie inside
// that box.
func NewTriangulator(min, max Point) *Triangulator {
	return newTriangulator(min, max, 100)
}

// newTriangulator is NewTriangulator with an explicit super-triangle scale.
func newTriangulator(min, max Point, scale float64) *Triangulator {
	cx := (min.X + max.X) / 2
	cy := (min.Y + max.Y) / 2
	m := max.X - min.X
	if d := max.Y - min.Y; d > m {
		m = d
	}
	if m <= 0 {
		m = 1
	}
	m *= scale
	tr := &Triangulator{
		pts: []Point{
			{X: cx - 2*m, Y: cy - m},
			{X: cx + 2*m, Y: cy - m},
			{X: cx, Y: cy + 2*m},
		},
		last: 0,
	}
	tr.tris = []tri{{v: [3]int{0, 1, 2}, n: [3]int{-1, -1, -1}, alive: true}}
	return tr
}

// Triangulate builds the Delaunay triangulation of points and returns the
// compact topological mesh.
func Triangulate(points []Point) *Mesh {
	if len(points) < 3 {
		return &Mesh{Points: append([]Point(nil), points...)}
	}
	min, max := points[0], points[0]
	for _, p := range points[1:] {
		if p.X < min.X {
			min.X = p.X
		}
		if p.Y < min.Y {
			min.Y = p.Y
		}
		if p.X > max.X {
			max.X = p.X
		}
		if p.Y > max.Y {
			max.Y = p.Y
		}
	}
	// A super-triangle vertex can fall inside the circumcircle of a
	// near-degenerate real triangle; Lawson flips then rewire convex-hull
	// edges to super vertices and removing them would leave the convex hull
	// partially uncovered. Enlarge the super-triangle until the extracted
	// mesh provably covers the convex hull of the point set.
	for scale := 100.0; ; scale *= 100 {
		tr := newTriangulator(min, max, scale)
		for _, p := range points {
			tr.Insert(p)
		}
		if m := tr.Mesh(); validMesh(m) {
			return m
		}
	}
}

// validMesh reports whether m triangulates the full convex hull of its
// points: the boundary loop must be convex, contain every point, and every
// point must appear as a triangle vertex.
func validMesh(m *Mesh) bool {
	if len(m.Points) < 3 {
		return true
	}
	h := m.Hull
	if len(h) < 3 {
		return false
	}
	tol := 1e-9 * (1 + bboxSize(m.Points))
	n := len(h)
	for i := range h {
		a, b, c := m.Points[h[i]], m.Points[h[(i+1)%n]], m.Points[h[(i+2)%n]]
		if orient2d(a, b, c) < -tol {
			return false
		}
		for _, p := range m.Points {
			if orient2d(a, b, p) < -tol {
				return false
			}
		}
	}
	used := make([]bool, len(m.Points))
	for _, t := range m.Triangles {
		used[t.V[0]] = true
		used[t.V[1]] = true
		used[t.V[2]] = true
	}
	for _, u := range used {
		if !u {
			return false
		}
	}
	return true
}

func bboxSize(pts []Point) float64 {
	min, max := pts[0], pts[0]
	for _, p := range pts[1:] {
		if p.X < min.X {
			min.X = p.X
		}
		if p.Y < min.Y {
			min.Y = p.Y
		}
		if p.X > max.X {
			max.X = p.X
		}
		if p.Y > max.Y {
			max.Y = p.Y
		}
	}
	d := max.X - min.X
	if dy := max.Y - min.Y; dy > d {
		d = dy
	}
	return d
}

// Insert adds p to the triangulation and restores the Delaunay property
// locally with Lawson flips. It returns the index of the point; inserting an
// exact duplicate is a no-op that returns the existing index.
func (tr *Triangulator) Insert(p Point) int {
	for i, q := range tr.pts[3:] {
		if q == p {
			return i + 3
		}
	}
	idx := len(tr.pts)
	tr.pts = append(tr.pts, p)

	t := tr.locate(p)
	// If p lies on an edge, split the one or two triangles sharing it.
	for i := 0; i < 3; i++ {
		a := tr.tris[t].v[(i+1)%3]
		b := tr.tris[t].v[(i+2)%3]
		if onEdge(tr.pts[a], tr.pts[b], p) {
			if u := tr.tris[t].n[i]; u >= 0 {
				tr.split4(t, i, u, idx)
			} else {
				tr.split2(t, i, idx)
			}
			tr.last = t
			return idx
		}
	}
	tr.split3(t, idx)
	tr.last = t
	return idx
}

// locate returns the index of the alive triangle containing p, using a walk
// from the last insertion triangle with a linear-scan fallback.
func (tr *Triangulator) locate(p Point) int {
	t := tr.last
	if t < 0 || t >= len(tr.tris) || !tr.tris[t].alive {
		t = tr.anyAlive()
	}
	for steps := 0; steps <= 3*len(tr.tris); steps++ {
		cur := tr.tris[t]
		advanced := false
		for i := 0; i < 3; i++ {
			a := tr.pts[cur.v[(i+1)%3]]
			b := tr.pts[cur.v[(i+2)%3]]
			if orient2d(a, b, p) < 0 {
				if cur.n[i] < 0 {
					return t // outside everything; should not happen
				}
				t = cur.n[i]
				advanced = true
				break
			}
		}
		if !advanced {
			return t
		}
	}
	// Fallback: brute-force scan (guards against walk cycles on degenerate input).
	for i, c := range tr.tris {
		if c.alive && tr.contains(i, p) {
			return i
		}
	}
	return tr.anyAlive()
}

func (tr *Triangulator) contains(t int, p Point) bool {
	c := tr.tris[t]
	for i := 0; i < 3; i++ {
		a := tr.pts[c.v[(i+1)%3]]
		b := tr.pts[c.v[(i+2)%3]]
		if orient2d(a, b, p) < 0 {
			return false
		}
	}
	return true
}

func (tr *Triangulator) anyAlive() int {
	for i, c := range tr.tris {
		if c.alive {
			return i
		}
	}
	return 0
}

func (tr *Triangulator) addTri(v, n [3]int) int {
	tr.tris = append(tr.tris, tri{v: v, n: n, alive: true})
	return len(tr.tris) - 1
}

// findEdge returns the edge index j of triangle u whose neighbour is t.
func (tr *Triangulator) findEdge(u, t int) int {
	for j := 0; j < 3; j++ {
		if tr.tris[u].n[j] == t {
			return j
		}
	}
	return -1
}

// replaceNeighbor rewires triangle x's neighbour reference from old to nw.
func (tr *Triangulator) replaceNeighbor(x, old, nw int) {
	if x < 0 {
		return
	}
	for k := 0; k < 3; k++ {
		if tr.tris[x].n[k] == old {
			tr.tris[x].n[k] = nw
			return
		}
	}
}

// split3 replaces triangle t (containing interior point p) with three
// triangles, then legalizes the three edges opposite p.
func (tr *Triangulator) split3(t, p int) {
	old := tr.tris[t]
	A, B, C := old.v[0], old.v[1], old.v[2]
	na, nb, nc := old.n[0], old.n[1], old.n[2]

	t1 := tr.addTri([3]int{p, C, A}, [3]int{nb, -1, -1})
	t2 := tr.addTri([3]int{p, A, B}, [3]int{nc, -1, -1})
	tr.tris[t] = tri{v: [3]int{p, B, C}, n: [3]int{na, t1, t2}, alive: true}
	tr.tris[t1].n = [3]int{nb, t2, t}
	tr.tris[t2].n = [3]int{nc, t, t1}

	tr.replaceNeighbor(nb, t, t1)
	tr.replaceNeighbor(nc, t, t2)

	tr.legalize(t, 0)
	tr.legalize(t1, 0)
	tr.legalize(t2, 0)
}

// split2 handles a point p landing on a boundary edge (edge ei of t with no
// neighbour): t is split into two triangles.
func (tr *Triangulator) split2(t, ei, p int) {
	shift := (ei + 1) % 3
	tv := rotate3(tr.tris[t].v, shift)
	tn := rotate3(tr.tris[t].n, shift)
	A, B, C := tv[0], tv[1], tv[2]
	na, nb := tn[0], tn[1]

	t1 := tr.addTri([3]int{p, C, A}, [3]int{nb, -1, t})
	tr.tris[t] = tri{v: [3]int{p, B, C}, n: [3]int{na, t1, -1}, alive: true}

	tr.replaceNeighbor(nb, t, t1)

	tr.legalize(t, 0)
	tr.legalize(t1, 0)
}

// split4 handles a point p landing on an interior edge shared by triangles t
// and u: both are split, producing four triangles.
func (tr *Triangulator) split4(t, ei, u, p int) {
	// Rotate t so the edge holding p is opposite vertex C (index 2).
	tv := rotate3(tr.tris[t].v, (ei+1)%3)
	tn := rotate3(tr.tris[t].n, (ei+1)%3)
	A, B, C := tv[0], tv[1], tv[2]
	na, nb := tn[0], tn[1]

	// Rotate u so the shared edge is opposite its vertex Q (index 0),
	// giving u = (Q, B, A).
	j := tr.findEdge(u, t)
	uv := rotate3(tr.tris[u].v, j)
	un := rotate3(tr.tris[u].n, j)
	Q := uv[0]
	m1, m2 := un[1], un[2] // across (A,Q) and (Q,B)

	t1 := tr.addTri([3]int{p, C, A}, [3]int{nb, u, -1})
	u1 := tr.addTri([3]int{p, Q, B}, [3]int{m2, t, u})
	tr.tris[t] = tri{v: [3]int{p, B, C}, n: [3]int{na, t1, u1}, alive: true}
	tr.tris[t1].n[2] = t
	tr.tris[u] = tri{v: [3]int{p, A, Q}, n: [3]int{m1, u1, t1}, alive: true}

	tr.replaceNeighbor(nb, t, t1)
	tr.replaceNeighbor(m2, u, u1)

	tr.legalize(t, 0)
	tr.legalize(t1, 0)
	tr.legalize(u, 0)
	tr.legalize(u1, 0)
}

// legalize applies Lawson's flip algorithm: while the edge of triangle t
// opposite its vertex at index i is illegal (the opposite vertex lies inside
// the circumcircle), flip it and recurse on the two new opposite edges.
func (tr *Triangulator) legalize(t, i int) {
	type edge struct{ t, i int }
	stack := []edge{{t, i}}
	for len(stack) > 0 {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !tr.tris[e.t].alive {
			continue
		}
		u := tr.tris[e.t].n[e.i]
		if u < 0 || !tr.tris[u].alive {
			continue
		}
		j := tr.findEdge(u, e.t)
		if j < 0 {
			continue
		}
		P := tr.tris[e.t].v[e.i]
		a := tr.tris[e.t].v[(e.i+1)%3]
		b := tr.tris[e.t].v[(e.i+2)%3]
		Q := tr.tris[u].v[j]
		if inCircle(tr.pts[P], tr.pts[a], tr.pts[b], tr.pts[Q]) <= 0 {
			continue // edge already legal
		}
		// Rotate both triangles so the shared edge is opposite index 0:
		// t = (P, a, b) with n[0]=u, u = (Q, b, a) with n[0]=t.
		if e.i != 0 {
			tr.tris[e.t].v = rotate3(tr.tris[e.t].v, e.i)
			tr.tris[e.t].n = rotate3(tr.tris[e.t].n, e.i)
		}
		if j != 0 {
			tr.tris[u].v = rotate3(tr.tris[u].v, j)
			tr.tris[u].n = rotate3(tr.tris[u].n, j)
		}
		na := tr.tris[e.t].n[1] // across (b, P)
		nb := tr.tris[e.t].n[2] // across (P, a)
		mb := tr.tris[u].n[1]   // across (a, Q)
		ma := tr.tris[u].n[2]   // across (Q, b)

		// Flip edge a-b to P-Q. The quad boundary traversed CCW is
		// P -> a -> Q -> b, so the two new CCW triangles are
		// t' = (P, Q, b) and u' = (P, a, Q). Both keep the inserted
		// point P at index 0 so the edges opposite P (indices 0) are
		// exactly the ones Lawson's algorithm must re-legalize.
		tr.tris[e.t] = tri{v: [3]int{P, Q, b}, n: [3]int{ma, na, u}, alive: true}
		tr.tris[u] = tri{v: [3]int{P, a, Q}, n: [3]int{mb, e.t, nb}, alive: true}

		tr.replaceNeighbor(nb, e.t, u)
		tr.replaceNeighbor(ma, u, e.t)

		stack = append(stack, edge{e.t, 0}, edge{u, 0})
	}
}

// Mesh extracts the final compact topology: triangles touching the
// super-triangle vertices are discarded, neighbour references are remapped,
// and the convex hull boundary is derived from the dangling edges.
func (tr *Triangulator) Mesh() *Mesh {
	m := &Mesh{Points: append([]Point(nil), tr.pts[3:]...)}
	id := make([]int, len(tr.tris))
	for i := range id {
		id[i] = -1
	}
	for i, t := range tr.tris {
		if !t.alive || t.v[0] < 3 || t.v[1] < 3 || t.v[2] < 3 {
			continue
		}
		id[i] = len(m.Triangles)
		m.Triangles = append(m.Triangles, Triangle{
			V: [3]int{t.v[0] - 3, t.v[1] - 3, t.v[2] - 3},
			N: [3]int{-1, -1, -1},
		})
	}
	for i, t := range tr.tris {
		if id[i] < 0 {
			continue
		}
		for k := 0; k < 3; k++ {
			if t.n[k] >= 0 && id[t.n[k]] >= 0 {
				m.Triangles[id[i]].N[k] = id[t.n[k]]
			}
		}
	}
	m.Hull = boundaryLoop(m)
	return m
}

// boundaryLoop chains the directed boundary edges (interior on the left)
// into the counter-clockwise convex hull loop.
func boundaryLoop(m *Mesh) []int {
	next := map[int]int{}
	for _, t := range m.Triangles {
		for i := 0; i < 3; i++ {
			if t.N[i] == -1 {
				next[t.V[(i+1)%3]] = t.V[(i+2)%3]
			}
		}
	}
	if len(next) == 0 {
		return nil
	}
	var start int
	for k := range next {
		start = k
		break
	}
	hull := []int{start}
	for cur := next[start]; cur != start; cur = next[cur] {
		hull = append(hull, cur)
	}
	return hull
}
