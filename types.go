package dtri

import "math/big"

// Point is a 2D point with exact integer coordinates.
type Point struct {
	X, Y *big.Int
}

// InputPoint is a survey point as supplied by the caller. ID is the source
// identifier; several InputPoints may share the same coordinates, in which
// case they are merged and all their IDs are kept.
type InputPoint struct {
	ID   string
	X, Y *big.Int
}

// Status describes the shape of the result.
type Status string

const (
	StatusEmpty        Status = "empty"     // no input points
	StatusPoint        Status = "point"     // a single distinct point
	StatusSegment      Status = "segment"   // exactly two distinct points
	StatusCollinear    Status = "collinear" // 3+ points, all collinear
	StatusTriangulated Status = "triangulated"
)

// Output is the canonical triangulation result. It is canonical in the sense
// that any permutation of the same input multiset yields an identical Output.
//
// Points are the distinct input points, sorted lexicographically by (X, Y);
// IDs[i] holds the sorted source IDs merged into Points[i].
//
// Triangles are counter-clockwise vertex triples indexing into Points.
// Neighbors[i][k] is the index of the triangle sharing the edge opposite
// vertex k of triangle i, or -1 if that edge lies on the convex hull.
//
// Hull lists the boundary vertex indices in counter-clockwise order,
// including collinear points lying on hull edges.
//
// Chain is only set for degenerate inputs (StatusPoint/StatusSegment/
// StatusCollinear) and lists all point indices ordered along the line.
type Output struct {
	Status    Status
	Points    []Point
	IDs       [][]string
	Triangles [][3]int
	Neighbors [][3]int
	Hull      []int
	Chain     []int
}

func lessPoint(a, b Point) bool {
	if c := a.X.Cmp(b.X); c != 0 {
		return c < 0
	}
	return a.Y.Cmp(b.Y) < 0
}
