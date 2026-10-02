package algebraic

import (
	"fmt"
	"math/big"
)

// Num is a real algebraic number, represented exactly by a square-free
// annihilating polynomial over Q together with an isolating interval
// with rational endpoints that contains this number and no other root
// of the polynomial. All arithmetic and comparisons are exact; no
// floating point is ever used to decide relations between numbers.
// The minimal (irreducible) polynomial is available via MinimalPoly.
type Num struct {
	poly   Poly
	lo, hi *big.Rat
}

// Rational returns the algebraic number equal to q.
func Rational(q *big.Rat) Num {
	r := new(big.Rat).Set(q)
	return Num{
		poly: NewPoly(new(big.Rat).Neg(r), big.NewRat(1, 1)),
		lo:   new(big.Rat).Set(r),
		hi:   new(big.Rat).Set(r),
	}
}

// Rat returns the algebraic number equal to the integer x.
func Rat(x int64) Num { return Rational(big.NewRat(x, 1)) }

// RatFrac returns the algebraic number equal to n/d.
func RatFrac(n, d int64) Num { return Rational(big.NewRat(n, d)) }

// Poly returns the square-free annihilating polynomial of n used
// internally; use MinimalPoly for the irreducible minimal polynomial.
func (n Num) Poly() Poly { return n.poly }

// MinimalPoly returns the minimal polynomial of n over Q: the monic
// irreducible factor of n's annihilating polynomial whose root lies
// inside n's isolating interval.
func (n Num) MinimalPoly() Poly {
	irreds := Factor(n.poly)
	if len(irreds) <= 1 {
		return n.poly.Monic()
	}
	lo, hi := n.lo, n.hi
	type cand struct {
		p      Poly
		lo, hi *big.Rat
	}
	var cands []*cand
	for _, g := range irreds {
		for _, iv := range Isolate(g) {
			cands = append(cands, &cand{g, iv[0], iv[1]})
		}
	}
	for {
		var ov []*cand
		for _, c := range cands {
			if c.lo.Cmp(hi) <= 0 && lo.Cmp(c.hi) <= 0 {
				ov = append(ov, c)
			}
		}
		if len(ov) == 1 {
			return ov[0].p
		}
		for _, c := range ov {
			c.lo, c.hi = refineRoot(c.p, c.lo, c.hi)
		}
		lo, hi = refineRoot(n.poly, lo, hi)
	}
}

// Interval returns the current isolating interval of n.
func (n Num) Interval() (lo, hi *big.Rat) {
	return new(big.Rat).Set(n.lo), new(big.Rat).Set(n.hi)
}

// pickRoot selects, among the real roots of h, the one lying in the
// interval returned by bound. bound must always contain the target root;
// refineBound must shrink it. It is used to lift root bounds through
// arithmetic operations.
func pickRoot(h Poly, bound func() (lo, hi *big.Rat), refineBound func()) Num {
	factors := sqfrFactors(h)
	type cand struct {
		f      Poly
		lo, hi *big.Rat
	}
	var cands []*cand
	for _, f := range factors {
		for _, iv := range Isolate(f) {
			cands = append(cands, &cand{f, iv[0], iv[1]})
		}
	}
	for {
		lo, hi := bound()
		var ov []*cand
		for _, c := range cands {
			if c.lo.Cmp(hi) <= 0 && lo.Cmp(c.hi) <= 0 {
				ov = append(ov, c)
			}
		}
		if len(ov) == 1 {
			c := ov[0]
			return Num{poly: c.f, lo: c.lo, hi: c.hi}
		}
		for _, c := range ov {
			c.lo, c.hi = refineRoot(c.f, c.lo, c.hi)
		}
		refineBound()
	}
}

// Add returns a+b.
func Add(a, b Num) Num {
	h := polySumPoly(a.poly, b.poly)
	lo := new(big.Rat).Add(a.lo, b.lo)
	hi := new(big.Rat).Add(a.hi, b.hi)
	return pickRoot(h,
		func() (*big.Rat, *big.Rat) { return lo, hi },
		func() {
			a.lo, a.hi = refineRoot(a.poly, a.lo, a.hi)
			b.lo, b.hi = refineRoot(b.poly, b.lo, b.hi)
			lo = new(big.Rat).Add(a.lo, b.lo)
			hi = new(big.Rat).Add(a.hi, b.hi)
		})
}

// Neg returns -a.
func Neg(a Num) Num {
	return Num{
		poly: a.poly.NegX(),
		lo:   new(big.Rat).Neg(a.hi),
		hi:   new(big.Rat).Neg(a.lo),
	}
}

// Sub returns a-b.
func Sub(a, b Num) Num { return Add(a, Neg(b)) }

// Mul returns a*b.
func Mul(a, b Num) Num {
	h := polyProdPoly(a.poly, b.poly)
	bound := func() (*big.Rat, *big.Rat) {
		p1 := new(big.Rat).Mul(a.lo, b.lo)
		p2 := new(big.Rat).Mul(a.lo, b.hi)
		p3 := new(big.Rat).Mul(a.hi, b.lo)
		p4 := new(big.Rat).Mul(a.hi, b.hi)
		lo := minRat(minRat(p1, p2), minRat(p3, p4))
		hi := maxRat(maxRat(p1, p2), maxRat(p3, p4))
		return lo, hi
	}
	lo, hi := bound()
	return pickRoot(h,
		func() (*big.Rat, *big.Rat) { return lo, hi },
		func() {
			a.lo, a.hi = refineRoot(a.poly, a.lo, a.hi)
			b.lo, b.hi = refineRoot(b.poly, b.lo, b.hi)
			lo, hi = bound()
		})
}

// Sqrt returns the non-negative square root of a. It panics if a < 0.
func Sqrt(a Num) Num {
	s := a.Sign()
	if s < 0 {
		panic("algebraic: Sqrt of a negative number")
	}
	if s == 0 {
		return Rat(0)
	}
	h := a.poly.ComposeX2()
	bound := func() (*big.Rat, *big.Rat) {
		lo := a.lo
		if lo.Sign() < 0 {
			lo = new(big.Rat)
		}
		return sqrtFloorRat(lo), sqrtCeilRat(a.hi)
	}
	lo, hi := bound()
	return pickRoot(h,
		func() (*big.Rat, *big.Rat) { return lo, hi },
		func() {
			a.lo, a.hi = refineRoot(a.poly, a.lo, a.hi)
			lo, hi = bound()
		})
}

// Sign returns -1, 0 or +1 according to the sign of n, decided exactly.
// Zero is detected when the minimal polynomial has 0 as a root and the
// isolating interval contains 0; otherwise the interval is refined
// until it no longer contains 0.
func (n Num) Sign() int {
	lo, hi := n.lo, n.hi
	zero := new(big.Rat)
	for {
		if lo.Sign() <= 0 && hi.Sign() >= 0 {
			if n.poly.Eval(zero).Sign() == 0 {
				// The unique root in [lo,hi] is exactly 0.
				return 0
			}
			lo, hi = refineRoot(n.poly, lo, hi)
			continue
		}
		if hi.Sign() < 0 {
			return -1
		}
		return 1
	}
}

// Compare returns -1, 0 or +1 according to whether a < b, a == b or
// a > b. Equality of two different expressions (e.g. sqrt(2)+sqrt(3)
// and sqrt(5+2*sqrt(6))) is proved exactly: the difference polynomial
// has 0 as a root and the isolating interval of the difference
// contains 0.
func Compare(a, b Num) int { return Sub(a, b).Sign() }

// Tighten returns a copy of n whose isolating interval has width at
// most 2^-bits.
func (n Num) Tighten(bits uint) Num {
	lo, hi := n.lo, n.hi
	thresh := new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Lsh(big.NewInt(1), bits))
	for new(big.Rat).Sub(hi, lo).Cmp(thresh) > 0 {
		lo, hi = refineRoot(n.poly, lo, hi)
	}
	return Num{poly: n.poly, lo: lo, hi: hi}
}

// Approx returns a decimal approximation of n for display purposes
// only; it is never used to decide relations between numbers.
func (n Num) Approx(places uint) string {
	t := n.Tighten(4*places + 16)
	flo := new(big.Float).SetPrec(8*places + 64).SetRat(t.lo)
	fhi := new(big.Float).SetPrec(8*places + 64).SetRat(t.hi)
	mid := new(big.Float).SetPrec(8*places+64).Add(flo, fhi)
	mid.Quo(mid, big.NewFloat(2))
	return mid.Text('f', int(places))
}

func (n Num) String() string {
	return fmt.Sprintf("root of (%s) ~ %s", n.poly.String(), n.Approx(10))
}

func minRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

func maxRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}

// sqrtFloorRat returns a rational r >= 0 with r^2 <= q (q must be >= 0).
func sqrtFloorRat(q *big.Rat) *big.Rat {
	n := new(big.Int).Mul(q.Num(), q.Denom())
	s := new(big.Int).Sqrt(n)
	return new(big.Rat).SetFrac(s, q.Denom())
}

// sqrtCeilRat returns a rational r >= 0 with r^2 >= q (q must be >= 0).
func sqrtCeilRat(q *big.Rat) *big.Rat {
	n := new(big.Int).Mul(q.Num(), q.Denom())
	s := new(big.Int).Sqrt(n)
	if new(big.Int).Mul(s, s).Cmp(n) != 0 {
		s.Add(s, big.NewInt(1))
	}
	return new(big.Rat).SetFrac(s, q.Denom())
}
