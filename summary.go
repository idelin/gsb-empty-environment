// Package freqsketch implements a fixed-capacity, mergeable summary for
// approximate frequency estimation over streams of positive integer count
// updates (a Misra-Gries style sketch with explicit per-item error bounds).
//
// Guarantees:
//
//   - For a monitored item, the true frequency f satisfies
//     Count <= f <= Count+Err, where Err is the summary's MaxError().
//   - For any item not present in the summary, f <= MaxError().
//   - MaxError() <= Total()/Capacity() at all times.
//   - Merging preserves the total number of processed updates and never
//     drops local error: each side's accumulated compression error is
//     carried into the merged summary's error bound.
package freqsketch

// Estimate reports the frequency bounds for one monitored item: the true
// frequency lies in [Count, Count+Err].
type Estimate[T comparable] struct {
	Item  T
	Count uint64 // lower bound on the true frequency
	Err   uint64 // upper bound on how much Count may underestimate
}

// Summary is a fixed-capacity mergeable frequency sketch.
//
// Each monitored item keeps an exact count of the occurrences attributed to
// it, which never exceeds its true frequency. Occurrences lost to
// compression (both local and merged in) are accounted for in baseErr, so
// the true frequency of any item - monitored or not - exceeds its reported
// count by at most MaxError(). A fixed-capacity summary cannot attribute
// evicted mass to individual items, so the error bound is global.
type Summary[T comparable] struct {
	capacity int
	total    uint64
	baseErr  uint64 // total compressed mass; bounds the frequency of unmonitored items
	entries  map[T]uint64
}

// New returns an empty summary that monitors at most capacity items.
func New[T comparable](capacity int) *Summary[T] {
	if capacity < 1 {
		panic("freqsketch: capacity must be a positive integer")
	}
	return &Summary[T]{capacity: capacity, entries: make(map[T]uint64)}
}

// Capacity returns the maximum number of monitored items.
func (s *Summary[T]) Capacity() int { return s.capacity }

// Len returns the number of currently monitored items.
func (s *Summary[T]) Len() int { return len(s.entries) }

// Total returns the total count of all updates processed (including merged
// ones). It is preserved exactly across merges.
func (s *Summary[T]) Total() uint64 { return s.total }

// MaxError bounds the estimation error of any reported item and the true
// frequency of any unmonitored item. It never exceeds Total()/Capacity().
func (s *Summary[T]) MaxError() uint64 { return s.baseErr }

// Add records n occurrences of item. n must be a positive integer.
func (s *Summary[T]) Add(item T, n uint64) {
	if n == 0 {
		panic("freqsketch: count update must be a positive integer")
	}
	s.total += n
	if _, ok := s.entries[item]; ok {
		s.entries[item] += n
		return
	}
	if len(s.entries) < s.capacity {
		s.entries[item] = n
		return
	}
	m := s.minCount()
	s.compress(m)
	s.entries[item] = n
}

// Merge folds other into s. The processed totals are summed, and local error
// from both sides is preserved: the accumulated compression errors are
// summed into the merged base error, so occurrences either side lost track
// of remain accounted for. s is compressed back to its capacity afterwards.
// Both summaries must have equal capacity.
func (s *Summary[T]) Merge(other *Summary[T]) {
	if s.capacity != other.capacity {
		panic("freqsketch: cannot merge summaries with different capacities")
	}
	s.total += other.total
	s.baseErr += other.baseErr
	for item, count := range other.entries {
		s.entries[item] += count
	}
	for len(s.entries) > s.capacity {
		s.compress(s.minCount())
	}
}

// Estimates returns the frequency bounds of all monitored items, in no
// particular order.
func (s *Summary[T]) Estimates() []Estimate[T] {
	out := make([]Estimate[T], 0, len(s.entries))
	for item, count := range s.entries {
		out = append(out, Estimate[T]{Item: item, Count: count, Err: s.baseErr})
	}
	return out
}

// Lookup returns the frequency bounds for item. If item is not monitored,
// ok is false and its true frequency is at most MaxError().
func (s *Summary[T]) Lookup(item T) (count, err uint64, ok bool) {
	count, ok = s.entries[item]
	if !ok {
		return 0, 0, false
	}
	return count, s.baseErr, true
}

func (s *Summary[T]) minCount() uint64 {
	first := true
	var m uint64
	for _, count := range s.entries {
		if first || count < m {
			m = count
			first = false
		}
	}
	return m
}

// compress subtracts m from every count, deletes emptied entries, and adds
// m to the base error so the dropped mass stays accounted for.
func (s *Summary[T]) compress(m uint64) {
	if m == 0 {
		return
	}
	s.baseErr += m
	for item, count := range s.entries {
		if count <= m {
			delete(s.entries, item)
		} else {
			s.entries[item] = count - m
		}
	}
}
