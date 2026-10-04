package main

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Root is an exact event time: either a rational number, or an irrational
// quadratic algebraic number u + v*sqrt(m) (m squarefree, v != 0) certified
// by a rational isolating interval (lo, hi) in which it is the unique root
// of poly. This is the "polynomial + rational isolating interval"
// representation; the closed form is kept alongside for equality tests and
// printing.
type Root struct {
	rat *big.Rat // non-nil iff rational

	u, v   *big.Rat // irrational: value = u + v*sqrt(m)
	m      *big.Int // squarefree integer > 1
	poly   Quad     // a polynomial having this root
	lo, hi *big.Rat // rational isolating interval, lo < root < hi
	loSign int      // sign of poly(lo); poly(hi) has the opposite sign
}

// RationalRoot wraps a rational event time.
func RationalRoot(r *big.Rat) *Root { return &Root{rat: new(big.Rat).Set(r)} }

// IsRational reports whether the root is rational.
func (r *Root) IsRational() bool { return r.rat != nil }

// bounds returns rational lo <= root <= hi.
func (r *Root) bounds() (lo, hi *big.Rat) {
	if r.rat != nil {
		return r.rat, r.rat
	}
	return r.lo, r.hi
}

// refine halves the isolating interval of an irrational root, keeping the
// root inside. The polynomial has no rational root (its discriminant is not
// a square), so the midpoint never lands exactly on the root.
func (r *Root) refine() {
	mid := new(big.Rat).Add(r.lo, r.hi)
	mid.Quo(mid, ratInt(2))
	s := r.poly.Eval(mid).Sign()
	if s == 0 {
		panic("Root.refine: hit a rational root of an irreducible quadratic")
	}
	if s == r.loSign {
		r.lo = mid
	} else {
		r.hi = mid
	}
}

// sameIrrational reports whether two irrational roots denote the same
// algebraic number. For squarefree m, Q(sqrt(m)) determines m, and the
// representation u + v*sqrt(m) is unique, so equality is decidable exactly.
func sameIrrational(x, y *Root) bool {
	return x.rat == nil && y.rat == nil &&
		x.m.Cmp(y.m) == 0 && x.u.Cmp(y.u) == 0 && x.v.Cmp(y.v) == 0
}

// CompareRoots compares two event times exactly, refining isolating
// intervals as needed. It returns -1, 0 or +1. Equality of distinct real
// numbers is always decided in finitely many refinements; equal numbers are
// detected symbolically, so termination is guaranteed.
func CompareRoots(x, y *Root) int {
	for {
		if x.rat != nil && y.rat != nil {
			return x.rat.Cmp(y.rat)
		}
		if sameIrrational(x, y) {
			return 0
		}
		xlo, xhi := x.bounds()
		ylo, yhi := y.bounds()
		if xhi.Cmp(ylo) < 0 {
			return -1
		}
		if yhi.Cmp(xlo) < 0 {
			return 1
		}
		// Intervals still overlap; shrink the irrational side(s). A
		// rational can never equal an irrational root, and unequal
		// irrationals are eventually separated.
		if x.rat == nil {
			x.refine()
		}
		if y.rat == nil {
			y.refine()
		}
	}
}

// Approx returns a float64 approximation, for display only.
func (r *Root) Approx() float64 {
	if r.rat != nil {
		f, _ := r.rat.Float64()
		return f
	}
	mf, _ := new(big.Float).SetInt(r.m).Float64()
	uf, _ := r.u.Float64()
	vf, _ := r.v.Float64()
	return uf + vf*math.Sqrt(mf)
}

// ExactString renders the exact value: "3/2" for rationals, or a closed
// form like "1/2 - 3/2*sqrt(5)" for irrationals.
func (r *Root) ExactString() string {
	if r.rat != nil {
		return ratStr(r.rat)
	}
	var sb strings.Builder
	vs := ratStr(new(big.Rat).Abs(r.v))
	core := "sqrt(" + r.m.String() + ")"
	if vs != "1" {
		core = vs + "*" + core
	}
	if r.u.Sign() == 0 {
		if r.v.Sign() < 0 {
			sb.WriteString("-")
		}
		sb.WriteString(core)
	} else {
		sb.WriteString(ratStr(r.u))
		if r.v.Sign() < 0 {
			sb.WriteString(" - ")
		} else {
			sb.WriteString(" + ")
		}
		sb.WriteString(core)
	}
	return sb.String()
}

// String returns the exact value plus a decimal approximation and the
// current rational isolating interval for irrationals.
func (r *Root) String() string {
	if r.rat != nil {
		return ratStr(r.rat)
	}
	loF, _ := r.lo.Float64()
	hiF, _ := r.hi.Float64()
	return r.ExactString() +
		" =~ " + strconv.FormatFloat(r.Approx(), 'g', 10, 64) +
		" in (" + strconv.FormatFloat(loF, 'g', 10, 64) +
		", " + strconv.FormatFloat(hiF, 'g', 10, 64) + ")"
}
