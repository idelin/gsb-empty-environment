package main

import (
	"fmt"
	"math/big"
)

var bigOne = big.NewInt(1)

// AlgNum is an exact real algebraic number of the form u + v*sqrt(m),
// where u, v are rational and m is a squarefree integer >= 1.
//
// Normalized invariants (make the representation unique):
//   - v == 0  implies  m == 1   (plain rational)
//   - m == 1  implies  v == 0   (sqrt(1) folded into u)
//
// Because m is squarefree, two normalized values are equal iff all three
// components are equal: 1 and sqrt(m) are linearly independent over Q for
// squarefree m > 1, and sqrt(m1)/sqrt(m2) is irrational for distinct
// squarefree m1, m2 > 1.
type AlgNum struct {
	u *big.Rat
	v *big.Rat
	m *big.Int
}

// ratAlg wraps a rational number.
func ratAlg(x *big.Rat) *AlgNum {
	return &AlgNum{u: new(big.Rat).Set(x), v: new(big.Rat), m: big.NewInt(1)}
}

// mkAlg builds a normalized AlgNum. m must be squarefree (or 1).
func mkAlg(u, v *big.Rat, m *big.Int) *AlgNum {
	if v.Sign() == 0 {
		return ratAlg(u)
	}
	if m.Cmp(bigOne) == 0 {
		return ratAlg(new(big.Rat).Add(u, v))
	}
	return &AlgNum{u: new(big.Rat).Set(u), v: new(big.Rat).Set(v), m: new(big.Int).Set(m)}
}

func (a *AlgNum) Equal(b *AlgNum) bool {
	return a.m.Cmp(b.m) == 0 && a.u.Cmp(b.u) == 0 && a.v.Cmp(b.v) == 0
}

func (a *AlgNum) Sign() int { return a.Cmp(ratAlg(new(big.Rat))) }

// Cmp exactly compares two values. If they are not equal, rational interval
// refinement is guaranteed to separate them, so this always terminates.
func (a *AlgNum) Cmp(b *AlgNum) int {
	if a.Equal(b) {
		return 0
	}
	for prec := uint(1); ; prec *= 2 {
		alo, ahi := a.interval(prec)
		blo, bhi := b.interval(prec)
		// a - b lies in [alo-bhi, ahi-blo].
		lo := new(big.Rat).Sub(alo, bhi)
		hi := new(big.Rat).Sub(ahi, blo)
		if lo.Sign() > 0 {
			return 1
		}
		if hi.Sign() < 0 {
			return -1
		}
		if prec > 1<<22 {
			panic("AlgNum.Cmp: failed to separate distinct numbers")
		}
	}
}

// interval returns rational bounds lo <= a <= hi whose width shrinks like
// 2^-prec (times |v|).
func (a *AlgNum) interval(prec uint) (lo, hi *big.Rat) {
	lo = new(big.Rat).Set(a.u)
	hi = new(big.Rat).Set(a.u)
	if a.v.Sign() == 0 {
		return lo, hi
	}
	slo, shi := sqrtInterval(a.m, prec)
	if a.v.Sign() > 0 {
		lo.Add(lo, new(big.Rat).Mul(a.v, slo))
		hi.Add(hi, new(big.Rat).Mul(a.v, shi))
	} else {
		lo.Add(lo, new(big.Rat).Mul(a.v, shi))
		hi.Add(hi, new(big.Rat).Mul(a.v, slo))
	}
	return lo, hi
}

// sqrtInterval returns rationals lo <= sqrt(m) <= hi with hi-lo <= 2^-prec.
func sqrtInterval(m *big.Int, prec uint) (lo, hi *big.Rat) {
	scaled := new(big.Int).Lsh(m, 2*prec) // m * 4^prec
	s := new(big.Int).Sqrt(scaled)        // floor(sqrt(m * 4^prec))
	den := new(big.Int).Lsh(bigOne, prec)
	lo = new(big.Rat).SetFrac(s, den)
	if new(big.Int).Mul(s, s).Cmp(scaled) == 0 {
		return lo, new(big.Rat).Set(lo)
	}
	hi = new(big.Rat).SetFrac(new(big.Int).Add(s, bigOne), den)
	return lo, hi
}

// sqrtDecomp writes sqrt(x) = coeff*sqrt(m) for rational x >= 0, with
// coeff >= 0 rational and m squarefree.
func sqrtDecomp(x *big.Rat) (coeff *big.Rat, m *big.Int) {
	if x.Sign() == 0 {
		return new(big.Rat), big.NewInt(1)
	}
	// x = num/de in lowest terms; sqrt(x) = sqrt(num*den)/den.
	n := new(big.Int).Mul(x.Num(), x.Denom())
	f, sf := squarefreePart(n)
	return new(big.Rat).SetFrac(f, x.Denom()), sf
}

// squarefreePart factors n = f^2 * m with m squarefree.
func squarefreePart(n *big.Int) (f, m *big.Int) {
	f, m = big.NewInt(1), big.NewInt(1)
	r := new(big.Int).Set(n)
	p := big.NewInt(2)
	q, rem := new(big.Int), new(big.Int)
	for new(big.Int).Mul(p, p).Cmp(r) <= 0 {
		exp := 0
		for {
			q.QuoRem(r, p, rem)
			if rem.Sign() != 0 {
				break
			}
			r.Set(q)
			exp++
		}
		if exp%2 == 1 {
			m.Mul(m, p)
		}
		if exp >= 2 {
			f.Mul(f, new(big.Int).Exp(p, big.NewInt(int64(exp/2)), nil))
		}
		p.Add(p, bigOne)
	}
	if r.Cmp(bigOne) > 0 { // leftover is squarefree
		m.Mul(m, r)
	}
	return f, m
}

func (a *AlgNum) approx() float64 {
	fu := new(big.Float).SetPrec(128).SetRat(a.u)
	if a.v.Sign() != 0 {
		sm := new(big.Float).SetPrec(128).Sqrt(new(big.Float).SetPrec(128).SetInt(a.m))
		fv := new(big.Float).SetPrec(128).SetRat(a.v)
		fu.Add(fu, fv.Mul(fv, sm))
	}
	f, _ := fu.Float64()
	return f
}

func (a *AlgNum) String() string {
	if a.v.Sign() == 0 {
		return a.u.RatString()
	}
	vs := new(big.Rat).Abs(a.v).RatString()
	ms := a.m.String()
	if a.u.Sign() == 0 {
		if a.v.Sign() < 0 {
			return fmt.Sprintf("-%s*sqrt(%s)", vs, ms)
		}
		return fmt.Sprintf("%s*sqrt(%s)", vs, ms)
	}
	sign := " + "
	if a.v.Sign() < 0 {
		sign = " - "
	}
	return fmt.Sprintf("%s%s%s*sqrt(%s)", a.u.RatString(), sign, vs, ms)
}
