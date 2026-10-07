package convexhull

import (
	"fmt"
	"math/big"
)

// Verify independently checks that h is the correct convex hull of pts,
// using only exact rational arithmetic. It confirms:
//
//   - vertex ids are valid, distinct in coordinates, and strictly
//     counterclockwise (no 180-degree turns, so only extreme points);
//   - edges connect consecutive vertices and each carries exactly the
//     ids of the input points collinear with its segment;
//   - every input point lies inside or on the hull (left of or on every
//     directed edge; on the segment in the degenerate cases);
//   - every hull edge is a supporting line of the point set, and
//     exhaustively, every supporting line through any pair of input
//     points is realized by a hull edge whose segment covers all input
//     points on that line.
//
// It returns nil when the hull is correct and a descriptive error
// otherwise.
func Verify(pts []Point, h Hull) error {
	if len(pts) == 0 {
		if len(h.Vertices) != 0 || len(h.Edges) != 0 {
			return fmt.Errorf("empty input must yield an empty hull")
		}
		return nil
	}
	if len(h.Vertices) == 0 {
		return fmt.Errorf("hull has no vertices for non-empty input")
	}

	// Vertex ids valid and coordinate-wise distinct.
	seen := make(map[coordKey]bool)
	for _, id := range h.Vertices {
		if id < 0 || id >= len(pts) {
			return fmt.Errorf("vertex id %d out of range", id)
		}
		k := keyOf(pts[id])
		if seen[k] {
			return fmt.Errorf("vertices share coordinates %v", k)
		}
		seen[k] = true
	}
	nv := len(h.Vertices)

	// Edge count and connectivity must match the vertex ring.
	wantEdges := nv
	if nv <= 2 {
		wantEdges = nv - 1
	}
	if len(h.Edges) != wantEdges {
		return fmt.Errorf("got %d edges, want %d", len(h.Edges), wantEdges)
	}
	for i, e := range h.Edges {
		if e.From != h.Vertices[i] || e.To != h.Vertices[(i+1)%nv] {
			return fmt.Errorf("edge %d connects %d->%d, want %d->%d",
				i, e.From, e.To, h.Vertices[i], h.Vertices[(i+1)%nv])
		}
	}

	switch {
	case nv == 1:
		for i, p := range pts {
			if keyOf(p) != keyOf(pts[h.Vertices[0]]) {
				return fmt.Errorf("point %d differs from the single vertex", i)
			}
		}
		return nil
	case nv == 2:
		// Strictly increasing endpoints along the line, all points on
		// the segment.
		a, b := pts[h.Vertices[0]], pts[h.Vertices[1]]
		for i, p := range pts {
			if orientSign(a, b, p) != 0 || !onSegment(a, b, p) {
				return fmt.Errorf("point %d not on the degenerate hull segment", i)
			}
		}
	default:
		// Strictly counterclockwise: every consecutive triple makes a
		// strict left turn, which also proves positive signed area.
		for i := range h.Vertices {
			a := pts[h.Vertices[i]]
			b := pts[h.Vertices[(i+1)%nv]]
			c := pts[h.Vertices[(i+2)%nv]]
			if orientSign(a, b, c) <= 0 {
				return fmt.Errorf("vertices %d,%d,%d are not a strict left turn",
					h.Vertices[i], h.Vertices[(i+1)%nv], h.Vertices[(i+2)%nv])
			}
		}
		// Every point inside or on the hull; every edge supporting.
		for i, p := range pts {
			for _, e := range h.Edges {
				if orientSign(pts[e.From], pts[e.To], p) < 0 {
					return fmt.Errorf("point %d lies outside edge %d->%d", i, e.From, e.To)
				}
			}
		}
	}

	// Each edge must carry exactly the ids of input points on its
	// segment, in ascending order.
	for ei, e := range h.Edges {
		a, b := pts[e.From], pts[e.To]
		next := 0
		for i, p := range pts {
			on := orientSign(a, b, p) == 0 && onSegment(a, b, p)
			if on {
				if next >= len(e.Points) || e.Points[next] != i {
					return fmt.Errorf("edge %d: point %d on segment but missing from Points", ei, i)
				}
				next++
			}
		}
		if next != len(e.Points) {
			return fmt.Errorf("edge %d: Points lists %d ids not on the segment", ei, len(e.Points)-next)
		}
	}

	// Exhaustive supporting-line check: for every pair of distinct
	// input points, if the line through them leaves all points on one
	// side, a hull edge must realize exactly that line and cover every
	// input point lying on it.
	for i := 0; i < len(pts); i++ {
		for j := i + 1; j < len(pts); j++ {
			if keyOf(pts[i]) == keyOf(pts[j]) {
				continue
			}
			neg, pos := false, false
			for k := range pts {
				switch s := orientSign(pts[i], pts[j], pts[k]); {
				case s < 0:
					neg = true
				case s > 0:
					pos = true
				}
			}
			if neg && pos {
				continue // not a supporting line
			}
			if !neg && !pos {
				continue // fully collinear set; degenerate hull checked above
			}
			if err := checkSupportingEdge(pts, h, i, j); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkSupportingEdge requires that some hull edge lies on the line
// through pts[i], pts[j] and covers all input points on that line.
func checkSupportingEdge(pts []Point, h Hull, i, j int) error {
	a, b := pts[i], pts[j]
	for _, e := range h.Edges {
		f, t := pts[e.From], pts[e.To]
		if orientSign(a, b, f) != 0 || orientSign(a, b, t) != 0 {
			continue
		}
		covers := true
		for _, p := range pts {
			if orientSign(a, b, p) == 0 && !onSegment(f, t, p) {
				covers = false
				break
			}
		}
		if covers {
			return nil
		}
	}
	return fmt.Errorf("supporting line through points %d,%d is not realized by any hull edge", i, j)
}

// SignedArea2 returns twice the signed area of the vertex polygon,
// positive for counterclockwise order. Exported for tests and callers
// that need an exact area.
func SignedArea2(pts []Point, h Hull) *big.Rat {
	sum := new(big.Rat)
	nv := len(h.Vertices)
	for i := 0; i < nv; i++ {
		a := pts[h.Vertices[i]]
		b := pts[h.Vertices[(i+1)%nv]]
		term := new(big.Rat).Mul(a.X, b.Y)
		term.Sub(term, new(big.Rat).Mul(b.X, a.Y))
		sum.Add(sum, term)
	}
	return sum
}
