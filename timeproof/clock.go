package timeproof

import "fmt"

// validateClock checks the piecewise drift model: contiguous segments,
// strictly increasing monotonic boundaries, positive and ordered rates.
func validateClock(c *Clock) error {
	if c == nil {
		return fmt.Errorf("timeproof: nil clock")
	}
	if len(c.Segments) == 0 {
		return fmt.Errorf("timeproof: clock has no drift segments")
	}
	for i, s := range c.Segments {
		if s.From == nil || s.To == nil || s.RateLo == nil || s.RateHi == nil {
			return fmt.Errorf("timeproof: segment %d has a nil field", i)
		}
		if s.From.Cmp(s.To) >= 0 {
			return fmt.Errorf("timeproof: segment %d is empty or reversed", i)
		}
		if s.RateLo.Sign() <= 0 || s.RateHi.Sign() <= 0 {
			return fmt.Errorf("timeproof: segment %d drift rate must be positive", i)
		}
		if s.RateLo.Cmp(s.RateHi) > 0 {
			return fmt.Errorf("timeproof: segment %d rate lower bound exceeds upper bound", i)
		}
		if i > 0 && c.Segments[i-1].To.Cmp(s.From) != 0 {
			return fmt.Errorf("timeproof: segments %d and %d are not contiguous", i-1, i)
		}
	}
	return nil
}

// coverage returns the covered monotonic range [lo, hi].
func (c *Clock) coverage() (*Rat, *Rat) {
	return ratCopy(c.Segments[0].From), ratCopy(c.Segments[len(c.Segments)-1].To)
}

// withinCoverage reports whether m is inside [lo, hi].
func withinCoverage(m, lo, hi *Rat) bool {
	return m.Cmp(lo) >= 0 && m.Cmp(hi) <= 0
}

// elapsed returns the exact rational interval [lo, hi] of possible true-time
// durations separating two local monotonic readings a and b. It integrates
// the piecewise drift bounds and handles a > b by sign reversal. Both points
// must lie inside the clock coverage.
func (c *Clock) elapsed(a, b *Rat) (*Rat, *Rat) {
	if a.Cmp(b) == 0 {
		return RatInt(0), RatInt(0)
	}
	if a.Cmp(b) > 0 {
		lo, hi := c.elapsed(b, a)
		// elapsed(a,b) = -elapsed(b,a)
		return negRat(hi), negRat(lo)
	}
	lo := new(Rat) // 0
	hi := new(Rat)
	cursor := new(Rat).Set(a)
	for _, s := range c.Segments {
		if cursor.Cmp(b) >= 0 {
			break
		}
		if s.To.Cmp(cursor) <= 0 || s.From.Cmp(b) >= 0 {
			continue
		}
		start := maxRat(cursor, s.From)
		end := minRat(b, s.To)
		if end.Cmp(start) <= 0 {
			continue
		}
		dm := new(Rat).Sub(end, start) // dm > 0
		lo.Add(lo, new(Rat).Mul(s.RateLo, dm))
		hi.Add(hi, new(Rat).Mul(s.RateHi, dm))
		cursor.Set(end)
	}
	return lo, hi
}

func negRat(r *Rat) *Rat { return new(Rat).Neg(r) }

func minRat(a, b *Rat) *Rat {
	if a.Cmp(b) <= 0 {
		return new(Rat).Set(a)
	}
	return new(Rat).Set(b)
}

func maxRat(a, b *Rat) *Rat {
	if a.Cmp(b) >= 0 {
		return new(Rat).Set(a)
	}
	return new(Rat).Set(b)
}
