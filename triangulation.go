package delaunay

import (
	"math/big"
	"sort"
)

// Triangulation accumulates points (with per-point source IDs) and produces
// a canonical Delaunay triangulation on demand via Result.
//
// Points may be inserted one at a time in any order; Result always returns
// the same canonical output for the same set of distinct coordinates,
// regardless of insertion order. See the package documentation for the
// public tie-break rule.
type Triangulation struct {
	pts   []Point
	ids   [][]string
	index map[string]int
}

// New returns an empty Triangulation.
func New() *Triangulation {
	return &Triangulation{index: make(map[string]int)}
}

func coordKey(x, y *big.Int) string { return x.String() + "," + y.String() }

// Insert adds one point. If the coordinates were already present, the id is
// appended to the existing point's source ID list and merged is true.
// Returns the canonical index of the point.
func (t *Triangulation) Insert(x, y *big.Int, id string) (idx int, merged bool) {
	k := coordKey(x, y)
	if i, ok := t.index[k]; ok {
		t.ids[i] = append(t.ids[i], id)
		return i, true
	}
	i := len(t.pts)
	t.pts = append(t.pts, Point{new(big.Int).Set(x), new(big.Int).Set(y)})
	t.ids = append(t.ids, []string{id})
	t.index[k] = i
	return i, false
}

// InsertXY is a convenience wrapper around Insert for int64 coordinates.
func (t *Triangulation) InsertXY(x, y int64, id string) (idx int, merged bool) {
	return t.Insert(big.NewInt(x), big.NewInt(y), id)
}

// Len returns the number of distinct points.
func (t *Triangulation) Len() int { return len(t.pts) }

// Points returns the distinct points in canonical insertion order.
func (t *Triangulation) Points() []Point { return t.pts }

// IDs returns the source ID lists per distinct point.
func (t *Triangulation) IDs() [][]string { return t.ids }

// lessIdx orders point indices lexicographically by (x, y).
func lessIdx(pts []Point, i, j int) bool {
	c := pts[i].X.Cmp(pts[j].X)
	if c != 0 {
		return c < 0
	}
	return pts[i].Y.Cmp(pts[j].Y) < 0
}

func allCollinear(pts []Point) bool {
	if len(pts) < 3 {
		return true
	}
	for i := 2; i < len(pts); i++ {
		if orient(pts[0], pts[1], pts[i]) != 0 {
			return false
		}
	}
	return true
}

// sortedIndices returns indices sorted by (x, y).
func sortedIndices(pts []Point) []int {
	idx := make([]int, len(pts))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return lessIdx(pts, idx[a], idx[b]) })
	return idx
}

// superTriangle returns a CCW triangle strictly containing all pts.
func superTriangle(pts []Point) (Point, Point, Point) {
	minX := new(big.Int).Set(pts[0].X)
	maxX := new(big.Int).Set(pts[0].X)
	minY := new(big.Int).Set(pts[0].Y)
	maxY := new(big.Int).Set(pts[0].Y)
	for _, p := range pts[1:] {
		if p.X.Cmp(minX) < 0 {
			minX.Set(p.X)
		}
		if p.X.Cmp(maxX) > 0 {
			maxX.Set(p.X)
		}
		if p.Y.Cmp(minY) < 0 {
			minY.Set(p.Y)
		}
		if p.Y.Cmp(maxY) > 0 {
			maxY.Set(p.Y)
		}
	}
	dx := isub(maxX, minX)
	dy := isub(maxY, minY)
	d := dx
	if dy.Cmp(d) > 0 {
		d = dy
	}
	d.Add(d, big.NewInt(1))
	cx := new(big.Int).Add(minX, maxX)
	cx.Div(cx, big.NewInt(2))

	// The super-triangle must be far enough that no super vertex lies
	// inside the circumcircle of any real point triple. For integer
	// points, a non-collinear triple has area >= 1/2, so its circumradius
	// is R = abc/(4A) <= D^3/2 where D is the point set diameter, and the
	// whole circle stays within distance 2R <= D^3 of the points. Since
	// D <= sqrt(2)*d, placing the super vertices at distance >= 32*d^3
	// from the bounding box is safely outside every real circumcircle.
	d3 := new(big.Int).Mul(d, d)
	d3.Mul(d3, d)
	k := new(big.Int).Mul(d3, big.NewInt(32))
	k4 := new(big.Int).Mul(k, big.NewInt(4))
	a := Point{new(big.Int).Sub(cx, k4), new(big.Int).Sub(minY, k)}
	b := Point{new(big.Int).Add(cx, k4), new(big.Int).Sub(minY, k)}
	c := Point{new(big.Int).Set(cx), new(big.Int).Add(maxY, k4)}
	return a, b, c
}

type tri struct{ v [3]int }

type uedge struct{ a, b int }

func makeEdge(a, b int) uedge {
	if a > b {
		a, b = b, a
	}
	return uedge{a, b}
}

// bowyerWatson triangulates pts (assumed not all collinear, len >= 3).
// Points are inserted in lexicographic (x, y) order; the in-circle test is
// strict, so cocircular degeneracies resolve deterministically.
func bowyerWatson(pts []Point) [][3]int {
	s0, s1, s2 := superTriangle(pts)
	all := make([]Point, 0, len(pts)+3)
	all = append(all, s0, s1, s2)
	all = append(all, pts...)

	tris := []tri{{[3]int{0, 1, 2}}}
	order := sortedIndices(pts)

	for _, pi := range order {
		p := all[pi+3]
		bad := make(map[int]bool)
		for i, tr := range tris {
			if inCircle(all[tr.v[0]], all[tr.v[1]], all[tr.v[2]], p) > 0 {
				bad[i] = true
			}
		}
		// Boundary edges of the cavity: edges used by exactly one bad
		// triangle, directed as stored (CCW around the bad triangle).
		count := make(map[uedge]int)
		dir := make(map[uedge]uedge)
		for bi := range bad {
			tr := tris[bi]
			for e := 0; e < 3; e++ {
				a, b := tr.v[e], tr.v[(e+1)%3]
				k := makeEdge(a, b)
				count[k]++
				dir[k] = uedge{a, b}
			}
		}
		var boundary []uedge
		for k, c := range count {
			if c == 1 {
				boundary = append(boundary, dir[k])
			}
		}
		sort.Slice(boundary, func(i, j int) bool {
			if boundary[i].a != boundary[j].a {
				return boundary[i].a < boundary[j].a
			}
			return boundary[i].b < boundary[j].b
		})
		next := make([]tri, 0, len(tris)-len(bad)+len(boundary))
		for i, tr := range tris {
			if !bad[i] {
				next = append(next, tr)
			}
		}
		for _, e := range boundary {
			o := orient(all[e.a], all[e.b], p)
			switch {
			case o > 0:
				next = append(next, tri{[3]int{e.a, e.b, pi + 3}})
			case o == 0:
				// p is collinear with the boundary edge and lies beyond
				// the segment: the edge stays on the hull; skip the
				// degenerate (zero-area) triangle.
			default:
				panic("delaunay: cavity not star-shaped (internal bug)")
			}
		}
		tris = next
	}

	var out [][3]int
	for _, tr := range tris {
		if tr.v[0] >= 3 && tr.v[1] >= 3 && tr.v[2] >= 3 {
			out = append(out, [3]int{tr.v[0] - 3, tr.v[1] - 3, tr.v[2] - 3})
		}
	}
	return out
}

// normalizeTri rotates a CCW triangle so its smallest vertex index is first.
func normalizeTri(t [3]int) [3]int {
	for t[1] < t[0] || t[2] < t[0] {
		t = [3]int{t[1], t[2], t[0]}
	}
	return t
}

func lessTri(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// adjacencyOf computes, per triangle, the neighbor across the edge opposite
// vertex i (slot i), or -1 for a boundary edge.
func adjacencyOf(tris [][3]int) [][3]int {
	type loc struct{ t, slot int }
	edgeTri := make(map[uedge]loc)
	adj := make([][3]int, len(tris))
	for i := range adj {
		adj[i] = [3]int{-1, -1, -1}
	}
	for ti, tr := range tris {
		for slot := 0; slot < 3; slot++ {
			a := tr[(slot+1)%3]
			b := tr[(slot+2)%3]
			k := makeEdge(a, b)
			if other, ok := edgeTri[k]; ok {
				adj[ti][slot] = other.t
				adj[other.t][other.slot] = ti
			} else {
				edgeTri[k] = loc{ti, slot}
			}
		}
	}
	return adj
}

// hullOf extracts the CCW convex hull loop (including collinear boundary
// points) from the boundary edges of the triangulation.
func hullOf(tris [][3]int) []int {
	count := make(map[uedge]int)
	dir := make(map[uedge]uedge)
	for _, tr := range tris {
		for e := 0; e < 3; e++ {
			a, b := tr[e], tr[(e+1)%3]
			k := makeEdge(a, b)
			count[k]++
			dir[k] = uedge{a, b}
		}
	}
	next := make(map[int]int)
	start := -1
	for k, c := range count {
		if c == 1 {
			d := dir[k]
			next[d.a] = d.b
			if start == -1 || d.a < start {
				start = d.a
			}
		}
	}
	if start == -1 {
		return nil
	}
	hull := []int{start}
	for cur := next[start]; cur != start; cur = next[cur] {
		hull = append(hull, cur)
	}
	return hull
}

// Result computes the canonical output for the inserted points. Output
// point indices are assigned in lexicographic (x, y) order and merged ID
// lists are sorted, so the whole document is independent of input order.
func (t *Triangulation) Result() *Output {
	n := len(t.pts)
	order := sortedIndices(t.pts)
	remap := make([]int, n)
	for newIdx, oldIdx := range order {
		remap[oldIdx] = newIdx
	}
	out := &Output{Points: make([]OutputPoint, n)}
	for newIdx, oldIdx := range order {
		p := t.pts[oldIdx]
		ids := make([]string, len(t.ids[oldIdx]))
		copy(ids, t.ids[oldIdx])
		sort.Strings(ids)
		out.Points[newIdx] = OutputPoint{
			Index: newIdx,
			X:     jsonNumber(p.X),
			Y:     jsonNumber(p.Y),
			IDs:   ids,
		}
	}

	switch {
	case n == 0:
		out.Mode = ModeEmpty
	case n == 1:
		out.Mode = ModePoint
		out.Chain = []int{0}
	case n == 2:
		out.Mode = ModeSegment
		out.Chain = []int{0, 1}
	case allCollinear(t.pts):
		out.Mode = ModeCollinear
		out.Chain = make([]int, n)
		for i := range out.Chain {
			out.Chain[i] = i
		}
	default:
		out.Mode = ModeTriangulation
		tris := bowyerWatson(t.pts)
		for i, tr := range tris {
			tris[i] = normalizeTri([3]int{remap[tr[0]], remap[tr[1]], remap[tr[2]]})
		}
		sort.Slice(tris, func(i, j int) bool { return lessTri(tris[i], tris[j]) })
		out.Triangles = tris
		out.Adjacency = adjacencyOf(tris)
		out.Hull = hullOf(tris)
	}
	return out
}
