package hull

import (
	"fmt"
	"math/big"
)

// Verify checks that h is exactly the convex hull of pts, using only exact
// rational predicates (no floating-point tolerance). It confirms:
//
//   - vertices are distinct input points in strict counterclockwise order;
//   - edges connect consecutive vertices cyclically and each edge's
//     Collinear list matches the exact set of input ids on the segment;
//   - every input point lies inside or on the hull (orient >= 0 for every
//     directed edge);
//   - exhaustively, every supporting line determined by a pair of distinct
//     input points corresponds to a hull edge, and the hull has no extra
//     edges.
//
// It returns nil on success and a descriptive error otherwise.
func Verify(pts []Point, h Hull) error {
	uq, ids := unique(pts)

	if len(uq) == 0 {
		if len(h.Vertices) != 0 || len(h.Edges) != 0 {
			return fmt.Errorf("empty cloud: hull must be empty, got %d vertices, %d edges",
				len(h.Vertices), len(h.Edges))
		}
		return nil
	}
	if len(uq) == 1 {
		if len(h.Vertices) != 1 || h.Vertices[0] != ids[0] || len(h.Edges) != 0 {
			return fmt.Errorf("single-point cloud: hull must be vertex %d with no edges", ids[0])
		}
		return nil
	}

	n := len(h.Vertices)
	if n < 2 {
		return fmt.Errorf("hull of %d distinct points must have >= 2 vertices, got %d", len(uq), n)
	}

	// Vertices: valid ids, distinct points.
	seen := make(map[string]bool, n)
	for _, v := range h.Vertices {
		if v < 0 || v >= len(pts) {
			return fmt.Errorf("vertex id %d out of range", v)
		}
		k := key(pts[v])
		if seen[k] {
			return fmt.Errorf("duplicate vertex point %s", k)
		}
		seen[k] = true
	}

	// Strict counterclockwise order (exact left turn at every vertex).
	if n >= 3 {
		for i := 0; i < n; i++ {
			a := pts[h.Vertices[i]]
			b := pts[h.Vertices[(i+1)%n]]
			c := pts[h.Vertices[(i+2)%n]]
			if sign(orient(a, b, c)) <= 0 {
				return fmt.Errorf("vertices not strictly CCW at index %d", (i+1)%n)
			}
		}
	}

	// Edges must be the cyclic consecutive pairs of vertices.
	if len(h.Edges) != n {
		return fmt.Errorf("expected %d edges, got %d", n, len(h.Edges))
	}
	edgeSet := make(map[[2]int]bool, n)
	for i, e := range h.Edges {
		if e.From != h.Vertices[i] || e.To != h.Vertices[(i+1)%n] {
			return fmt.Errorf("edge %d = (%d,%d), want (%d,%d)",
				i, e.From, e.To, h.Vertices[i], h.Vertices[(i+1)%n])
		}
		edgeSet[[2]int{e.From, e.To}] = true

		// Recompute the exact collinear id set for this segment.
		want := []int{}
		for id, p := range pts {
			if onSegment(pts[e.From], pts[e.To], p) {
				want = append(want, id)
			}
		}
		if !equalInts(e.Collinear, want) {
			return fmt.Errorf("edge (%d,%d): collinear ids %v, want %v",
				e.From, e.To, e.Collinear, want)
		}
	}

	// Every input point inside or on the hull.
	if n == 2 {
		a, b := pts[h.Vertices[0]], pts[h.Vertices[1]]
		for id, p := range pts {
			if !onSegment(a, b, p) {
				return fmt.Errorf("point %d outside degenerate hull segment", id)
			}
		}
	} else {
		for _, e := range h.Edges {
			for id, p := range pts {
				if sign(orient(pts[e.From], pts[e.To], p)) < 0 {
					return fmt.Errorf("point %d strictly right of edge (%d,%d)", id, e.From, e.To)
				}
			}
		}
	}

	// Exhaustive supporting-line check: for every ordered pair of distinct
	// points, if the line through them has the whole cloud on its left (or
	// on it), the extreme points on that line must form a hull edge.
	supporting := make(map[[2]int]bool)
	for i := 0; i < len(uq); i++ {
		for j := 0; j < len(uq); j++ {
			if i == j {
				continue
			}
			a, b := uq[i], uq[j]
			left := true
			for _, p := range uq {
				if sign(orient(a, b, p)) < 0 {
					left = false
					break
				}
			}
			if !left {
				continue
			}
			lo, hi := lineExtremes(uq, ids, a, b)
			supporting[[2]int{lo, hi}] = true
		}
	}
	for e := range supporting {
		if !edgeSet[e] {
			return fmt.Errorf("missing hull edge (%d,%d) for supporting line", e[0], e[1])
		}
	}
	for e := range edgeSet {
		if !supporting[e] {
			return fmt.Errorf("hull edge (%d,%d) is not a supporting line", e[0], e[1])
		}
	}
	return nil
}

// lineExtremes returns the input ids of the two extreme points among the
// unique points collinear with directed line a->b, ordered along a->b.
func lineExtremes(uq []Point, ids []int, a, b Point) (int, int) {
	dx := new(big.Rat).Sub(b.X, a.X)
	dy := new(big.Rat).Sub(b.Y, a.Y)
	proj := func(p Point) *big.Rat {
		px := new(big.Rat).Sub(p.X, a.X)
		py := new(big.Rat).Sub(p.Y, a.Y)
		t1 := new(big.Rat).Mul(px, dx)
		t2 := new(big.Rat).Mul(py, dy)
		return t1.Add(t1, t2)
	}
	lo, hi := -1, -1
	var loT, hiT *big.Rat
	for k, p := range uq {
		if sign(orient(a, b, p)) != 0 {
			continue
		}
		t := proj(p)
		if lo == -1 || t.Cmp(loT) < 0 {
			lo, loT = k, t
		}
		if hi == -1 || t.Cmp(hiT) > 0 {
			hi, hiT = k, t
		}
	}
	return ids[lo], ids[hi]
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
