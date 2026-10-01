package sim

import (
	"fmt"
	"math/big"
)

// Interval is a half-open byte range [Lo, Hi) over extended integers
// (math/big.Int), so sequence numbers are not limited to 32 or 64 bits.
type Interval struct {
	Lo *big.Int
	Hi *big.Int
}

// NewInterval copies the bounds into a new Interval.
func NewInterval(lo, hi *big.Int) Interval {
	return Interval{Lo: new(big.Int).Set(lo), Hi: new(big.Int).Set(hi)}
}

func (iv Interval) String() string {
	return fmt.Sprintf("[%s,%s)", iv.Lo, iv.Hi)
}

// IntervalSet stores sorted, disjoint, non-adjacent intervals. Bounds are
// treated as immutable: every method copies instead of mutating in place.
type IntervalSet struct {
	ivs []Interval
}

// Intervals returns a copy of the stored intervals in ascending order.
func (s *IntervalSet) Intervals() []Interval {
	out := make([]Interval, len(s.ivs))
	copy(out, s.ivs)
	return out
}

// Add inserts [lo, hi), merging any overlapping or adjacent intervals so the
// union of overlapping SACK blocks is only ever counted once.
func (s *IntervalSet) Add(lo, hi *big.Int) {
	if lo.Cmp(hi) >= 0 {
		return
	}
	nlo := new(big.Int).Set(lo)
	nhi := new(big.Int).Set(hi)
	out := s.ivs[:0]
	placed := false
	for _, iv := range s.ivs {
		if iv.Hi.Cmp(nlo) < 0 {
			out = append(out, iv)
			continue
		}
		if iv.Lo.Cmp(nhi) > 0 {
			if !placed {
				out = append(out, Interval{nlo, nhi})
				placed = true
			}
			out = append(out, iv)
			continue
		}
		if iv.Lo.Cmp(nlo) < 0 {
			nlo = new(big.Int).Set(iv.Lo)
		}
		if iv.Hi.Cmp(nhi) > 0 {
			nhi = new(big.Int).Set(iv.Hi)
		}
	}
	if !placed {
		out = append(out, Interval{nlo, nhi})
	}
	s.ivs = out
}

// Subtract removes [lo, hi) from the set, splitting intervals as needed.
func (s *IntervalSet) Subtract(lo, hi *big.Int) {
	if lo.Cmp(hi) >= 0 {
		return
	}
	out := s.ivs[:0]
	for _, iv := range s.ivs {
		if iv.Hi.Cmp(lo) <= 0 || iv.Lo.Cmp(hi) >= 0 {
			out = append(out, iv)
			continue
		}
		if iv.Lo.Cmp(lo) < 0 {
			out = append(out, Interval{new(big.Int).Set(iv.Lo), new(big.Int).Set(lo)})
		}
		if iv.Hi.Cmp(hi) > 0 {
			out = append(out, Interval{new(big.Int).Set(hi), new(big.Int).Set(iv.Hi)})
		}
	}
	s.ivs = out
}

// Gaps returns the sub-ranges of [lo, hi) not covered by the set.
func (s *IntervalSet) Gaps(lo, hi *big.Int) []Interval {
	if lo.Cmp(hi) >= 0 {
		return nil
	}
	var gaps []Interval
	cur := new(big.Int).Set(lo)
	for _, iv := range s.ivs {
		if iv.Hi.Cmp(cur) <= 0 {
			continue
		}
		if iv.Lo.Cmp(hi) >= 0 {
			break
		}
		if iv.Lo.Cmp(cur) > 0 {
			gaps = append(gaps, NewInterval(cur, iv.Lo))
		}
		if iv.Hi.Cmp(cur) > 0 {
			cur = new(big.Int).Set(iv.Hi)
		}
	}
	if cur.Cmp(hi) < 0 {
		gaps = append(gaps, NewInterval(cur, hi))
	}
	return gaps
}

// Find returns the interval containing point p, if any.
func (s *IntervalSet) Find(p *big.Int) (Interval, bool) {
	for _, iv := range s.ivs {
		if iv.Lo.Cmp(p) <= 0 && p.Cmp(iv.Hi) < 0 {
			return iv, true
		}
	}
	return Interval{}, false
}

// Measure returns the total number of bytes covered by the union.
func (s *IntervalSet) Measure() *big.Int {
	total := new(big.Int)
	for _, iv := range s.ivs {
		total.Add(total, new(big.Int).Sub(iv.Hi, iv.Lo))
	}
	return total
}
