// Package interval implements sets of intervals over the rational numbers
// (math/big.Rat) with open/closed endpoints, including infinite endpoints.
//
// A Set is always kept normalized: its intervals are sorted, pairwise
// disjoint, and two adjacent intervals are merged only when their union
// has no gap (i.e. they share an endpoint value that at least one of them
// includes). Empty intervals are dropped; a closed single-point interval
// [x, x] is valid.
package interval

import (
	"fmt"
	"math/big"
	"strings"
)

// Kind classifies an endpoint position.
type Kind int

const (
	NegInf Kind = -1 // negative infinity
	Finite Kind = 0  // finite rational value
	PosInf Kind = 1  // positive infinity
)

// Endpoint is one bound of an interval. Infinite endpoints are always
// treated as open; Open is meaningful only for finite endpoints.
type Endpoint struct {
	Kind  Kind
	Value *big.Rat // non-nil iff Kind == Finite
	Open  bool
}

// ClosedEnd returns a closed finite endpoint at v.
func ClosedEnd(v *big.Rat) Endpoint { return Endpoint{Finite, new(big.Rat).Set(v), false} }

// OpenEnd returns an open finite endpoint at v.
func OpenEnd(v *big.Rat) Endpoint { return Endpoint{Finite, new(big.Rat).Set(v), true} }

// NegInfEnd returns the -inf endpoint (always open).
func NegInfEnd() Endpoint { return Endpoint{Kind: NegInf, Open: true} }

// PosInfEnd returns the +inf endpoint (always open).
func PosInfEnd() Endpoint { return Endpoint{Kind: PosInf, Open: true} }

// comparePos orders endpoints by position on the extended real line,
// ignoring openness.
func comparePos(a, b Endpoint) int {
	if a.Kind != b.Kind {
		if a.Kind < b.Kind {
			return -1
		}
		return 1
	}
	if a.Kind == Finite {
		return a.Value.Cmp(b.Value)
	}
	return 0
}

// flipOpen toggles the openness of a finite endpoint (used by Subtract).
func flipOpen(e Endpoint) Endpoint {
	if e.Kind == Finite {
		e.Open = !e.Open
	}
	return e
}

func (e Endpoint) String() string {
	switch e.Kind {
	case NegInf:
		return "-inf"
	case PosInf:
		return "+inf"
	}
	s := e.Value.RatString()
	if e.Open {
		return s + "(open)"
	}
	return s
}

// Interval is a single (possibly unbounded) interval [Lo, Hi].
type Interval struct {
	Lo, Hi Endpoint
}

// New builds the interval (lo, hi). The result may be empty; empty
// intervals are dropped when added to a Set.
func New(lo, hi Endpoint) Interval { return Interval{lo, hi} }

// Point returns the closed single-point interval [x, x].
func Point(x *big.Rat) Interval { return Interval{ClosedEnd(x), ClosedEnd(x)} }

// All returns the whole real line (-inf, +inf).
func All() Interval { return Interval{NegInfEnd(), PosInfEnd()} }

// IsEmpty reports whether the interval contains no point.
func (iv Interval) IsEmpty() bool {
	c := comparePos(iv.Lo, iv.Hi)
	if c > 0 {
		return true
	}
	if c < 0 {
		return false
	}
	// Lo and Hi at the same position: only a finite, doubly closed
	// interval (a single point) is non-empty.
	if iv.Lo.Kind != Finite {
		return true
	}
	return iv.Lo.Open || iv.Hi.Open
}

// Contains reports whether the finite rational x lies in the interval.
func (iv Interval) Contains(x *big.Rat) bool {
	if iv.IsEmpty() {
		return false
	}
	return lowerOK(iv.Lo, x) && upperOK(iv.Hi, x)
}

func lowerOK(lo Endpoint, x *big.Rat) bool {
	switch lo.Kind {
	case NegInf:
		return true
	case PosInf:
		return false
	}
	c := x.Cmp(lo.Value)
	if lo.Open {
		return c > 0
	}
	return c >= 0
}

func upperOK(hi Endpoint, x *big.Rat) bool {
	switch hi.Kind {
	case PosInf:
		return true
	case NegInf:
		return false
	}
	c := x.Cmp(hi.Value)
	if hi.Open {
		return c < 0
	}
	return c <= 0
}

func (iv Interval) String() string {
	if iv.IsEmpty() {
		return "(empty)"
	}
	l, r := "[", "]"
	if iv.Lo.Open {
		l = "("
	}
	if iv.Hi.Open {
		r = ")"
	}
	ls, hs := iv.Lo.String(), iv.Hi.String()
	ls = strings.TrimSuffix(ls, "(open)")
	hs = strings.TrimSuffix(hs, "(open)")
	return fmt.Sprintf("%s%s, %s%s", l, ls, hs, r)
}
