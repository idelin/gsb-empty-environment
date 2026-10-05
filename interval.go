// Package interval provides exact rational interval arithmetic with
// decimal enclosure rounding. Intervals keep exact *big.Rat endpoints;
// Enclose converts them to decimal enclosures with a given number of
// fraction digits, rounding the left endpoint down and the right
// endpoint up so the result always contains the exact interval.
package interval

import (
	"fmt"
	"math/big"
)

// Ext is an extended real number: a finite *big.Rat or +/-infinity.
type Ext struct {
	inf int      // -1 => -Inf, 0 => finite, +1 => +Inf
	val *big.Rat // non-nil iff inf == 0
}

// Fin returns the finite extended real v.
func Fin(v *big.Rat) Ext { return Ext{inf: 0, val: new(big.Rat).Set(v)} }

// NegInf returns -infinity.
func NegInf() Ext { return Ext{inf: -1} }

// PosInf returns +infinity.
func PosInf() Ext { return Ext{inf: 1} }

// IsInf reports whether e is infinite.
func (e Ext) IsInf() bool { return e.inf != 0 }

// Sign returns -1, 0 or +1.
func (e Ext) Sign() int {
	if e.inf != 0 {
		return e.inf
	}
	return e.val.Sign()
}

func (e Ext) String() string {
	switch e.inf {
	case -1:
		return "-inf"
	case 1:
		return "+inf"
	}
	return e.val.RatString()
}

func cmpExt(a, b Ext) int {
	if a.inf < 0 {
		if b.inf < 0 {
			return 0
		}
		return -1
	}
	if a.inf > 0 {
		if b.inf > 0 {
			return 0
		}
		return 1
	}
	if b.inf < 0 {
		return 1
	}
	if b.inf > 0 {
		return -1
	}
	return a.val.Cmp(b.val)
}

func negExt(e Ext) Ext {
	if e.inf != 0 {
		return Ext{inf: -e.inf}
	}
	return Fin(new(big.Rat).Neg(e.val))
}

// addExt adds two extended reals. The caller must not pass (+Inf)+(-Inf);
// for valid intervals this never happens.
func addExt(a, b Ext) Ext {
	if a.inf == 0 && b.inf == 0 {
		return Fin(new(big.Rat).Add(a.val, b.val))
	}
	if a.inf != 0 {
		return a
	}
	return b
}

// mulExt multiplies two extended reals. ok is false for the
// indeterminate form 0 * (+-Inf); interval multiplication resolves
// such cases through the remaining endpoint products.
func mulExt(a, b Ext) (v Ext, ok bool) {
	sa, sb := a.Sign(), b.Sign()
	if sa == 0 || sb == 0 {
		if a.inf != 0 || b.inf != 0 {
			return Ext{}, false
		}
		return Fin(new(big.Rat)), true
	}
	if a.inf != 0 || b.inf != 0 {
		return Ext{inf: sa * sb}, true
	}
	return Fin(new(big.Rat).Mul(a.val, b.val)), true
}

// Interval is a single connected interval with exact rational (or
// infinite) endpoints. The zero value is not valid; use a constructor.
// The empty set is represented canonically by Empty().
type Interval struct {
	lo, hi         Ext
	loOpen, hiOpen bool
	empty          bool
}

// Empty returns the canonical empty interval.
func Empty() Interval { return Interval{empty: true} }

// All returns the whole real line (-inf, +inf).
func All() Interval { return Make(NegInf(), PosInf(), true, true) }

// Point returns the degenerate closed interval [v, v]. A single point
// is never treated as empty.
func Point(v *big.Rat) Interval { return Make(Fin(v), Fin(v), false, false) }

// Closed returns [lo, hi].
func Closed(lo, hi *big.Rat) Interval { return Make(Fin(lo), Fin(hi), false, false) }

// Make builds an interval from endpoints and openness flags, forcing
// infinite endpoints open and canonicalizing empty results.
func Make(lo, hi Ext, loOpen, hiOpen bool) Interval {
	if lo.inf != 0 {
		loOpen = true
	}
	if hi.inf != 0 {
		hiOpen = true
	}
	c := cmpExt(lo, hi)
	if c > 0 || (c == 0 && (loOpen || hiOpen)) {
		return Empty()
	}
	return Interval{lo: lo, hi: hi, loOpen: loOpen, hiOpen: hiOpen}
}

// IsEmpty reports whether i is the empty set.
func (i Interval) IsEmpty() bool { return i.empty }

// IsPoint reports whether i is a single closed point [v, v].
func (i Interval) IsPoint() bool {
	return !i.empty && cmpExt(i.lo, i.hi) == 0
}

// Lo returns the left endpoint.
func (i Interval) Lo() Ext { return i.lo }

// Hi returns the right endpoint.
func (i Interval) Hi() Ext { return i.hi }

// LoOpen reports whether the left endpoint is excluded.
func (i Interval) LoOpen() bool { return i.loOpen }

// HiOpen reports whether the right endpoint is excluded.
func (i Interval) HiOpen() bool { return i.hiOpen }

// Contains reports whether the rational point v lies in i.
func (i Interval) Contains(v *big.Rat) bool {
	if i.empty {
		return false
	}
	e := Fin(v)
	if c := cmpExt(e, i.lo); c < 0 || (c == 0 && i.loOpen) {
		return false
	}
	if c := cmpExt(e, i.hi); c > 0 || (c == 0 && i.hiOpen) {
		return false
	}
	return true
}

func (i Interval) String() string {
	if i.empty {
		return "empty"
	}
	l, r := "[", "]"
	if i.loOpen {
		l = "("
	}
	if i.hiOpen {
		r = ")"
	}
	return fmt.Sprintf("%s%s, %s%s", l, i.lo, i.hi, r)
}

// Intersect returns x ∩ y.
func (x Interval) Intersect(y Interval) Interval {
	if x.empty || y.empty {
		return Empty()
	}
	lo, loOpen := x.lo, x.loOpen
	if c := cmpExt(y.lo, x.lo); c > 0 {
		lo, loOpen = y.lo, y.loOpen
	} else if c == 0 {
		loOpen = loOpen || y.loOpen
	}
	hi, hiOpen := x.hi, x.hiOpen
	if c := cmpExt(y.hi, x.hi); c < 0 {
		hi, hiOpen = y.hi, y.hiOpen
	} else if c == 0 {
		hiOpen = hiOpen || y.hiOpen
	}
	return Make(lo, hi, loOpen, hiOpen)
}

// Union returns x ∪ y as a Set (one segment if they overlap or touch
// at an included point, two otherwise).
func (x Interval) Union(y Interval) *Set {
	s := NewSet(0)
	s.Add(x)
	s.Add(y)
	return s
}
