package domain

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Interval is a rational interval with optional open endpoints.
// A point interval has Lo == Hi and both endpoints closed.
type Interval struct {
	Lo, Hi         *big.Rat
	LoOpen, HiOpen bool
}

// Point builds the degenerate closed interval [c, c].
func Point(c *big.Rat) Interval {
	return Interval{Lo: new(big.Rat).Set(c), Hi: new(big.Rat).Set(c)}
}

func (iv Interval) empty() bool {
	c := iv.Lo.Cmp(iv.Hi)
	return c > 0 || (c == 0 && (iv.LoOpen || iv.HiOpen))
}

func (iv Interval) contains(x *big.Rat) bool {
	cl, ch := iv.Lo.Cmp(x), iv.Hi.Cmp(x)
	if cl > 0 || ch < 0 {
		return false
	}
	if cl == 0 && iv.LoOpen {
		return false
	}
	if ch == 0 && iv.HiOpen {
		return false
	}
	return true
}

func (iv Interval) String() string {
	l, r := "[", "]"
	if iv.LoOpen {
		l = "("
	}
	if iv.HiOpen {
		r = ")"
	}
	return fmt.Sprintf("%s%s, %s%s", l, iv.Lo.RatString(), iv.Hi.RatString(), r)
}

// intersect returns the intersection of two intervals.
func intersect(a, b Interval) (Interval, bool) {
	lo, loOpen := a.Lo, a.LoOpen
	switch b.Lo.Cmp(a.Lo) {
	case 1:
		lo, loOpen = b.Lo, b.LoOpen
	case 0:
		loOpen = a.LoOpen || b.LoOpen
	}
	hi, hiOpen := a.Hi, a.HiOpen
	switch b.Hi.Cmp(a.Hi) {
	case -1:
		hi, hiOpen = b.Hi, b.HiOpen
	case 0:
		hiOpen = a.HiOpen || b.HiOpen
	}
	iv := Interval{Lo: lo, Hi: hi, LoOpen: loOpen, HiOpen: hiOpen}
	if iv.empty() {
		return Interval{}, false
	}
	return iv, true
}

// minusPoint removes a single point from the interval.
func (iv Interval) minusPoint(p *big.Rat) []Interval {
	if !iv.contains(p) {
		return []Interval{iv}
	}
	var out []Interval
	if iv.Lo.Cmp(p) < 0 {
		out = append(out, Interval{Lo: iv.Lo, Hi: p, LoOpen: iv.LoOpen, HiOpen: true})
	}
	if p.Cmp(iv.Hi) < 0 {
		out = append(out, Interval{Lo: p, Hi: iv.Hi, LoOpen: true, HiOpen: iv.HiOpen})
	}
	return out
}

// normalize sorts intervals and merges overlapping or touching ones.
// Touching intervals merge only when the shared endpoint is included
// by at least one side, so (0,1) and (1,2) stay apart but [0,1] and
// (1,2] merge into [0,2].
func normalize(ivs []Interval) []Interval {
	sorted := make([]Interval, 0, len(ivs))
	for _, iv := range ivs {
		if !iv.empty() {
			sorted = append(sorted, iv)
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if c := a.Lo.Cmp(b.Lo); c != 0 {
			return c < 0
		}
		if a.LoOpen != b.LoOpen {
			return !a.LoOpen // closed endpoint sorts first
		}
		return a.Hi.Cmp(b.Hi) < 0
	})
	var out []Interval
	for _, iv := range sorted {
		if n := len(out); n > 0 {
			last := &out[n-1]
			c := iv.Lo.Cmp(last.Hi)
			if c < 0 || (c == 0 && (!iv.LoOpen || !last.HiOpen)) {
				switch h := iv.Hi.Cmp(last.Hi); {
				case h > 0:
					last.Hi, last.HiOpen = iv.Hi, iv.HiOpen
				case h == 0:
					last.HiOpen = last.HiOpen && iv.HiOpen
				}
				continue
			}
		}
		out = append(out, iv)
	}
	return out
}

// removePoints deletes the given rational points from a set of intervals.
func removePoints(ivs []Interval, pts []*big.Rat) []Interval {
	out := ivs
	for _, p := range pts {
		var next []Interval
		for _, iv := range out {
			next = append(next, iv.minusPoint(p)...)
		}
		out = next
	}
	return normalize(out)
}

// Domain is the result of an analysis: the exactly determined piecewise
// domain plus descriptions of the parts that could not be decided.
// If Unsupported is non-empty, Parts may be an over-approximation for
// the reasons listed there.
type Domain struct {
	Parts       []Interval
	Unsupported []string
}

func (d Domain) String() string {
	if len(d.Parts) == 0 {
		return "∅"
	}
	ss := make([]string, len(d.Parts))
	for i, iv := range d.Parts {
		ss[i] = iv.String()
	}
	return strings.Join(ss, " ∪ ")
}
