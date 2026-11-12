package taskpkg

import (
	"math"
	"sort"
)

type span struct {
	first PacketNumber
	last  PacketNumber
}

// sentSet stores the packet numbers that have been sent in one generation as
// sorted, disjoint, non-adjacent closed spans.
type sentSet struct {
	spans []span
}

func (s *sentSet) add(pn PacketNumber) {
	n := len(s.spans)
	if n == 0 {
		s.spans = append(s.spans, span{first: pn, last: pn})
		return
	}

	last := &s.spans[n-1]
	if pn <= last.last {
		return
	}
	if pn == last.last+1 {
		last.last = pn
		return
	}
	s.spans = append(s.spans, span{first: pn, last: pn})
}

func (s *sentSet) contains(pn PacketNumber) bool {
	i := sort.Search(len(s.spans), func(i int) bool {
		return s.spans[i].last >= pn
	})
	return i < len(s.spans) && s.spans[i].first <= pn
}

// firstMissing returns the first packet number in [first, last] that is not in
// the set. It is O(log n + number of gaps) and does not iterate the interval.
func (s *sentSet) firstMissing(first, last PacketNumber) (PacketNumber, bool) {
	if first > last {
		return first, true
	}

	i := sort.Search(len(s.spans), func(i int) bool {
		return s.spans[i].last >= first
	})

	cursor := first
	for ; i < len(s.spans); i++ {
		current := s.spans[i]
		if current.first > last {
			return cursor, true
		}
		if current.first > cursor {
			return cursor, true
		}
		if current.last >= last {
			return 0, false
		}
		if current.last == PacketNumber(math.MaxUint64) {
			return 0, false
		}
		cursor = current.last + 1
		if cursor > last {
			return 0, false
		}
	}
	return cursor, true
}

func (s *sentSet) intervals() []Interval {
	out := make([]Interval, 0, len(s.spans))
	for _, current := range s.spans {
		out = append(out, Interval{First: current.first, Last: current.last})
	}
	return out
}

func mergeIntervals(in []Interval) []Interval {
	if len(in) == 0 {
		return nil
	}

	sorted := append([]Interval(nil), in...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].First != sorted[j].First {
			return sorted[i].First < sorted[j].First
		}
		return sorted[i].Last < sorted[j].Last
	})

	out := make([]Interval, 0, len(sorted))
	for _, current := range sorted {
		if len(out) == 0 {
			out = append(out, current)
			continue
		}
		last := &out[len(out)-1]
		if last.Last == PacketNumber(math.MaxUint64) || current.First <= last.Last+1 {
			if current.Last > last.Last {
				last.Last = current.Last
			}
			continue
		}
		out = append(out, current)
	}
	return out
}

func unionContains(intervals []Interval, pn PacketNumber) bool {
	i := sort.Search(len(intervals), func(i int) bool {
		return intervals[i].Last >= pn
	})
	return i < len(intervals) && intervals[i].First <= pn
}
