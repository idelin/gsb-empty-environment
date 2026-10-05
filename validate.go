package dtri

import (
	"fmt"
	"sort"
)

// Validate independently checks a triangulation Output. It does not trust any
// property claimed by the construction; every check is recomputed from the
// output data with exact integer predicates. It returns nil on success or the
// first violation found.
//
// Checked properties:
//   - canonical point order, merged IDs non-empty and sorted
//   - every triangle is CCW with strictly positive area
//   - every point is used by at least one triangle (triangulated case)
//   - no two triangles overlap (no vertex strictly inside another triangle,
//     no proper edge crossings)
//   - edge adjacency is symmetric and consistent with the neighbor table
//   - hull equals the boundary edge loop, is convex, and covers all points
//   - Euler relations: V-E+T = 1 and 3T = 2E-B
//   - Delaunay: no fourth point lies strictly inside any triangle's
//     circumcircle (cocircular, i.e. exactly on, is allowed)
//   - degenerate cases: chain contains every point exactly once, is ordered
//     along the line, and all points are collinear
func Validate(out *Output) error {
	n := len(out.Points)
	if len(out.IDs) != n {
		return fmt.Errorf("validate: IDs length %d != points length %d", len(out.IDs), n)
	}
	for i := 0; i < n; i++ {
		if len(out.IDs[i]) == 0 {
			return fmt.Errorf("validate: point %d has no source IDs", i)
		}
		if !sort.StringsAreSorted(out.IDs[i]) {
			return fmt.Errorf("validate: IDs of point %d not sorted", i)
		}
		if i > 0 && !lessPoint(out.Points[i-1], out.Points[i]) {
			return fmt.Errorf("validate: points not in canonical (x,y) order at %d", i)
		}
	}

	switch out.Status {
	case StatusEmpty:
		if n != 0 {
			return fmt.Errorf("validate: status empty but %d points present", n)
		}
		return nil
	case StatusPoint, StatusSegment, StatusCollinear:
		return validateDegenerate(out)
	case StatusTriangulated:
		return validateTriangulated(out)
	}
	return fmt.Errorf("validate: unknown status %q", out.Status)
}

func validateDegenerate(out *Output) error {
	n := len(out.Points)
	want := map[Status]int{StatusPoint: 1, StatusSegment: 2}
	if w, ok := want[out.Status]; ok && n != w {
		return fmt.Errorf("validate: status %s but %d points", out.Status, n)
	}
	if out.Status == StatusCollinear && n < 3 {
		return fmt.Errorf("validate: status collinear with %d points", n)
	}
	if len(out.Triangles) != 0 {
		return fmt.Errorf("validate: degenerate status but %d triangles present", len(out.Triangles))
	}
	// Chain must be a permutation of all point indices.
	if len(out.Chain) != n {
		return fmt.Errorf("validate: chain length %d != %d points", len(out.Chain), n)
	}
	seen := make([]bool, n)
	for _, idx := range out.Chain {
		if idx < 0 || idx >= n {
			return fmt.Errorf("validate: chain index %d out of range", idx)
		}
		if seen[idx] {
			return fmt.Errorf("validate: chain index %d repeated", idx)
		}
		seen[idx] = true
	}
	// All points collinear and chain ordered monotonically along the line.
	if n >= 2 {
		a := out.Points[out.Chain[0]]
		b := out.Points[out.Chain[n-1]]
		for i := 1; i < n-1; i++ {
			if orient(a, b, out.Points[out.Chain[i]]) != 0 {
				return fmt.Errorf("validate: chain point %d not collinear with endpoints", i)
			}
		}
		for i := 0; i+1 < n; i++ {
			p := out.Points[out.Chain[i]]
			q := out.Points[out.Chain[i+1]]
			// Distinct collinear points in lexicographic (x,y) order are
			// monotone along any straight line.
			if !lessPoint(p, q) {
				return fmt.Errorf("validate: chain not monotone at position %d", i+1)
			}
		}
	}
	// Hull must be the chain endpoints.
	if len(out.Hull) != 2 || out.Hull[0] != out.Chain[0] || out.Hull[1] != out.Chain[n-1] {
		if !(out.Status == StatusPoint && len(out.Hull) == 1 && out.Hull[0] == out.Chain[0]) {
			return fmt.Errorf("validate: hull inconsistent with chain endpoints")
		}
	}
	return nil
}

func validateTriangulated(out *Output) error {
	n := len(out.Points)
	pts := out.Points
	tris := out.Triangles
	if len(out.Neighbors) != len(tris) {
		return fmt.Errorf("validate: neighbors length %d != triangles length %d", len(out.Neighbors), len(tris))
	}

	// Positive area, valid indices.
	used := make([]bool, n)
	for i, t := range tris {
		for k := 0; k < 3; k++ {
			if t[k] < 0 || t[k] >= n {
				return fmt.Errorf("validate: triangle %d vertex index %d out of range", i, t[k])
			}
			used[t[k]] = true
		}
		if t[0] == t[1] || t[1] == t[2] || t[0] == t[2] {
			return fmt.Errorf("validate: triangle %d has repeated vertex", i)
		}
		if orient(pts[t[0]], pts[t[1]], pts[t[2]]) <= 0 {
			return fmt.Errorf("validate: triangle %d is not strictly CCW", i)
		}
	}
	for i := range used {
		if !used[i] {
			return fmt.Errorf("validate: point %d not used by any triangle", i)
		}
	}

	// No two triangles overlap.
	for i := 0; i < len(tris); i++ {
		for j := i + 1; j < len(tris); j++ {
			if trianglesOverlap(pts, tris[i], tris[j]) {
				return fmt.Errorf("validate: triangles %d and %d overlap", i, j)
			}
		}
	}

	// Edge structure and adjacency symmetry.
	norm := func(u, v int) [2]int {
		if u < v {
			return [2]int{u, v}
		}
		return [2]int{v, u}
	}
	edgeTris := map[[2]int][]int{}
	for i, t := range tris {
		for k := 0; k < 3; k++ {
			e := norm(t[(k+1)%3], t[(k+2)%3])
			edgeTris[e] = append(edgeTris[e], i)
		}
	}
	boundary := 0
	for e, list := range edgeTris {
		if len(list) > 2 {
			return fmt.Errorf("validate: edge %v shared by %d triangles", e, len(list))
		}
		if len(list) == 1 {
			boundary++
		}
	}
	for i, t := range tris {
		for k := 0; k < 3; k++ {
			u, v := t[(k+1)%3], t[(k+2)%3]
			e := norm(u, v)
			nb := out.Neighbors[i][k]
			if len(edgeTris[e]) == 1 {
				if nb != -1 {
					return fmt.Errorf("validate: boundary edge of triangle %d slot %d has neighbor %d", i, k, nb)
				}
				continue
			}
			if nb < 0 || nb >= len(tris) || nb == i {
				return fmt.Errorf("validate: triangle %d slot %d invalid neighbor %d", i, k, nb)
			}
			// Neighbor must share the edge and point back.
			foundEdge, foundBack := false, false
			tn := tris[nb]
			for k2 := 0; k2 < 3; k2++ {
				if norm(tn[(k2+1)%3], tn[(k2+2)%3]) == e {
					foundEdge = true
					if out.Neighbors[nb][k2] == i {
						foundBack = true
					}
				}
			}
			if !foundEdge {
				return fmt.Errorf("validate: neighbor %d of triangle %d does not share the edge", nb, i)
			}
			if !foundBack {
				return fmt.Errorf("validate: adjacency not symmetric between %d and %d", i, nb)
			}
		}
	}

	// Hull: convex, CCW, covers all points, and matches boundary edges.
	if err := validateHull(out, edgeTris); err != nil {
		return err
	}

	// Euler relations for a triangulation of a point set (disk topology):
	// V - E + T = 1 and 3T = 2E - B.
	vCnt, eCnt, tCnt := n, len(edgeTris), len(tris)
	if vCnt-eCnt+tCnt != 1 {
		return fmt.Errorf("validate: Euler relation V-E+T=%d, want 1 (V=%d E=%d T=%d)",
			vCnt-eCnt+tCnt, vCnt, eCnt, tCnt)
	}
	if 3*tCnt != 2*eCnt-boundary {
		return fmt.Errorf("validate: edge relation 3T=%d != 2E-B=%d", 3*tCnt, 2*eCnt-boundary)
	}

	// Delaunay: no point strictly inside any triangle's circumcircle.
	for i, t := range tris {
		a, b, c := pts[t[0]], pts[t[1]], pts[t[2]]
		for j := 0; j < n; j++ {
			if j == t[0] || j == t[1] || j == t[2] {
				continue
			}
			if incircle(a, b, c, pts[j]) > 0 {
				return fmt.Errorf("validate: point %d strictly inside circumcircle of triangle %d", j, i)
			}
		}
	}
	return nil
}

func validateHull(out *Output, edgeTris map[[2]int][]int) error {
	n := len(out.Points)
	hull := out.Hull
	if len(hull) < 3 {
		return fmt.Errorf("validate: hull has %d vertices", len(hull))
	}
	seen := map[int]bool{}
	for _, h := range hull {
		if h < 0 || h >= n {
			return fmt.Errorf("validate: hull index %d out of range", h)
		}
		if seen[h] {
			return fmt.Errorf("validate: hull index %d repeated", h)
		}
		seen[h] = true
	}
	// Convexity (collinear boundary points allowed) and coverage.
	m := len(hull)
	for i := 0; i < m; i++ {
		a := out.Points[hull[i]]
		b := out.Points[hull[(i+1)%m]]
		c := out.Points[hull[(i+2)%m]]
		if orient(a, b, c) < 0 {
			return fmt.Errorf("validate: hull not convex at vertex %d", hull[(i+1)%m])
		}
		for j := 0; j < n; j++ {
			if orient(a, b, out.Points[j]) < 0 {
				return fmt.Errorf("validate: point %d outside hull edge %d->%d", j, hull[i], hull[(i+1)%m])
			}
		}
	}
	// Hull edges must be exactly the boundary edges of the triangulation.
	hullEdges := map[[2]int]int{}
	for i := 0; i < m; i++ {
		u, v := hull[i], hull[(i+1)%m]
		if u > v {
			u, v = v, u
		}
		hullEdges[[2]int{u, v}]++
	}
	for e, list := range edgeTris {
		if len(list) == 1 {
			ue := [2]int{e[0], e[1]}
			if hullEdges[ue] != 1 {
				return fmt.Errorf("validate: boundary edge %v missing from hull", e)
			}
			delete(hullEdges, ue)
		}
	}
	if len(hullEdges) != 0 {
		return fmt.Errorf("validate: hull contains non-boundary edges")
	}
	return nil
}

// trianglesOverlap reports whether two CCW triangles intersect with positive
// area: a vertex strictly inside the other, or a proper edge crossing.
func trianglesOverlap(pts []Point, t1, t2 [3]int) bool {
	a := [3]Point{pts[t1[0]], pts[t1[1]], pts[t1[2]]}
	b := [3]Point{pts[t2[0]], pts[t2[1]], pts[t2[2]]}
	for k := 0; k < 3; k++ {
		if strictlyInTriangle(a[k], b) || strictlyInTriangle(b[k], a) {
			return true
		}
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if segmentsProperCross(a[i], a[(i+1)%3], b[j], b[(j+1)%3]) {
				return true
			}
		}
	}
	return false
}

func strictlyInTriangle(p Point, t [3]Point) bool {
	return orient(t[0], t[1], p) > 0 &&
		orient(t[1], t[2], p) > 0 &&
		orient(t[2], t[0], p) > 0
}

func segmentsProperCross(a, b, c, d Point) bool {
	o1 := orient(a, b, c)
	o2 := orient(a, b, d)
	o3 := orient(c, d, a)
	o4 := orient(c, d, b)
	return o1*o2 < 0 && o3*o4 < 0
}
