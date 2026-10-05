package delaunay

import (
	"fmt"
	"math/big"
	"sort"
)

// Validate independently checks that out is a correct Delaunay
// triangulation of the given raw input points. It verifies:
//
//   - point dedup/merge consistency (coordinates and source IDs)
//   - the output mode matches the input degeneracy class
//   - every triangle is counterclockwise with distinct vertices
//   - every distinct point is used by some triangle (or the chain)
//   - edges are manifold (<= 2 triangles per edge) and adjacency is
//     symmetric and consistent with the triangle list
//   - Euler relations: V - E + F = 1 and 3F = 2E - B
//   - faces do not overlap: exact triangle area sum equals hull area
//   - the hull is convex, CCW, covers every point, and its edges are
//     exactly the boundary edges of the triangulation
//   - Delaunay property: no point lies strictly inside any triangle's
//     circumcircle (cocircular is allowed)
//
// It returns nil on success or the first violation found.
func Validate(raw []RawPoint, out *Output) error {
	if out == nil {
		return fmt.Errorf("nil output")
	}
	pts, err := out.GeomPoints()
	if err != nil {
		return err
	}
	if err := checkDedup(raw, out); err != nil {
		return err
	}
	wantMode := expectedMode(pts)
	if out.Mode != wantMode {
		return fmt.Errorf("mode = %q, want %q", out.Mode, wantMode)
	}
	switch out.Mode {
	case ModeEmpty:
		return nil
	case ModePoint, ModeSegment, ModeCollinear:
		return validateChain(pts, out)
	default:
		return validateTriangulation(pts, out)
	}
}

func expectedMode(pts []Point) string {
	switch {
	case len(pts) == 0:
		return ModeEmpty
	case len(pts) == 1:
		return ModePoint
	case len(pts) == 2:
		return ModeSegment
	case allCollinear(pts):
		return ModeCollinear
	default:
		return ModeTriangulation
	}
}

// checkDedup verifies that the output points are exactly the distinct input
// coordinates, each carrying the IDs of all inputs that mapped to it.
func checkDedup(raw []RawPoint, out *Output) error {
	want := make(map[string][]string)
	for _, p := range raw {
		k := coordKey(p.X, p.Y)
		want[k] = append(want[k], p.ID)
	}
	if len(out.Points) != len(want) {
		return fmt.Errorf("output has %d distinct points, input has %d", len(out.Points), len(want))
	}
	seen := make(map[string]bool)
	for _, op := range out.Points {
		x, _ := new(big.Int).SetString(string(op.X), 10)
		y, _ := new(big.Int).SetString(string(op.Y), 10)
		k := coordKey(x, y)
		wids, ok := want[k]
		if !ok {
			return fmt.Errorf("output point %d at %s not present in input", op.Index, k)
		}
		if seen[k] {
			return fmt.Errorf("duplicate output point at %s", k)
		}
		seen[k] = true
		got := append([]string(nil), op.IDs...)
		exp := append([]string(nil), wids...)
		sort.Strings(got)
		sort.Strings(exp)
		if fmt.Sprint(got) != fmt.Sprint(exp) {
			return fmt.Errorf("point at %s: IDs %v, want %v", k, op.IDs, wids)
		}
	}
	return nil
}

// validateChain checks the degenerate (point/segment/collinear) outputs.
func validateChain(pts []Point, out *Output) error {
	if len(out.Triangles) != 0 {
		return fmt.Errorf("mode %q must not have triangles", out.Mode)
	}
	if len(out.Chain) != len(pts) {
		return fmt.Errorf("chain has %d points, want %d", len(out.Chain), len(pts))
	}
	seen := make(map[int]bool)
	for _, idx := range out.Chain {
		if idx < 0 || idx >= len(pts) {
			return fmt.Errorf("chain index %d out of range", idx)
		}
		if seen[idx] {
			return fmt.Errorf("chain repeats point %d", idx)
		}
		seen[idx] = true
	}
	// Strictly monotone along the supporting line and collinear throughout.
	for i := 0; i+1 < len(out.Chain); i++ {
		a := pts[out.Chain[i]]
		b := pts[out.Chain[i+1]]
		cx := a.X.Cmp(b.X)
		cy := a.Y.Cmp(b.Y)
		if cx > 0 || (cx == 0 && cy >= 0) {
			return fmt.Errorf("chain not ordered at position %d", i)
		}
		if i+2 < len(out.Chain) {
			if orient(a, b, pts[out.Chain[i+2]]) != 0 {
				return fmt.Errorf("chain not collinear at position %d", i)
			}
		}
	}
	return nil
}

func validateTriangulation(pts []Point, out *Output) error {
	n := len(pts)
	tris := out.Triangles
	if len(tris) == 0 {
		return fmt.Errorf("triangulation mode with no triangles")
	}
	if len(out.Adjacency) != len(tris) {
		return fmt.Errorf("adjacency has %d entries, want %d", len(out.Adjacency), len(tris))
	}

	used := make(map[int]bool)
	edgeCount := make(map[uedge]int)
	edgeDir := make(map[uedge]uedge)
	totalArea := new(big.Int)

	for ti, tr := range tris {
		a, b, c := tr[0], tr[1], tr[2]
		for _, v := range tr {
			if v < 0 || v >= n {
				return fmt.Errorf("triangle %d: vertex %d out of range", ti, v)
			}
			used[v] = true
		}
		if a == b || b == c || a == c {
			return fmt.Errorf("triangle %d: repeated vertex", ti)
		}
		if orient(pts[a], pts[b], pts[c]) <= 0 {
			return fmt.Errorf("triangle %d %v is not counterclockwise", ti, tr)
		}
		totalArea.Add(totalArea, twiceArea(pts[a], pts[b], pts[c]))
		for e := 0; e < 3; e++ {
			u, v := tr[e], tr[(e+1)%3]
			k := makeEdge(u, v)
			edgeCount[k]++
			edgeDir[k] = uedge{u, v}
		}
	}
	for i := 0; i < n; i++ {
		if !used[i] {
			return fmt.Errorf("point %d is not used by any triangle", i)
		}
	}

	boundary := 0
	for k, cnt := range edgeCount {
		if cnt > 2 {
			return fmt.Errorf("edge %v shared by %d triangles (non-manifold)", k, cnt)
		}
		if cnt == 1 {
			boundary++
		}
	}

	// Euler relations for a triangulation of a point set:
	// V - E + F = 1 and 3F = 2E - B.
	V, E, F := n, len(edgeCount), len(tris)
	if V-E+F != 1 {
		return fmt.Errorf("Euler relation violated: V-E+F = %d-%d+%d = %d, want 1", V, E, F, V-E+F)
	}
	if 3*F != 2*E-boundary {
		return fmt.Errorf("edge relation violated: 3F=%d, 2E-B=%d", 3*F, 2*E-boundary)
	}

	if err := checkAdjacency(tris, out.Adjacency); err != nil {
		return err
	}
	hullArea, err := checkHull(pts, out.Hull, edgeCount, edgeDir, n)
	if err != nil {
		return err
	}
	// Non-overlap + full coverage: every triangle lies inside the convex
	// hull, so equal total area implies pairwise-disjoint interiors.
	if totalArea.Cmp(hullArea) != 0 {
		return fmt.Errorf("area mismatch: triangle area sum %s != hull area %s (overlap or gap)",
			totalArea, hullArea)
	}
	return checkDelaunay(pts, tris)
}

// checkAdjacency recomputes adjacency from the triangle list and requires
// an exact match, which enforces symmetry.
func checkAdjacency(tris [][3]int, adj [][3]int) error {
	want := adjacencyOf(tris)
	for ti := range tris {
		for slot := 0; slot < 3; slot++ {
			if adj[ti][slot] != want[ti][slot] {
				return fmt.Errorf("adjacency[%d][%d] = %d, want %d (asymmetric or wrong)",
					ti, slot, adj[ti][slot], want[ti][slot])
			}
		}
	}
	return nil
}

// checkHull verifies the hull loop: convex, CCW, covers all points, and its
// edges are exactly the boundary edges of the triangulation. Returns twice
// the hull area.
func checkHull(pts []Point, hull []int, edgeCount map[uedge]int, edgeDir map[uedge]uedge, n int) (*big.Int, error) {
	if len(hull) < 3 {
		return nil, fmt.Errorf("hull has %d vertices, want >= 3", len(hull))
	}
	seen := make(map[int]bool)
	for _, v := range hull {
		if v < 0 || v >= n {
			return nil, fmt.Errorf("hull vertex %d out of range", v)
		}
		if seen[v] {
			return nil, fmt.Errorf("hull repeats vertex %d", v)
		}
		seen[v] = true
	}
	h := len(hull)
	area := new(big.Int)
	for i := 0; i < h; i++ {
		a := pts[hull[i]]
		b := pts[hull[(i+1)%h]]
		// Boundary edge of the triangulation, directed CCW.
		k := makeEdge(hull[i], hull[(i+1)%h])
		if edgeCount[k] != 1 {
			return nil, fmt.Errorf("hull edge (%d,%d) is not a boundary edge", hull[i], hull[(i+1)%h])
		}
		if d := edgeDir[k]; d.a != hull[i] || d.b != hull[(i+1)%h] {
			return nil, fmt.Errorf("hull edge (%d,%d) directed inconsistently with triangulation", hull[i], hull[(i+1)%h])
		}
		// Convexity (collinear consecutive vertices allowed).
		c := pts[hull[(i+2)%h]]
		if orient(a, b, c) < 0 {
			return nil, fmt.Errorf("hull is concave or clockwise at vertex %d", hull[(i+1)%h])
		}
		// Shoelace term.
		term := new(big.Int).Mul(a.X, b.Y)
		term.Sub(term, new(big.Int).Mul(a.Y, b.X))
		area.Add(area, term)
	}
	if area.Sign() <= 0 {
		return nil, fmt.Errorf("hull is not counterclockwise (signed area %s)", area)
	}
	// Coverage: every point must lie inside or on the convex hull.
	for i := 0; i < n; i++ {
		for e := 0; e < h; e++ {
			if orient(pts[hull[e]], pts[hull[(e+1)%h]], pts[i]) < 0 {
				return nil, fmt.Errorf("point %d lies outside the hull", i)
			}
		}
	}
	// Every boundary edge must appear on the hull loop.
	bcount := 0
	for _, cnt := range edgeCount {
		if cnt == 1 {
			bcount++
		}
	}
	if bcount != h {
		return nil, fmt.Errorf("triangulation has %d boundary edges but hull has %d", bcount, h)
	}
	return area, nil
}

// checkDelaunay verifies that no point lies strictly inside the
// circumcircle of any triangle.
func checkDelaunay(pts []Point, tris [][3]int) error {
	for ti, tr := range tris {
		a, b, c := pts[tr[0]], pts[tr[1]], pts[tr[2]]
		for i, p := range pts {
			if i == tr[0] || i == tr[1] || i == tr[2] {
				continue
			}
			if inCircle(a, b, c, p) > 0 {
				return fmt.Errorf("point %d strictly inside circumcircle of triangle %d %v", i, ti, tr)
			}
		}
	}
	return nil
}
