package circlecover

import (
	"errors"
	"math/big"
)

// Point is an integer lattice point. Points with equal coordinates are
// treated as distinct entries: each contributes its own weight.
type Point struct {
	X, Y int64
}

// Center is a candidate disk center. Each coordinate is an algebraic
// number a + b*sqrt(s); purely rational centers have b == 0.
type Center struct {
	X, Y Alg
}

func (c Center) String() string {
	return "(" + c.X.String() + ", " + c.Y.String() + ")"
}

// Result is the outcome of Solve: the maximum coverable weight and one
// center attaining it. When several centers are tied, the first one in
// enumeration order (single points, then pairs) is returned.
type Result struct {
	Weight *big.Int
	Center Center
}

// Solve returns the maximum total weight coverable by a closed disk of
// the given non-negative rational radius, plus a center attaining it.
//
// Candidate centers are enumerated exactly:
//   - every input point (covers the case where the optimal disk only
//     covers points at one location, and anchors degenerate cases such
//     as radius 0);
//   - for every pair of points at distance d <= 2r, the two centers
//     putting both points on the boundary (they coincide when d == 2r).
//
// This suffices: among all disks covering an optimal set S, the
// intersection of radius-r disks around the points of S is non-empty and
// convex, and some vertex of its boundary (a pairwise circle
// intersection) or an interior point (reachable by a single-point
// candidate) also covers all of S.
func Solve(pts []Point, weights []int64, radius *big.Rat) (Result, error) {
	if len(pts) != len(weights) {
		return Result{}, errors.New("circlecover: points/weights length mismatch")
	}
	if radius == nil || radius.Sign() < 0 {
		return Result{}, errors.New("circlecover: radius must be non-negative")
	}
	for _, w := range weights {
		if w < 0 {
			return Result{}, errors.New("circlecover: weights must be non-negative")
		}
	}
	r2 := new(big.Rat).Mul(radius, radius)

	var best *big.Int
	var bestCenter Center
	consider := func(c Center) {
		w := coveredWeightSq(pts, weights, c, r2)
		if best == nil || w.Cmp(best) > 0 {
			best = w
			bestCenter = c
		}
	}
	for _, p := range pts {
		consider(pointCenter(p))
	}
	for i := 0; i < len(pts); i++ {
		for j := i + 1; j < len(pts); j++ {
			for _, c := range pairCenters(pts[i], pts[j], r2) {
				consider(c)
			}
		}
	}
	if best == nil {
		best = new(big.Int)
		bestCenter = pointCenter(Point{0, 0})
	}
	return Result{Weight: best, Center: bestCenter}, nil
}

// Covered reports whether p lies in the closed disk of the given rational
// radius around c. The comparison is exact; points exactly on the
// boundary count as covered.
func Covered(c Center, p Point, radius *big.Rat) bool {
	r2 := new(big.Rat).Mul(radius, radius)
	return lteqRat(distSquared(c, p), r2)
}

// CoveredWeight recomputes the total weight of the points covered by the
// closed disk of the given radius around c. It is exported so callers
// (and tests) can independently verify a reported center.
func CoveredWeight(pts []Point, weights []int64, c Center, radius *big.Rat) *big.Int {
	r2 := new(big.Rat).Mul(radius, radius)
	return coveredWeightSq(pts, weights, c, r2)
}

func coveredWeightSq(pts []Point, weights []int64, c Center, r2 *big.Rat) *big.Int {
	total := new(big.Int)
	for i, p := range pts {
		if lteqRat(distSquared(c, p), r2) {
			total.Add(total, big.NewInt(weights[i]))
		}
	}
	return total
}

func pointCenter(p Point) Center {
	return Center{X: algInt(p.X), Y: algInt(p.Y)}
}

// pairCenters returns the centers of radius-r disks having both p and q
// on the boundary, or nil if the pair cannot lie on one such disk.
//
// With d = |pq|, the centers are mid +/- sqrt(s) * (-dy, dx) where
// s = (4r^2 - d^2) / (4 d^2) >= 0, so each coordinate has the exact
// algebraic form a + b*sqrt(s) with rational a, b, s.
func pairCenters(p, q Point, r2 *big.Rat) []Center {
	dx := q.X - p.X
	dy := q.Y - p.Y
	if dx == 0 && dy == 0 {
		return nil // duplicate location: handled by single-point candidates
	}
	d2 := new(big.Rat).Add(mulRat(ratInt(dx), ratInt(dx)), mulRat(ratInt(dy), ratInt(dy)))
	four := big.NewRat(4, 1)
	fourR2 := new(big.Rat).Mul(four, r2)
	if d2.Cmp(fourR2) > 0 {
		return nil
	}
	s := new(big.Rat).Sub(fourR2, d2)
	s.Quo(s, new(big.Rat).Mul(four, d2))
	two := big.NewRat(2, 1)
	mx := new(big.Rat).Add(ratInt(p.X), ratInt(q.X))
	mx.Quo(mx, two)
	my := new(big.Rat).Add(ratInt(p.Y), ratInt(q.Y))
	my.Quo(my, two)

	c1 := Center{
		X: alg(mx, ratInt(-dy), s),
		Y: alg(my, ratInt(dx), s),
	}
	if s.Sign() == 0 {
		// d == 2r: the two boundary centers coincide at the midpoint.
		return []Center{c1}
	}
	c2 := Center{
		X: alg(mx, ratInt(dy), s),
		Y: alg(my, ratInt(-dx), s),
	}
	return []Center{c1, c2}
}

// distSquared computes the squared distance between center c and point p
// as an exact algebraic number. If c.X = ax + bx*sqrt(s) and likewise
// for Y, then with ux = p.X - ax, uy = p.Y - ay:
//
//	dist^2 = (ux^2 + uy^2) + (bx^2 + by^2)*s - 2*(ux*bx + uy*by)*sqrt(s)
func distSquared(c Center, p Point) Alg {
	s := c.X.S
	if s.Sign() == 0 {
		s = c.Y.S
	}
	ux := new(big.Rat).Sub(ratInt(p.X), c.X.A)
	uy := new(big.Rat).Sub(ratInt(p.Y), c.Y.A)
	u := new(big.Rat).Add(mulRat(ux, ux), mulRat(uy, uy))
	b2 := new(big.Rat).Add(mulRat(c.X.B, c.X.B), mulRat(c.Y.B, c.Y.B))
	u.Add(u, new(big.Rat).Mul(b2, s))
	v := new(big.Rat).Add(mulRat(ux, c.X.B), mulRat(uy, c.Y.B))
	v.Mul(v, big.NewRat(-2, 1))
	return Alg{A: u, B: v, S: s}
}
