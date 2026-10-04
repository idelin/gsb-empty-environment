package domain

import (
	"fmt"
	"math/big"
	"sort"
)

// DomainOf computes the exact piecewise domain of e for x ranging over
// the rational interval rng. Parts of the expression whose domain
// cannot be decided exactly are listed in Domain.Unsupported.
func DomainOf(e *Expr, rng Interval) Domain {
	d := domainOf(e, rng)
	d.Parts = normalize(d.Parts)
	return d
}

func domainOf(e *Expr, rng Interval) Domain {
	switch e.Kind {
	case KConst, KVar:
		return Domain{Parts: []Interval{rng}}
	case KAdd, KSub, KMul:
		return intersectDom(domainOf(e.L, rng), domainOf(e.R, rng))
	case KDiv:
		if identZero(e.R) {
			return Domain{} // division by an identically zero denominator
		}
		d := intersectDom(domainOf(e.L, rng), domainOf(e.R, rng))
		z, exact := zeros(e.R)
		if !exact {
			d.Unsupported = append(d.Unsupported,
				fmt.Sprintf("cannot exactly determine zeros of denominator %s; reported parts may still contain points where it vanishes", e.R))
			return d
		}
		d.Parts = removePoints(d.Parts, z)
		return d
	case KPow:
		return powDomain(e, rng)
	}
	return Domain{Unsupported: []string{fmt.Sprintf("unknown expression %s", e)}}
}

// powDomain handles base^(p/q) with p/q in lowest terms:
//   - q even: real branch requires base >= 0 (base > 0 if p/q < 0);
//     negative bases are rejected.
//   - q odd: defined for negative bases too.
//   - p/q < 0: base must be nonzero (0 to a negative power undefined).
//   - p/q == 0: constant 1 everywhere the base is defined (0^0 == 1).
func powDomain(e *Expr, rng Interval) Domain {
	base, exp := e.L, e.Exp
	dB := domainOf(base, rng)
	if exp.Sign() == 0 {
		return dB
	}
	if identZero(base) {
		if exp.Sign() > 0 {
			return dB // 0 to a positive power is 0
		}
		return Domain{} // 0 to a negative power is undefined
	}
	evenQ := exp.Denom().Bit(0) == 0
	var z []*big.Rat
	exact := true
	if evenQ || exp.Sign() < 0 {
		z, exact = zeros(base)
	}
	var d Domain
	if evenQ {
		if !exact {
			d = Domain{Unsupported: []string{
				fmt.Sprintf("cannot exactly determine where (%s) >= 0: its zeros are not exactly decidable", base),
			}}
		} else {
			parts, notes := nonnegParts(base, dB.Parts, z)
			d = Domain{Parts: parts, Unsupported: notes}
		}
		d.Unsupported = append(d.Unsupported, dB.Unsupported...)
	} else {
		d = dB
	}
	if exp.Sign() < 0 {
		if !exact {
			d.Unsupported = append(d.Unsupported,
				fmt.Sprintf("cannot exactly exclude zeros of (%s): 0 to a negative power is undefined", base))
		} else {
			d.Parts = removePoints(d.Parts, z)
		}
	}
	return d
}

// nonnegParts keeps the parts of ivs where base >= 0, given the exact
// zero set z of base. base is continuous on each of its domain
// intervals, so between consecutive zeros its sign is constant and can
// be decided at one rational sample point.
func nonnegParts(base *Expr, ivs []Interval, z []*big.Rat) ([]Interval, []string) {
	var out []Interval
	var notes []string
	for _, iv := range ivs {
		var cuts []*big.Rat
		for _, c := range z {
			if iv.Lo.Cmp(c) < 0 && c.Cmp(iv.Hi) < 0 {
				cuts = append(cuts, c)
			}
			if iv.contains(c) {
				out = append(out, Point(c)) // base == 0 is >= 0
			}
		}
		sort.Slice(cuts, func(i, j int) bool { return cuts[i].Cmp(cuts[j]) < 0 })
		lo, loOpen := iv.Lo, iv.LoOpen
		emit := func(hi *big.Rat, hiOpen bool) {
			seg := Interval{Lo: lo, Hi: hi, LoOpen: loOpen, HiOpen: hiOpen}
			if seg.empty() {
				return
			}
			s, ok := segSign(base, seg)
			if !ok {
				notes = append(notes,
					fmt.Sprintf("cannot exactly determine sign of (%s) on %s", base, seg))
				return
			}
			if s >= 0 {
				out = append(out, seg)
			}
		}
		for _, c := range cuts {
			emit(c, true)
			lo, loOpen = c, true
		}
		emit(iv.Hi, iv.HiOpen)
	}
	return normalize(out), notes
}

// segSign determines the exact sign of base on a segment by evaluating
// at one rational sample point (the midpoint, or the point itself for
// degenerate segments).
func segSign(base *Expr, seg Interval) (int, bool) {
	var m *big.Rat
	if seg.Lo.Cmp(seg.Hi) == 0 {
		m = seg.Lo
	} else {
		m = new(big.Rat).Add(seg.Lo, seg.Hi)
		m.Quo(m, big.NewRat(2, 1))
	}
	return signAt(base, m)
}

// signAt computes the exact sign of e at the rational point x.
// It reports failure when the sign cannot be decided exactly (e.g. a
// sum of terms of opposite signs with incommensurable magnitudes, or
// the expression being undefined at x).
func signAt(e *Expr, x *big.Rat) (int, bool) {
	if a, b, ok := affineOf(e); ok {
		v := new(big.Rat).Mul(a, x)
		v.Add(v, b)
		return v.Sign(), true
	}
	switch e.Kind {
	case KAdd, KSub:
		sl, ok1 := signAt(e.L, x)
		sr, ok2 := signAt(e.R, x)
		if !ok1 || !ok2 {
			return 0, false
		}
		if e.Kind == KSub {
			sr = -sr
		}
		switch {
		case sl == 0:
			return sr, true
		case sr == 0:
			return sl, true
		case sl == sr:
			return sl, true
		}
		return 0, false
	case KMul:
		sl, ok1 := signAt(e.L, x)
		sr, ok2 := signAt(e.R, x)
		if !ok1 || !ok2 {
			return 0, false
		}
		return sl * sr, true
	case KDiv:
		sl, ok1 := signAt(e.L, x)
		sr, ok2 := signAt(e.R, x)
		if !ok1 || !ok2 || sr == 0 {
			return 0, false
		}
		return sl * sr, true
	case KPow:
		sb, ok := signAt(e.L, x)
		if !ok {
			return 0, false
		}
		switch {
		case sb > 0:
			return 1, true
		case sb == 0:
			switch e.Exp.Sign() {
			case 1:
				return 0, true
			case 0:
				return 1, true // 0^0 == 1 by convention
			}
			return 0, false // 0 to a negative power: undefined
		default: // sb < 0
			if e.Exp.Denom().Bit(0) == 0 {
				return 0, false // even denominator: no real branch
			}
			if e.Exp.Num().Bit(0) == 0 {
				return 1, true
			}
			return -1, true
		}
	}
	return 0, false
}

// zeros returns the exact finite zero set of e, or ok == false when it
// cannot be decided exactly. Callers must rule out identically zero
// expressions with identZero first.
func zeros(e *Expr) (z []*big.Rat, ok bool) {
	if a, b, ok := affineOf(e); ok {
		if a.Sign() == 0 {
			return nil, true // nonzero constant: no zeros
		}
		r := new(big.Rat).Neg(b)
		r.Quo(r, a)
		return []*big.Rat{r}, true
	}
	switch e.Kind {
	case KMul:
		zl, ok1 := zeros(e.L)
		zr, ok2 := zeros(e.R)
		if !ok1 || !ok2 {
			return nil, false
		}
		return unionPts(zl, zr), true
	case KDiv:
		zl, ok1 := zeros(e.L)
		zr, ok2 := zeros(e.R)
		if !ok1 || !ok2 {
			return nil, false
		}
		return diffPts(zl, zr), true
	case KPow:
		if e.Exp.Sign() > 0 {
			return zeros(e.L)
		}
		return nil, true // nonpositive exponents never produce zeros
	}
	return nil, false
}

// identZero reports whether e is provably the zero function.
func identZero(e *Expr) bool {
	if a, b, ok := affineOf(e); ok {
		return a.Sign() == 0 && b.Sign() == 0
	}
	switch e.Kind {
	case KMul:
		return identZero(e.L) || identZero(e.R)
	case KDiv:
		return identZero(e.L)
	case KPow:
		return e.Exp.Sign() > 0 && identZero(e.L)
	}
	return false
}

// affineOf reports whether e is an affine function a*x + b with
// rational coefficients, and returns them.
func affineOf(e *Expr) (a, b *big.Rat, ok bool) {
	switch e.Kind {
	case KConst:
		return new(big.Rat), new(big.Rat).Set(e.Val), true
	case KVar:
		return big.NewRat(1, 1), new(big.Rat), true
	case KAdd, KSub:
		a1, b1, ok1 := affineOf(e.L)
		a2, b2, ok2 := affineOf(e.R)
		if !ok1 || !ok2 {
			return nil, nil, false
		}
		if e.Kind == KSub {
			a2.Neg(a2)
			b2.Neg(b2)
		}
		return a1.Add(a1, a2), b1.Add(b1, b2), true
	case KMul:
		if e.L.Kind == KConst {
			if a2, b2, ok := affineOf(e.R); ok {
				return a2.Mul(a2, e.L.Val), b2.Mul(b2, e.L.Val), true
			}
		}
		if e.R.Kind == KConst {
			if a1, b1, ok := affineOf(e.L); ok {
				return a1.Mul(a1, e.R.Val), b1.Mul(b1, e.R.Val), true
			}
		}
	case KDiv:
		if e.R.Kind == KConst && e.R.Val.Sign() != 0 {
			if a1, b1, ok := affineOf(e.L); ok {
				return a1.Quo(a1, e.R.Val), b1.Quo(b1, e.R.Val), true
			}
		}
	}
	return nil, nil, false
}

func unionPts(a, b []*big.Rat) []*big.Rat {
	out := append([]*big.Rat{}, a...)
	for _, p := range b {
		if !containsPt(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func diffPts(a, b []*big.Rat) []*big.Rat {
	var out []*big.Rat
	for _, p := range a {
		if !containsPt(b, p) {
			out = append(out, p)
		}
	}
	return out
}

func containsPt(set []*big.Rat, p *big.Rat) bool {
	for _, q := range set {
		if q.Cmp(p) == 0 {
			return true
		}
	}
	return false
}

func intersectDom(a, b Domain) Domain {
	var parts []Interval
	for _, x := range a.Parts {
		for _, y := range b.Parts {
			if iv, ok := intersect(x, y); ok {
				parts = append(parts, iv)
			}
		}
	}
	notes := append(append([]string{}, a.Unsupported...), b.Unsupported...)
	return Domain{Parts: normalize(parts), Unsupported: notes}
}
