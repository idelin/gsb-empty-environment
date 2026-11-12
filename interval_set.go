package netsec

// interval is an inclusive packet-number range [first, last].
// Zero-length intervals (first > last) are never stored.
type interval struct {
	first uint64
	last  uint64
}

// intervalSet is a normalized collection of disjoint, ascending uint64
// intervals. It is the interval-reference model used both by the production
// ledger (to track which packet numbers were actually sent / acknowledged)
// and by the tests as an independent oracle.
type intervalSet struct {
	ranges []interval
}

// contains reports whether pn is covered by the set.
func (s *intervalSet) contains(pn uint64) bool {
	idx := searchFirstGE(s.ranges, pn)
	if idx < len(s.ranges) && s.ranges[idx].first <= pn && pn <= s.ranges[idx].last {
		return true
	}
	if idx > 0 {
		prev := s.ranges[idx-1]
		return prev.first <= pn && pn <= prev.last
	}
	return false
}

// covers reports whether every point in [first, last] is covered.
func (s *intervalSet) covers(first, last uint64) bool {
	if first > last {
		return false
	}
	idx := searchFirstGE(s.ranges, first)
	if idx > 0 && s.ranges[idx-1].last >= first {
		idx--
	}
	if idx >= len(s.ranges) {
		return false
	}
	return s.ranges[idx].first <= first && last <= s.ranges[idx].last
}

// empty reports whether the set holds no points.
func (s *intervalSet) empty() bool {
	return len(s.ranges) == 0
}

// max returns the largest covered point and true, or false when empty.
func (s *intervalSet) max() (uint64, bool) {
	if len(s.ranges) == 0 {
		return 0, false
	}
	return s.ranges[len(s.ranges)-1].last, true
}

// count returns the number of distinct covered points. It saturates at
// math.MaxUint64 for the full 64-bit space.
func (s *intervalSet) count() uint64 {
	var total uint64
	for _, r := range s.ranges {
		total = saturatingAdd(total, r.last-r.first+1)
	}
	return total
}

// add inserts every point of [first, last], merging adjacent and overlapping
// intervals to keep the representation normalized.
func (s *intervalSet) add(first, last uint64) {
	if first > last {
		return
	}
	// Find the first interval that starts at or after first, then walk
	// backwards to absorb any interval immediately adjacent on the left.
	start := searchFirstGE(s.ranges, first)
	for start > 0 && first > 0 && s.ranges[start-1].last >= first-1 {
		start--
	}

	// Absorb every interval that overlaps [first, last] or is adjacent to
	// its right edge, widening the bounds as we go.
	end := start
	for end < len(s.ranges) {
		r := s.ranges[end]
		if r.first > last && (last == ^uint64(0) || r.first > last+1) {
			break
		}
		if r.first < first {
			first = r.first
		}
		if r.last > last {
			last = r.last
		}
		end++
	}

	merged := interval{first: first, last: last}
	s.ranges = append(s.ranges[:start], append([]interval{merged}, s.ranges[end:]...)...)
}

// remove deletes every point of [first, last], splitting intervals as needed.
func (s *intervalSet) remove(first, last uint64) {
	if first > last {
		return
	}
	out := s.ranges[:0]
	for _, r := range s.ranges {
		switch {
		case r.last < first || r.first > last:
			out = append(out, r)
		default:
			if r.first < first {
				out = append(out, interval{first: r.first, last: first - 1})
			}
			if r.last > last {
				out = append(out, interval{first: last + 1, last: r.last})
			}
		}
	}
	s.ranges = out
}

// searchFirstGE returns the index of the first interval whose first endpoint
// is >= pn, or len(ranges) when none qualifies.
func searchFirstGE(ranges []interval, pn uint64) int {
	lo, hi := 0, len(ranges)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if ranges[mid].first < pn {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func saturatingAdd(a, b uint64) uint64 {
	sum := a + b
	if sum < a {
		return ^uint64(0)
	}
	return sum
}
