package freqsketch

import (
	"fmt"
	"sort"
)

// Exact is an independent, complete (non-approximate) frequency counter used
// to verify every bound reported by a Summary.
type Exact struct {
	total  uint64
	counts map[string]uint64
}

// NewExact returns an empty exact counter.
func NewExact() *Exact {
	return &Exact{counts: make(map[string]uint64)}
}

// Update records w occurrences of item. w must be a positive integer.
func (e *Exact) Update(item string, w uint64) {
	if w == 0 {
		panic("freqsketch: update weight must be a positive integer")
	}
	e.total += w
	e.counts[item] += w
}

// Count returns the exact frequency of item (0 if never seen).
func (e *Exact) Count(item string) uint64 { return e.counts[item] }

// Total returns the exact total weight processed.
func (e *Exact) Total() uint64 { return e.total }

// Items returns all items ever seen, in sorted order.
func (e *Exact) Items() []string {
	items := make([]string, 0, len(e.counts))
	for item := range e.counts {
		items = append(items, item)
	}
	sort.Strings(items)
	return items
}

// Verify checks every guarantee the Summary advertises against the
// independent exact counts:
//
//  1. the summary's processed total equals the exact total;
//  2. capacity is never exceeded;
//  3. no phantom items: every tracked item really appeared (an unseen item
//     is never reported with an exact-looking positive frequency);
//  4. for every tracked item: c <= f <= c+residual;
//  5. for every untracked item: f <= residual.
//
// It returns nil if all guarantees hold, otherwise the first violation.
func Verify(s *Summary, e *Exact) error {
	if s.Total() != e.Total() {
		return fmt.Errorf("total mismatch: summary=%d exact=%d", s.Total(), e.Total())
	}
	if s.Size() > s.Capacity() {
		return fmt.Errorf("capacity exceeded: %d > %d", s.Size(), s.Capacity())
	}
	res := s.Residual()
	for _, item := range s.Items() {
		est, err := s.Estimate(item)
		f := e.Count(item)
		if f == 0 {
			return fmt.Errorf("phantom item %q: reported estimate %d but never appeared", item, est)
		}
		if est > f {
			return fmt.Errorf("item %q: estimate %d exceeds exact frequency %d", item, est, f)
		}
		if f-est > err {
			return fmt.Errorf("item %q: error %d exceeds bound %d (estimate %d, exact %d)", item, f-est, err, est, f)
		}
	}
	for _, item := range e.Items() {
		if s.Tracked(item) {
			continue
		}
		if f := e.Count(item); f > res {
			return fmt.Errorf("untracked item %q: exact frequency %d exceeds residual bound %d", item, f, res)
		}
	}
	return nil
}
