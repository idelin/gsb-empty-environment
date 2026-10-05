package dtri

import (
	"fmt"
	"math/big"
	"sort"
)

// Triangulate computes the canonical Delaunay triangulation of the given
// survey points using only exact integer arithmetic.
//
// Canonical-order and cocircular tie-break rule (public contract):
//
//  1. Duplicate coordinates are merged (all source IDs are kept).
//  2. Distinct points are sorted lexicographically by (X, Y). This canonical
//     order, not the input order, drives the incremental construction, so
//     any permutation of the input yields the same output.
//  3. Points are inserted one at a time in canonical order (Bowyer-Watson).
//     When the point being inserted lies exactly on the circumcircle of an
//     existing triangle (cocircular case), it is treated as strictly INSIDE
//     that circle. This is equivalent to a symbolic perturbation in which
//     later points in canonical order carry an infinitesimally smaller
//     lifting weight, i.e. the result is the regular triangulation for those
//     symbolic weights. Because the inserted point is always the newest one,
//     the rule is consistent and deterministic.
func Triangulate(input []InputPoint) (*Output, error) {
	pts, ids := canonicalPoints(input)
	n := len(pts)

	out := &Output{Points: pts, IDs: ids}
	switch {
	case n == 0:
		out.Status = StatusEmpty
		out.Hull = []int{}
		return out, nil
	case n == 1:
		out.Status = StatusPoint
		out.Hull = []int{0}
		out.Chain = []int{0}
		return out, nil
	}

	tris, err := build(pts)
	if err != nil {
		return nil, err
	}

	if len(tris) == 0 {
		// All points are collinear (covers the 2-point case as well).
		chain := collinearChain(pts)
		if n == 2 {
			out.Status = StatusSegment
		} else {
			out.Status = StatusCollinear
		}
		out.Chain = chain
		out.Hull = []int{chain[0], chain[len(chain)-1]}
		out.Triangles = [][3]int{}
		out.Neighbors = [][3]int{}
		return out, nil
	}

	out.Status = StatusTriangulated
	out.Triangles = tris
	out.Neighbors, out.Hull = adjacency(tris)
	return out, nil
}

// canonicalPoints merges duplicates and sorts distinct points by (X, Y).
func canonicalPoints(input []InputPoint) ([]Point, [][]string) {
	type entry struct {
		p   Point
		ids []string
	}
	index := map[string]int{}
	var entries []entry
	for i, ip := range input {
		if ip.X == nil || ip.Y == nil {
			continue
		}
		id := ip.ID
		if id == "" {
			id = fmt.Sprintf("%d", i)
		}
		key := ip.X.String() + "," + ip.Y.String()
		if j, ok := index[key]; ok {
			entries[j].ids = append(entries[j].ids, id)
			continue
		}
		index[key] = len(entries)
		entries = append(entries, entry{
			p:   Point{X: new(big.Int).Set(ip.X), Y: new(big.Int).Set(ip.Y)},
			ids: []string{id},
		})
	}
	sort.Slice(entries, func(i, j int) bool { return lessPoint(entries[i].p, entries[j].p) })
	pts := make([]Point, len(entries))
	ids := make([][]string, len(entries))
	for i, e := range entries {
		pts[i] = e.p
		sort.Strings(e.ids)
		ids[i] = e.ids
	}
	return pts, ids
}

// superTriangle appends (at indices n, n+1, n+2) a counter-clockwise triangle
// that strictly contains the bounding box of pts.
//
// The super-triangle must not only contain all points; its vertices must lie
// outside the circumcircle of every possible Delaunay triangle of every
// subset of pts, otherwise those vertices would corrupt the triangulation
// near the convex hull. For integer coordinates inside a bbox of side M the
// doubled area of any non-collinear triple is a positive integer (>= 1) and
// every side is <= M*sqrt(2), so the circumradius is
// R = a*b*c / (2*K2) <= (M*sqrt(2))^3 / 2 = sqrt(2)*M^3. Every point of
// every such circumcircle is therefore within M + 2*sqrt(2)*M^3 < 4*M^3 of
// the bbox center. Placing the super-triangle vertices at distance >= 6*M^3
// from the center is provably safe.
func superTriangle(pts []Point) []Point {
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
	dx := new(big.Int).Sub(maxX, minX)
	dy := new(big.Int).Sub(maxY, minY)
	m := dx
	if dy.Cmp(m) > 0 {
		m = dy
	}
	m = new(big.Int).Add(m, big.NewInt(1))
	// m = 2*M^3 (see safety argument above).
	m.Mul(m, new(big.Int).Mul(new(big.Int).Set(m), new(big.Int).Set(m)))
	m.Mul(m, big.NewInt(2))
	cx := new(big.Int).Div(new(big.Int).Add(minX, maxX), big.NewInt(2))
	cy := new(big.Int).Div(new(big.Int).Add(minY, maxY), big.NewInt(2))

	m3 := new(big.Int).Mul(m, big.NewInt(3))
	m4 := new(big.Int).Mul(m, big.NewInt(4))
	a := Point{X: new(big.Int).Sub(cx, m4), Y: new(big.Int).Sub(cy, m3)}
	b := Point{X: new(big.Int).Add(cx, m4), Y: new(big.Int).Sub(cy, m3)}
	c := Point{X: new(big.Int).Set(cx), Y: new(big.Int).Add(cy, m3)}
	return append(pts, a, b, c)
}

type triangle struct {
	v     [3]int
	alive bool
}

// build runs the Bowyer-Watson incremental construction over pts (already in
// canonical order) and returns the final CCW triangles that do not touch the
// super-triangle.
func build(pts []Point) ([][3]int, error) {
	n := len(pts)
	all := superTriangle(pts)

	tris := []triangle{{v: [3]int{n, n + 1, n + 2}, alive: true}}

	for p := 0; p < n; p++ {
		// Cavity: triangles whose circumcircle contains p. A point exactly
		// on the circle counts as inside (public tie-break rule, see
		// Triangulate).
		edgeCount := map[[2]int]int{}
		var cavity []int
		for i := range tris {
			t := &tris[i]
			if !t.alive {
				continue
			}
			s := incircle(all[t.v[0]], all[t.v[1]], all[t.v[2]], all[p])
			if s < 0 {
				continue
			}
			t.alive = false
			cavity = append(cavity, i)
			for k := 0; k < 3; k++ {
				e := [2]int{t.v[k], t.v[(k+1)%3]}
				edgeCount[e]++
			}
		}
		if len(cavity) == 0 {
			return nil, fmt.Errorf("dtri: point %d (%s,%s) not located in any circumcircle",
				p, all[p].X, all[p].Y)
		}
		// Boundary edges of the cavity appear exactly once in CCW direction.
		seen := map[[2]int]bool{}
		for _, ci := range cavity {
			t := tris[ci]
			for k := 0; k < 3; k++ {
				u, v := t.v[k], t.v[(k+1)%3]
				if seen[[2]int{u, v}] {
					continue
				}
				seen[[2]int{u, v}] = true
				if edgeCount[[2]int{v, u}] > 0 {
					continue // interior edge, shared with another cavity triangle
				}
				switch orient(all[u], all[v], all[p]) {
				case 1:
					tris = append(tris, triangle{v: [3]int{u, v, p}, alive: true})
				case 0:
					// Degenerate (zero-area) triangle: p is collinear with
					// the boundary edge. Skipping it keeps the edge intact;
					// p is still connected through other boundary edges.
				default:
					return nil, fmt.Errorf("dtri: non-star-shaped cavity at point %d", p)
				}
			}
		}
	}

	var out [][3]int
	for _, t := range tris {
		if !t.alive {
			continue
		}
		if t.v[0] >= n || t.v[1] >= n || t.v[2] >= n {
			continue // touches the super-triangle
		}
		out = append(out, t.v)
	}
	return out, nil
}

// adjacency builds the neighbor table and the CCW hull loop for a set of CCW
// triangles. Neighbors[i][k] is the triangle across the edge opposite vertex
// k of triangle i, or -1 on the hull.
func adjacency(tris [][3]int) (neighbors [][3]int, hull []int) {
	type uedge struct{ a, b int }
	edgeTris := map[uedge][]int{}
	norm := func(u, v int) uedge {
		if u < v {
			return uedge{u, v}
		}
		return uedge{v, u}
	}
	for i, t := range tris {
		for k := 0; k < 3; k++ {
			e := norm(t[(k+1)%3], t[(k+2)%3])
			edgeTris[e] = append(edgeTris[e], i)
		}
	}

	neighbors = make([][3]int, len(tris))
	next := map[int]int{}
	for i, t := range tris {
		for k := 0; k < 3; k++ {
			u, v := t[(k+1)%3], t[(k+2)%3]
			sharing := edgeTris[norm(u, v)]
			nb := -1
			for _, j := range sharing {
				if j != i {
					nb = j
				}
			}
			neighbors[i][k] = nb
			if nb == -1 {
				// Boundary edge in CCW direction (interior on the left).
				next[u] = v
			}
		}
	}

	// Chain boundary edges into the CCW hull loop, starting at the smallest
	// vertex index so the loop is canonical.
	start := -1
	for u := range next {
		if start == -1 || u < start {
			start = u
		}
	}
	if start >= 0 {
		cur := start
		for {
			hull = append(hull, cur)
			cur = next[cur]
			if cur == start {
				break
			}
		}
	}
	return neighbors, hull
}

// collinearChain returns all point indices ordered along the common line.
func collinearChain(pts []Point) []int {
	idx := make([]int, len(pts))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(i, j int) bool { return lessPoint(pts[idx[i]], pts[idx[j]]) })
	return idx
}
