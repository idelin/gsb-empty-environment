package algcmp

import (
	"fmt"
	"math/big"
)

// AlgNum is a real algebraic number: the unique root of the square-free
// polynomial P inside the open interval (L, R).
type AlgNum struct {
	P    Poly // square-free part of the defining polynomial
	L, R *big.Rat
}

// NewAlgNum validates the isolating interval and returns the algebraic
// number.  The polynomial must be nonzero; repeated factors are removed
// by square-free decomposition.  It returns an error (never a silent
// guess) when the interval is not a valid isolating interval: lower <
// upper, neither endpoint is a root, and the interval contains exactly
// one distinct real root.
func NewAlgNum(p Poly, l, r *big.Rat) (AlgNum, error) {
	if p.IsZero() {
		return AlgNum{}, fmt.Errorf("polynomial must be nonzero")
	}
	if l == nil || r == nil {
		return AlgNum{}, fmt.Errorf("interval endpoints must be rational numbers")
	}
	if l.Cmp(r) >= 0 {
		return AlgNum{}, fmt.Errorf("invalid interval (%s, %s): lower endpoint must be less than upper endpoint", l.RatString(), r.RatString())
	}
	sf := SquareFree(p)
	if sf.SignAt(l) == 0 {
		return AlgNum{}, fmt.Errorf("invalid isolating interval (%s, %s): lower endpoint is a root of the polynomial", l.RatString(), r.RatString())
	}
	if sf.SignAt(r) == 0 {
		return AlgNum{}, fmt.Errorf("invalid isolating interval (%s, %s): upper endpoint is a root of the polynomial", l.RatString(), r.RatString())
	}
	n := CountRoots(sf, l, r)
	if n != 1 {
		return AlgNum{}, fmt.Errorf("invalid isolating interval (%s, %s): it contains %d distinct real roots, exactly one required", l.RatString(), r.RatString(), n)
	}
	return AlgNum{P: sf, L: new(big.Rat).Set(l), R: new(big.Rat).Set(r)}, nil
}

// Compare returns -1, 0 or +1 according as a < b, a == b or a > b,
// together with a checkable certificate.  Both inputs must have been
// validated by NewAlgNum.
//
// Equality is decided exactly: alpha == beta iff gcd(Pa, Pb) has a root
// in the intersection of the two isolating intervals.  Otherwise both
// intervals are refined by exact bisection until they are disjoint,
// which decides the order.  No fixed-precision approximation is used.
func Compare(a, b AlgNum) (int, *CertJSON) {
	if cert, ok := equalCert(a, b); ok {
		return 0, cert
	}
	A, B := a, b
	for {
		if A.R.Cmp(B.L) <= 0 {
			return -1, orderCert("less", A, B)
		}
		if B.R.Cmp(A.L) <= 0 {
			return 1, orderCert("greater", A, B)
		}
		A.refine()
		B.refine()
	}
}

// equalCert reports whether the two numbers are equal and, if so,
// produces the equality certificate.
func equalCert(a, b AlgNum) (*CertJSON, bool) {
	g := GCD(a.P, b.P)
	if g.Deg() < 1 {
		return nil, false
	}
	lo := maxRat(a.L, b.L)
	hi := minRat(a.R, b.R)
	if lo.Cmp(hi) >= 0 {
		return nil, false
	}
	// lo and hi are endpoints of a's or b's interval; g divides both
	// polynomials, so neither lo nor hi is a root of g.
	if CountRoots(g, lo, hi) == 0 {
		return nil, false
	}
	return &CertJSON{
		Result: "equal",
		Left:   IntervalJSON{Lower: a.L.RatString(), Upper: a.R.RatString()},
		Right:  IntervalJSON{Lower: b.L.RatString(), Upper: b.R.RatString()},
		GCD:    g.IntCoeffs(),
		Meet:   &IntervalJSON{Lower: lo.RatString(), Upper: hi.RatString()},
	}, true
}

func orderCert(result string, a, b AlgNum) *CertJSON {
	return &CertJSON{
		Result: result,
		Left:   IntervalJSON{Lower: a.L.RatString(), Upper: a.R.RatString()},
		Right:  IntervalJSON{Lower: b.L.RatString(), Upper: b.R.RatString()},
	}
}

// refine halves the isolating interval, keeping the unique root inside.
func (n *AlgNum) refine() {
	mid := avgRat(n.L, n.R)
	if n.P.SignAt(mid) == 0 {
		// The root is exactly the rational midpoint; shrink to a
		// symmetric neighbourhood whose endpoints are not roots.
		n.narrowAround(mid)
		return
	}
	if CountRoots(n.P, n.L, mid) == 1 {
		n.R = mid
	} else {
		n.L = mid
	}
}

// narrowAround replaces (L, R) by a sub-interval containing m (which is
// the root) whose endpoints are not roots of P.
func (n *AlgNum) narrowAround(m *big.Rat) {
	l := avgRat(n.L, m)
	for n.P.SignAt(l) == 0 {
		l = avgRat(l, m)
	}
	r := avgRat(m, n.R)
	for n.P.SignAt(r) == 0 {
		r = avgRat(m, r)
	}
	n.L, n.R = l, r
}

func avgRat(x, y *big.Rat) *big.Rat {
	return new(big.Rat).Quo(new(big.Rat).Add(x, y), big.NewRat(2, 1))
}

func maxRat(x, y *big.Rat) *big.Rat {
	if x.Cmp(y) >= 0 {
		return x
	}
	return y
}

func minRat(x, y *big.Rat) *big.Rat {
	if x.Cmp(y) <= 0 {
		return x
	}
	return y
}
