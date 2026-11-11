package netsec

// interval is a closed, non-wrapping interval [lo, hi] of unsigned integer
// values. All arithmetic stays in uint64 so the same code works for the 8-bit
// reference width and the production 32-bit width.
type interval struct {
	lo uint64
	hi uint64
}

// wrapRange expands a possibly zero-wrapping unsigned range into one or two
// non-wrapping intervals. lo/hi are inclusive. A range "wraps" when it is
// expressed as [lo, max] U [0, hi] with lo > hi; a normal range has lo <= hi.
func wrapRange(lo, hi, mask uint64) []interval {
	if lo > hi {
		// Keep domain intervals sorted by value: the zero-adjacent head comes
		// first, then the high tail.
		return []interval{{0, hi}, {lo, mask}}
	}
	return []interval{{lo, hi}}
}

// mergeIntervals sorts, merges overlapping or adjacent intervals and drops
// empty ones. The result is sorted and contains disjoint intervals.
func mergeIntervals(in []interval) []interval {
	if len(in) == 0 {
		return nil
	}
	work := make([]interval, 0, len(in))
	for _, iv := range in {
		if iv.lo <= iv.hi {
			work = append(work, iv)
		}
	}
	if len(work) == 0 {
		return nil
	}
	for i := 1; i < len(work); i++ {
		for j := i; j > 0 && work[j].lo < work[j-1].lo; j-- {
			work[j], work[j-1] = work[j-1], work[j]
		}
	}
	out := work[:1]
	for _, iv := range work[1:] {
		last := &out[len(out)-1]
		if iv.lo <= last.hi || iv.lo == last.hi+1 {
			if iv.hi > last.hi {
				last.hi = iv.hi
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

// intersectDomains returns the intersection of two sorted disjoint interval
// lists, again sorted and merged.
func intersectDomains(a, b []interval) []interval {
	out := make([]interval, 0)
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		x, y := a[i], b[j]
		lo := x.lo
		if y.lo > lo {
			lo = y.lo
		}
		hi := x.hi
		if y.hi < hi {
			hi = y.hi
		}
		if lo <= hi {
			out = append(out, interval{lo, hi})
		}
		// Advance whichever interval ends first; equal ends retire both.
		if x.hi < y.hi {
			i++
		} else if y.hi < x.hi {
			j++
		} else {
			i++
			j++
		}
	}
	return mergeIntervals(out)
}

// domainCardinality returns the number of values in a domain, capped at cap+1
// so callers can treat "large" uniformly without overflowing.
func domainCardinality(dom []interval, cap uint64) uint64 {
	var total uint64
	for _, iv := range dom {
		size := iv.hi - iv.lo + 1
		if total > cap || size > cap-total {
			return cap + 1
		}
		total += size
	}
	return total
}

func fullDomain(mask uint64) []interval {
	return []interval{{0, mask}}
}

// nextMatch returns the smallest x >= start satisfying (x & mask) == pattern,
// or ok=false when no such x fits under widthMask.
//
// Successor construction. Each bit p is a candidate for the first position
// where the result becomes strictly greater than start. A raise at p is valid
// when every fixed bit above p already agrees between start and pattern, and
// p itself can be made greater:
//   - free bit zero in start: set it to 1;
//   - fixed bit zero in start whose pattern value is 1: keep the required 1.
//
// Below p the candidate is minimized subject to the pattern (fixed bits take
// pattern, free bits take zero). Every valid candidate is collected; the
// smallest is the successor.
func nextMatch(start, mask, pattern, widthMask uint64) (x uint64, ok bool) {
	if start > widthMask {
		return 0, false
	}
	if start&mask == pattern {
		return start, true
	}
	best := widthMask + 1
	top := uint64(1)
	for top<<1 <= widthMask {
		top <<= 1
	}
	for p := top; p != 0; p >>= 1 {
		higher := widthMask &^ (p | (p - 1))
		if start&mask&higher != pattern&higher {
			continue
		}
		raiseBit := uint64(0)
		if mask&p != 0 {
			if pattern&p == 0 || start&p != 0 {
				continue
			}
			raiseBit = p
		} else {
			if start&p != 0 {
				continue
			}
			raiseBit = p
		}
		candidate := (start & higher) | raiseBit | (pattern & (p - 1))
		candidate &= widthMask
		if candidate >= start && candidate&mask == pattern && candidate < best {
			best = candidate
		}
	}
	if best > widthMask {
		return 0, false
	}
	return best, true
}

// runEnd returns the largest r <= iv.hi such that every value in [v, r] keeps
// v's fixed-bit pattern. Starting at v, consecutive increments only change
// free bits until the first carry reaches a fixed bit. That carry happens at
// the lowest fixed bit whose entire free-bit block beneath it is already one;
// the matching run ends one before it.
func runEnd(v uint64, iv interval, mask, widthMask uint64) uint64 {
	bit := uint64(1)
	for bit <= widthMask {
		if mask&bit != 0 {
			// Lowest fixed bit. The free bits beneath it span (bit-1); the
			// carry from v reaches this fixed bit once all those free bits
			// are one, i.e. when v mod bit == bit-1.
			if v&(bit-1) == bit-1 {
				return v
			}
			// Round v up to the end of its current fixed-pattern block: the
			// next value whose lower free bits are all one below this fixed
			// bit would change it, so the block ends just before that.
			blockEnd := (v | (bit - 1))
			if blockEnd > iv.hi || blockEnd < v {
				return iv.hi
			}
			return blockEnd
		}
		bit <<= 1
	}
	// No fixed bits at all: the whole remaining interval is one run.
	if iv.hi > widthMask {
		return widthMask
	}
	return iv.hi
}

// filterMask restricts dom to values x satisfying (x & mask) == pattern.
// One budget unit is charged per input interval and per emitted contiguous
// run; ok=false means the budget was exhausted before filtering completed.
func filterMask(dom []interval, mc maskConstraint, widthMask uint64, budget *int64) (out []interval, ok bool) {
	out = make([]interval, 0)
	for _, iv := range dom {
		*budget--
		if *budget <= 0 {
			return nil, false
		}
		cursor := iv.lo
		for cursor <= iv.hi {
			match, found := nextMatch(cursor, mc.mask, mc.pattern, widthMask)
			if !found || match > iv.hi {
				break
			}
			end := runEnd(match, iv, mc.mask, widthMask)
			out = append(out, interval{match, end})
			*budget--
			if *budget <= 0 {
				return nil, false
			}
			if end >= iv.hi {
				break
			}
			cursor = end + 1
		}
	}
	return mergeIntervals(out), true
}
