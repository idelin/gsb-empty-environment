package domain

import (
	"fmt"
	"math/big"
	"strings"
)

// Endpoint is one end of an interval: a rational value plus openness.
type Endpoint struct {
	Val  *big.Rat
	Open bool
}

func closed(v *big.Rat) Endpoint { return Endpoint{Val: v, Open: false} }
func open(v *big.Rat) Endpoint   { return Endpoint{Val: v, Open: true} }

// Interval is a bounded rational interval [Lo, Hi] with open/closed ends.
type Interval struct {
	Lo, Hi Endpoint
}

// Iv builds the interval [lo, hi]; use OpenIv for open ends.
func Iv(lo, hi *big.Rat) Interval { return Interval{Lo: closed(lo), Hi: closed(hi)} }

func (iv Interval) String() string {
	lb, rb := "[", "]"
	if iv.Lo.Open {
		lb = "("
	}
	if iv.Hi.Open {
		rb = ")"
	}
	if iv.Lo.Val.Cmp(iv.Hi.Val) == 0 && !iv.Lo.Open && !iv.Hi.Open {
		return "{" + iv.Lo.Val.RatString() + "}"
	}
	return fmt.Sprintf("%s%s, %s%s", lb, iv.Lo.Val.RatString(), iv.Hi.Val.RatString(), rb)
}

// Domain is a finite union of disjoint, sorted rational intervals.
type Domain struct {
	Intervals []Interval
}

func (d Domain) String() string {
	if len(d.Intervals) == 0 {
		return "∅"
	}
	parts := make([]string, len(d.Intervals))
	for i, iv := range d.Intervals {
		parts[i] = iv.String()
	}
	return strings.Join(parts, " ∪ ")
}

// cmpLo orders left endpoints: by value; at equal value a closed end
// extends further left than an open one.
func cmpLo(a, b Endpoint) int {
	if c := a.Val.Cmp(b.Val); c != 0 {
		return c
	}
	switch {
	case a.Open == b.Open:
		return 0
	case a.Open: // open is to the right of closed
		return 1
	default:
		return -1
	}
}

// cmpHi orders right endpoints: by value; at equal value an open end
// terminates earlier than a closed one.
func cmpHi(a, b Endpoint) int {
	if c := a.Val.Cmp(b.Val); c != 0 {
		return c
	}
	switch {
	case a.Open == b.Open:
		return 0
	case a.Open:
		return -1
	default:
		return 1
	}
}

func maxLo(a, b Endpoint) Endpoint {
	if cmpLo(a, b) >= 0 {
		return a
	}
	return b
}

func minHi(a, b Endpoint) Endpoint {
	if cmpHi(a, b) <= 0 {
		return a
	}
	return b
}

// intersect returns a ∩ b, ok=false when the intersection is empty.
func intersect(a, b Interval) (Interval, bool) {
	lo := maxLo(a.Lo, b.Lo)
	hi := minHi(a.Hi, b.Hi)
	c := lo.Val.Cmp(hi.Val)
	if c > 0 {
		return Interval{}, false
	}
	if c == 0 && (lo.Open || hi.Open) {
		return Interval{}, false
	}
	return Interval{Lo: lo, Hi: hi}, true
}

// splitAtPoint splits iv at the point r into the part left of r, the
// singleton {r} (when r ∈ iv) and the part right of r.
func splitAtPoint(iv Interval, r *big.Rat) []Interval {
	if r.Cmp(iv.Lo.Val) < 0 || r.Cmp(iv.Hi.Val) > 0 {
		return []Interval{iv}
	}
	point := Interval{Lo: closed(new(big.Rat).Set(r)), Hi: closed(new(big.Rat).Set(r))}
	atLo := r.Cmp(iv.Lo.Val) == 0
	atHi := r.Cmp(iv.Hi.Val) == 0
	switch {
	case atLo && atHi:
		if iv.Lo.Open || iv.Hi.Open {
			return nil
		}
		return []Interval{point}
	case atLo:
		if iv.Lo.Open {
			return []Interval{iv}
		}
		return []Interval{point, {Lo: open(new(big.Rat).Set(r)), Hi: iv.Hi}}
	case atHi:
		if iv.Hi.Open {
			return []Interval{iv}
		}
		return []Interval{{Lo: iv.Lo, Hi: open(new(big.Rat).Set(r))}, point}
	default:
		return []Interval{
			{Lo: iv.Lo, Hi: open(new(big.Rat).Set(r))},
			point,
			{Lo: open(new(big.Rat).Set(r)), Hi: iv.Hi},
		}
	}
}
