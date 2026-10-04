package domain

import (
	"fmt"
	"math/big"
)

// Sign is a (possibly coarse) sign classification of an expression on a
// region. SNonNeg/SNonPos mean the exact zero set could not be isolated
// but the sign does not change; SUnknown means nothing could be proved.
type Sign int

const (
	SNeg Sign = iota
	SZero
	SPos
	SNonNeg
	SNonPos
	SUnknown
)

func (s Sign) String() string {
	switch s {
	case SNeg:
		return "-"
	case SZero:
		return "0"
	case SPos:
		return "+"
	case SNonNeg:
		return ">=0"
	case SNonPos:
		return "<=0"
	}
	return "?"
}

// Region is an interval on which an expression has the given sign.
type Region struct {
	Interval
	Sign
}

func ratSign(r *big.Rat) Sign {
	switch r.Sign() {
	case 1:
		return SPos
	case -1:
		return SNeg
	}
	return SZero
}

// signPartition partitions dom into regions of constant sign for e.
// Undecidable parts become SUnknown regions and are also reported in the
// returned notes.
func signPartition(e *Expr, dom Domain) ([]Region, []string) {
	switch e.Kind {
	case KConst:
		rs := make([]Region, 0, len(dom.Intervals))
		for _, iv := range dom.Intervals {
			rs = append(rs, Region{iv, ratSign(e.C)})
		}
		return rs, nil
	case KVar:
		return affinePartition(big.NewRat(1, 1), big.NewRat(0, 1), dom), nil
	case KNeg:
		rs, notes := signPartition(e.A, dom)
		for i := range rs {
			rs[i].Sign = negSign(rs[i].Sign)
		}
		return rs, notes
	case KAdd, KSub:
		if a, b, ok := affine(e); ok {
			return affinePartition(a, b, dom), nil
		}
		var rs []Region
		var notes []string
		for _, iv := range dom.Intervals {
			s := constSign(e, iv)
			if s == SUnknown {
				notes = append(notes, fmt.Sprintf("cannot determine the sign of %s on %s exactly", e, iv))
			}
			rs = append(rs, Region{iv, s})
		}
		return rs, notes
	case KMul:
		ra, na := signPartition(e.A, dom)
		rb, nb := signPartition(e.B, dom)
		return combineRegions(ra, rb, mulSign), append(na, nb...)
	case KDiv:
		ra, na := signPartition(e.A, dom)
		rb, nb := signPartition(e.B, dom)
		return combineRegions(ra, rb, divSign), append(na, nb...)
	case KPow:
		rs, notes := signPartition(e.A, dom)
		p := e.Exp.Num().Int64()
		q := e.Exp.Denom().Int64()
		out := make([]Region, 0, len(rs))
		for _, r := range rs {
			if s, ok := powSign(r.Sign, p, q); ok {
				out = append(out, Region{r.Interval, s})
			}
		}
		return mergeRegions(out), notes
	}
	return nil, []string{fmt.Sprintf("unsupported expression %s", e)}
}

// affinePartition splits dom at the rational root of a*x+b.
func affinePartition(a, b *big.Rat, dom Domain) []Region {
	var rs []Region
	for _, iv := range dom.Intervals {
		if a.Sign() == 0 {
			rs = append(rs, Region{iv, ratSign(b)})
			continue
		}
		root := new(big.Rat).Quo(new(big.Rat).Neg(b), a)
		for _, piece := range splitAtPoint(iv, root) {
			rs = append(rs, Region{piece, affineSignAt(a, b, piece)})
		}
	}
	return mergeRegions(rs)
}

// affineSignAt evaluates the sign of a*x+b on a piece that does not
// contain the root in its interior: exactly at a singleton, by sampling
// the rational midpoint otherwise.
func affineSignAt(a, b *big.Rat, iv Interval) Sign {
	var x *big.Rat
	if iv.Lo.Val.Cmp(iv.Hi.Val) == 0 && !iv.Lo.Open && !iv.Hi.Open {
		x = iv.Lo.Val
	} else {
		x = new(big.Rat).Add(iv.Lo.Val, iv.Hi.Val)
		x.Quo(x, big.NewRat(2, 1))
	}
	v := new(big.Rat).Add(new(big.Rat).Mul(a, x), b)
	return ratSign(v)
}

// constSign tries to prove a constant sign for e on iv using exact
// rational interval arithmetic. Returns SUnknown when unprovable.
func constSign(e *Expr, iv Interval) Sign {
	b, ok := evalInterval(e, iv)
	if !ok {
		return SUnknown
	}
	ls, hs := b.lo.Sign(), b.hi.Sign()
	switch {
	case ls == 0 && hs == 0 && !b.loOpen && !b.hiOpen:
		return SZero
	case ls > 0 || (ls == 0 && b.loOpen):
		return SPos
	case hs < 0 || (hs == 0 && b.hiOpen):
		return SNeg
	case ls == 0:
		return SNonNeg
	case hs == 0:
		return SNonPos
	}
	return SUnknown
}

func negSign(s Sign) Sign {
	switch s {
	case SNeg:
		return SPos
	case SPos:
		return SNeg
	case SNonNeg:
		return SNonPos
	case SNonPos:
		return SNonNeg
	}
	return s
}

func mulSign(a, b Sign) (Sign, bool) {
	if a == SZero || b == SZero {
		return SZero, true
	}
	if a == SUnknown || b == SUnknown {
		return SUnknown, true
	}
	pos := func(s Sign) bool { return s == SPos || s == SNonNeg }
	neg := func(s Sign) bool { return s == SNeg || s == SNonPos }
	strictA := a == SPos || a == SNeg
	strictB := b == SPos || b == SNeg
	switch {
	case pos(a) && pos(b):
		if strictA && strictB {
			return SPos, true
		}
		return SNonNeg, true
	case neg(a) && neg(b):
		if strictA && strictB {
			return SPos, true
		}
		return SNonNeg, true
	case (pos(a) && neg(b)) || (neg(a) && pos(b)):
		if strictA && strictB {
			return SNeg, true
		}
		return SNonPos, true
	}
	return SUnknown, true
}

// divSign combines numerator/denominator signs; ok=false where the
// denominator is exactly zero (the expression is undefined there).
func divSign(n, d Sign) (Sign, bool) {
	if d == SZero {
		return SZero, false
	}
	if n == SZero {
		if d == SPos || d == SNeg {
			return SZero, true
		}
		return SUnknown, true
	}
	if d != SPos && d != SNeg {
		return SUnknown, true
	}
	return mulSign(n, d)
}

// powSign maps the sign of a base through x^(p/q); ok=false where the
// real value is undefined (even-denominator root of a negative, or zero
// to a negative power).
func powSign(s Sign, p, q int64) (Sign, bool) {
	if p == 0 {
		return SPos, true // x^0 == 1 by convention
	}
	evenPow := func(s Sign) Sign {
		switch s {
		case SNeg, SPos:
			return SPos
		case SZero:
			return SZero
		case SNonNeg, SNonPos:
			return SNonNeg
		}
		return SUnknown
	}
	oddPow := func(s Sign) Sign { return s }
	if q == 1 {
		if p < 0 && s == SZero {
			return SZero, false // 0 to a negative power is undefined
		}
		if p < 0 && (s == SNonNeg || s == SNonPos) {
			return SUnknown, true // may hit zero somewhere inside
		}
		pp := p
		if pp < 0 {
			pp = -pp
		}
		if pp%2 == 0 {
			return evenPow(s), true
		}
		return oddPow(s), true
	}
	if q%2 == 0 {
		// Even denominator: negative base has no real value.
		switch s {
		case SNeg:
			return SZero, false
		case SNonPos, SUnknown:
			return SUnknown, true
		}
		// s is SZero, SPos or SNonNeg here.
		if p < 0 {
			if s == SZero {
				return SZero, false
			}
			if s == SNonNeg {
				return SUnknown, true
			}
			return SPos, true
		}
		if s == SZero {
			return SZero, true
		}
		if s == SNonNeg {
			return SNonNeg, true
		}
		return SPos, true
	}
	// Odd denominator: real branch exists for negative bases too.
	if p < 0 {
		if s == SZero {
			return SZero, false
		}
		if s == SNonNeg || s == SNonPos {
			return SUnknown, true
		}
	}
	pp := p
	if pp < 0 {
		pp = -pp
	}
	if pp%2 == 0 {
		return evenPow(s), true
	}
	return oddPow(s), true
}

// combineRegions merges two partitions of the same domain, combining the
// sign of overlapping pieces with f. f may report ok=false, producing a
// gap (an undefined point/interval) in the result.
func combineRegions(a, b []Region, f func(Sign, Sign) (Sign, bool)) []Region {
	var out []Region
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if iv, ok := intersect(a[i].Interval, b[j].Interval); ok {
			if s, ok2 := f(a[i].Sign, b[j].Sign); ok2 {
				out = append(out, Region{iv, s})
			}
		}
		switch c := cmpHi(a[i].Hi, b[j].Hi); {
		case c < 0:
			i++
		case c > 0:
			j++
		default:
			i++
			j++
		}
	}
	return mergeRegions(out)
}

// mergeRegions merges adjacent regions that share a sign and whose common
// endpoint is included in at least one of them (no gap at the seam).
func mergeRegions(rs []Region) []Region {
	out := rs[:0]
	for _, r := range rs {
		if n := len(out); n > 0 {
			last := &out[n-1]
			if last.Sign == r.Sign &&
				last.Hi.Val.Cmp(r.Lo.Val) == 0 &&
				(!last.Hi.Open || !r.Lo.Open) {
				last.Hi = r.Hi
				continue
			}
		}
		out = append(out, r)
	}
	return out
}
