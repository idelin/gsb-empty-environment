// Package diskcover finds a closed disk of fixed rational radius that
// covers the maximum total weight of integer lattice points.
//
// All arithmetic is exact: candidate centers are algebraic numbers of the
// form a + b*sqrt(D) with rational a, b and a non-negative integer D, and
// every comparison is done with big.Rat / big.Int. No floating-point
// tolerance is used anywhere.
//
// Candidate enumeration follows the standard argument: an optimal disk can
// be translated/rotated without losing covered weight until either a covered
// point sits at its center (single-point candidate) or two covered points
// lie on its boundary (two-point candidate). Each candidate's weight is then
// recomputed exactly.
package diskcover

import (
	"errors"
	"fmt"
	"math/big"
)

// Point is an integer lattice point.
type Point struct {
	X, Y int64
}

// Quad represents A + B*sqrt(D), where D is stored on the enclosing
// AlgPoint and shared by both coordinates.
type Quad struct {
	A, B *big.Rat
}

// AlgPoint is a candidate disk center whose coordinates are quadratic
// surds: X = X.A + X.B*sqrt(D), Y = Y.A + Y.B*sqrt(D), with D a
// non-negative integer. D == 0 (or B == 0) means the coordinate is
// rational.
type AlgPoint struct {
	X, Y Quad
	D    *big.Int
}

var (
	ErrLengthMismatch = errors.New("diskcover: points and weights length mismatch")
	ErrNegativeRadius = errors.New("diskcover: radius must be a non-negative rational")
	ErrNegativeWeight = errors.New("diskcover: weights must be non-negative")
)

func ratInt64(v int64) *big.Rat { return new(big.Rat).SetInt64(v) }

func ratInt(v *big.Int) *big.Rat { return new(big.Rat).SetInt(v) }

func cloneRat(v *big.Rat) *big.Rat { return new(big.Rat).Set(v) }

func rationalCenter(x, y *big.Rat) AlgPoint {
	zero := new(big.Rat)
	return AlgPoint{
		X: Quad{A: x, B: zero},
		Y: Quad{A: y, B: cloneRat(zero)},
		D: new(big.Int),
	}
}

// Solve returns the maximum total weight coverable by a closed disk of the
// given radius, together with a center achieving it. If several centers are
// tied, the first one found in enumeration order is returned, so the result
// is deterministic.
func Solve(pts []Point, weights []int64, radius *big.Rat) (int64, AlgPoint, error) {
	if len(pts) != len(weights) {
		return 0, AlgPoint{}, ErrLengthMismatch
	}
	if radius == nil || radius.Sign() < 0 {
		return 0, AlgPoint{}, ErrNegativeRadius
	}
	for _, w := range weights {
		if w < 0 {
			return 0, AlgPoint{}, ErrNegativeWeight
		}
	}
	if len(pts) == 0 {
		return 0, rationalCenter(new(big.Rat), new(big.Rat)), nil
	}

	best := int64(-1)
	var bestCenter AlgPoint
	for _, c := range candidates(pts, radius) {
		if w := CoveredWeight(c, pts, weights, radius); w > best {
			best, bestCenter = w, c
		}
	}
	return best, bestCenter, nil
}

// candidates enumerates single-point centers and, for every pair of
// distinct points within 2*radius of each other, the (up to two) centers
// having both points on the boundary.
func candidates(pts []Point, radius *big.Rat) []AlgPoint {
	out := make([]AlgPoint, 0, len(pts))
	for _, p := range pts {
		out = append(out, rationalCenter(ratInt64(p.X), ratInt64(p.Y)))
	}

	// radius = a/b in lowest terms.
	a := radius.Num()
	b := radius.Denom()
	b2 := new(big.Int).Mul(b, b)
	fourA2 := new(big.Int).Mul(a, a)
	fourA2.Lsh(fourA2, 2)
	two := big.NewRat(2, 1)

	n := len(pts)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			dx := new(big.Int).SetInt64(pts[j].X - pts[i].X)
			dy := new(big.Int).SetInt64(pts[j].Y - pts[i].Y)
			d2 := new(big.Int).Add(new(big.Int).Mul(dx, dx), new(big.Int).Mul(dy, dy))
			if d2.Sign() == 0 {
				// Coincident points: no new center; the single-point
				// candidate already covers this case.
				continue
			}
			// Pair is usable iff d <= 2r, i.e. d2*b^2 <= 4a^2.
			N := new(big.Int).Sub(fourA2, new(big.Int).Mul(d2, b2))
			if N.Sign() < 0 {
				continue
			}
			mx := new(big.Rat).Quo(ratInt64(pts[i].X+pts[j].X), two)
			my := new(big.Rat).Quo(ratInt64(pts[i].Y+pts[j].Y), two)
			if N.Sign() == 0 {
				// Points exactly 2r apart: both boundary centers
				// collapse to the midpoint.
				out = append(out, rationalCenter(mx, my))
				continue
			}
			// Center = m +- sqrt(N/d2)/(2b) * (-dy, dx). Rewriting
			// sqrt(N/d2) = sqrt(N*d2)/d2 keeps the radical integral:
			// center = m +- sqrt(D)/(2*b*d2) * (-dy, dx) with D = N*d2.
			D := new(big.Int).Mul(N, d2)
			den := new(big.Int).Mul(d2, b)
			den.Lsh(den, 1)
			coef := new(big.Rat).SetFrac(big.NewInt(1), den)
			bx := new(big.Rat).Mul(coef, ratInt(dy))
			by := new(big.Rat).Mul(coef, ratInt(dx))
			out = append(out,
				AlgPoint{
					X: Quad{A: cloneRat(mx), B: new(big.Rat).Neg(bx)},
					Y: Quad{A: cloneRat(my), B: cloneRat(by)},
					D: new(big.Int).Set(D),
				},
				AlgPoint{
					X: Quad{A: cloneRat(mx), B: cloneRat(bx)},
					Y: Quad{A: cloneRat(my), B: new(big.Rat).Neg(by)},
					D: new(big.Int).Set(D),
				},
			)
		}
	}
	return out
}

// CoveredWeight recomputes, exactly, the total weight of points lying in the
// closed disk of the given radius around center.
func CoveredWeight(center AlgPoint, pts []Point, weights []int64, radius *big.Rat) int64 {
	r2 := new(big.Rat).Mul(radius, radius)
	var total int64
	for i, p := range pts {
		if coversPoint(center, p, r2) {
			total += weights[i]
		}
	}
	return total
}

// coversPoint decides |p - center|^2 <= r2 exactly. With
// center = (ax + bx*sqrt(D), ay + by*sqrt(D)) the squared distance is
// P + Q*sqrt(D) for rationals P, Q, which leqQuad compares against r2.
func coversPoint(c AlgPoint, p Point, r2 *big.Rat) bool {
	ux := new(big.Rat).Sub(ratInt64(p.X), c.X.A)
	vx := new(big.Rat).Neg(c.X.B)
	uy := new(big.Rat).Sub(ratInt64(p.Y), c.Y.A)
	vy := new(big.Rat).Neg(c.Y.B)

	uu := new(big.Rat).Add(new(big.Rat).Mul(ux, ux), new(big.Rat).Mul(uy, uy))
	vv := new(big.Rat).Add(new(big.Rat).Mul(vx, vx), new(big.Rat).Mul(vy, vy))
	P := new(big.Rat).Add(uu, new(big.Rat).Mul(vv, ratInt(c.D)))
	Q := new(big.Rat).Add(new(big.Rat).Mul(ux, vx), new(big.Rat).Mul(uy, vy))
	Q.Add(Q, Q)
	return leqQuad(P, Q, c.D, r2)
}

// leqQuad decides P + Q*sqrt(D) <= S exactly, where D is a non-negative
// integer and P, Q, S are rationals.
func leqQuad(P, Q *big.Rat, D *big.Int, S *big.Rat) bool {
	T := new(big.Rat).Sub(S, P) // need Q*sqrt(D) <= T
	switch Q.Sign() {
	case 0:
		return T.Sign() >= 0
	case 1:
		// sqrt(D) <= T/Q; impossible if T < 0.
		if T.Sign() < 0 {
			return false
		}
		ratio := new(big.Rat).Quo(T, Q)
		return ratInt(D).Cmp(new(big.Rat).Mul(ratio, ratio)) <= 0
	default:
		// sqrt(D) >= T/Q; automatic if T >= 0.
		if T.Sign() >= 0 {
			return true
		}
		ratio := new(big.Rat).Quo(T, Q)
		return ratInt(D).Cmp(new(big.Rat).Mul(ratio, ratio)) >= 0
	}
}

// String renders the center exactly, e.g. "(1, 1/4*sqrt(48))".
func (p AlgPoint) String() string {
	return fmt.Sprintf("(%s, %s)", quadString(p.X, p.D), quadString(p.Y, p.D))
}

func quadString(q Quad, D *big.Int) string {
	if q.B.Sign() == 0 {
		return q.A.RatString()
	}
	sign := "+"
	b := q.B
	if b.Sign() < 0 {
		sign = "-"
		b = new(big.Rat).Neg(b)
	}
	if q.A.Sign() == 0 {
		if sign == "-" {
			return fmt.Sprintf("-%s*sqrt(%s)", b.RatString(), D.String())
		}
		return fmt.Sprintf("%s*sqrt(%s)", b.RatString(), D.String())
	}
	return fmt.Sprintf("%s %s %s*sqrt(%s)", q.A.RatString(), sign, b.RatString(), D.String())
}
