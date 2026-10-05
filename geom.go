// Package delaunay computes a deterministic Delaunay triangulation of 2D
// integer points using exact arbitrary-precision arithmetic.
//
// Canonical result / tie-break rule (public contract):
//
//   - Duplicate coordinates are merged; all source IDs are retained.
//   - Points are inserted in lexicographic (x, y) order.
//   - The in-circle test is strict: a cocircular fourth point is NOT a
//     violation. Combined with the fixed insertion order and canonical
//     output normalization (triangles rotated to start at their smallest
//     vertex index and sorted lexicographically), the output is a unique
//     canonical triangulation that does not depend on the input order.
//
// Degenerate inputs: fewer than 3 distinct points, or all points collinear,
// produce an ordered chain instead of triangles.
package delaunay

import "math/big"

// Point is a 2D point with exact integer coordinates.
type Point struct {
	X, Y *big.Int
}

// Pt builds a Point from int64 coordinates.
func Pt(x, y int64) Point { return Point{big.NewInt(x), big.NewInt(y)} }

func isub(a, b *big.Int) *big.Int { return new(big.Int).Sub(a, b) }

// orient returns the sign of the cross product (b-a) x (c-a):
// > 0 if c is strictly left of the directed line a->b,
// < 0 if strictly right, 0 if collinear. Exact for any integer input.
func orient(a, b, c Point) int {
	abx := isub(b.X, a.X)
	aby := isub(b.Y, a.Y)
	acx := isub(c.X, a.X)
	acy := isub(c.Y, a.Y)
	t := new(big.Int).Mul(abx, acy)
	u := new(big.Int).Mul(aby, acx)
	return t.Sub(t, u).Sign()
}

// inCircle returns the sign of the in-circle determinant for the
// counterclockwise triangle (a, b, c):
// > 0 if p is strictly inside the circumcircle,
// 0 if p is cocircular with (a, b, c),
// < 0 if p is strictly outside. Exact for any integer input.
func inCircle(a, b, c, p Point) int {
	ax := isub(a.X, p.X)
	ay := isub(a.Y, p.Y)
	bx := isub(b.X, p.X)
	by := isub(b.Y, p.Y)
	cx := isub(c.X, p.X)
	cy := isub(c.Y, p.Y)

	sq := func(x, y *big.Int) *big.Int {
		x2 := new(big.Int).Mul(x, x)
		y2 := new(big.Int).Mul(y, y)
		return x2.Add(x2, y2)
	}
	cross := func(x1, y1, x2, y2 *big.Int) *big.Int {
		t := new(big.Int).Mul(x1, y2)
		u := new(big.Int).Mul(y1, x2)
		return t.Sub(t, u)
	}

	alift := sq(ax, ay)
	blift := sq(bx, by)
	clift := sq(cx, cy)

	det := new(big.Int).Mul(alift, cross(bx, by, cx, cy))
	det.Sub(det, new(big.Int).Mul(blift, cross(ax, ay, cx, cy)))
	det.Add(det, new(big.Int).Mul(clift, cross(ax, ay, bx, by)))
	return det.Sign()
}

// twiceArea returns twice the signed area of triangle (a, b, c);
// positive iff (a, b, c) is counterclockwise.
func twiceArea(a, b, c Point) *big.Int {
	abx := isub(b.X, a.X)
	aby := isub(b.Y, a.Y)
	acx := isub(c.X, a.X)
	acy := isub(c.Y, a.Y)
	t := new(big.Int).Mul(abx, acy)
	u := new(big.Int).Mul(aby, acx)
	return t.Sub(t, u)
}
