// Package freqsketch implements a fixed-capacity, mergeable approximate
// frequency summary (weighted Misra-Gries with an explicit residual error
// term), together with an exact counter used to independently verify every
// reported bound.
//
// Invariant maintained by Summary:
//
//	tracked item i:   c_i <= f_i <= c_i + residual
//	untracked item i: 0   <= f_i <= residual
//
// where f_i is the exact frequency, c_i the stored counter and residual the
// total count mass discarded by decrements and merges. residual is never
// decreased by any operation, so local error is never lost when merging.
package freqsketch

import "sort"

// Summary is a fixed-capacity approximate frequency sketch.
type Summary struct {
	capacity int
	total    uint64 // exact number of updates processed
	residual uint64 // accumulated error shared by all candidates
	counts   map[string]uint64
}

// New returns an empty Summary that keeps at most capacity counters.
func New(capacity int) *Summary {
	if capacity < 1 {
		panic("freqsketch: capacity must be a positive integer")
	}
	return &Summary{capacity: capacity, counts: make(map[string]uint64)}
}

// Capacity returns the maximum number of counters kept.
func (s *Summary) Capacity() int { return s.capacity }

// Total returns the exact total weight of all updates processed.
func (s *Summary) Total() uint64 { return s.total }

// Residual returns the global error bound: every estimate is off by at most
// this much, and every untracked item has frequency at most this much.
func (s *Summary) Residual() uint64 { return s.residual }

// Size returns the number of counters currently kept (<= Capacity).
func (s *Summary) Size() int { return len(s.counts) }

// Update records w occurrences of item. w must be a positive integer.
func (s *Summary) Update(item string, w uint64) {
	if w == 0 {
		panic("freqsketch: update weight must be a positive integer")
	}
	s.total += w
	if c, ok := s.counts[item]; ok {
		s.counts[item] = c + w
		return
	}
	if len(s.counts) < s.capacity {
		s.counts[item] = w
		return
	}
	// Weighted Misra-Gries: decrement every counter by dec = min(w, minCount).
	// The discarded mass dec is exactly accounted for in residual.
	dec := minCount(s.counts)
	if w < dec {
		dec = w
	}
	s.decrementAll(dec)
	s.residual += dec
	if w > dec {
		s.counts[item] = w - dec
	}
	// If w <= dec the item's whole weight is absorbed into residual, and its
	// true frequency w <= residual still satisfies the untracked bound.
}

// Merge returns a new Summary summarising everything processed by s and
// other. Neither input is modified. Both summaries must share the same
// capacity. The merged summary keeps the combined processed total and the
// combined residual, so no local error is dropped.
func (s *Summary) Merge(other *Summary) *Summary {
	if s.capacity != other.capacity {
		panic("freqsketch: cannot merge summaries with different capacities")
	}
	out := &Summary{
		capacity: s.capacity,
		total:    s.total + other.total,
		residual: s.residual + other.residual,
		counts:   make(map[string]uint64, len(s.counts)+len(other.counts)),
	}
	for item, c := range s.counts {
		out.counts[item] = c
	}
	for item, c := range other.counts {
		out.counts[item] += c
	}
	out.prune()
	return out
}

// prune enforces the capacity by subtracting the (capacity+1)-th largest
// counter from all counters and folding it into residual.
func (s *Summary) prune() {
	for len(s.counts) > s.capacity {
		vals := make([]uint64, 0, len(s.counts))
		for _, c := range s.counts {
			vals = append(vals, c)
		}
		sort.Slice(vals, func(i, j int) bool { return vals[i] > vals[j] })
		c := vals[s.capacity] // (capacity+1)-th largest
		s.decrementAll(c)
		s.residual += c
	}
}

// decrementAll subtracts dec (<= min counter) from every counter and drops
// counters that reach zero.
func (s *Summary) decrementAll(dec uint64) {
	for item, c := range s.counts {
		if c <= dec {
			delete(s.counts, item)
		} else {
			s.counts[item] = c - dec
		}
	}
}

func minCount(counts map[string]uint64) uint64 {
	first := true
	var m uint64
	for _, c := range counts {
		if first || c < m {
			m = c
			first = false
		}
	}
	return m
}

// Estimate returns the approximate frequency of item and its error bound:
// the exact frequency f satisfies estimate <= f <= estimate+err for tracked
// items, and 0 <= f <= err (estimate == 0) for untracked ones.
func (s *Summary) Estimate(item string) (estimate, err uint64) {
	return s.counts[item], s.residual
}

// Bounds returns the guaranteed inclusive range [lo, hi] for the exact
// frequency of item.
func (s *Summary) Bounds(item string) (lo, hi uint64) {
	c := s.counts[item]
	return c, c + s.residual
}

// Items returns the currently tracked items in deterministic (sorted) order.
func (s *Summary) Items() []string {
	items := make([]string, 0, len(s.counts))
	for item := range s.counts {
		items = append(items, item)
	}
	sort.Strings(items)
	return items
}

// Tracked reports whether item currently has a counter.
func (s *Summary) Tracked(item string) bool {
	_, ok := s.counts[item]
	return ok
}
