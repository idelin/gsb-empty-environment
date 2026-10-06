// Package stats maintains a weighted multiset of integer values and answers
// weighted-quantile queries over consistent barrier cuts of multiple
// disconnectable partitions.
package stats

import "sort"

// WeightedQuantile is a multiset of int64 values with non-negative integer
// weights. It supports online additions and removals of individual samples.
//
// The quantile definition: for q in (0, 1], Quantile returns the smallest
// value whose cumulative weight is at least q*totalWeight. For q <= 0 it
// returns the smallest value that has positive weight. Values with zero
// weight never contribute and can never be returned.
type WeightedQuantile struct {
	counts map[int64]uint64
	total  uint64
}

// NewWeightedQuantile returns an empty set.
func NewWeightedQuantile() *WeightedQuantile {
	return &WeightedQuantile{counts: make(map[int64]uint64)}
}

// Add inserts weight occurrences of value. A zero weight is a no-op.
func (w *WeightedQuantile) Add(value int64, weight uint64) {
	if weight == 0 {
		return
	}
	w.counts[value] += weight
	w.total += weight
}

// Remove deletes weight occurrences of value. It reports an *UnderflowError
// and leaves the set unchanged when fewer than weight occurrences exist.
func (w *WeightedQuantile) Remove(value int64, weight uint64) error {
	if weight == 0 {
		return nil
	}
	if w.counts[value] < weight {
		return &UnderflowError{Value: value, Have: w.counts[value], Want: weight}
	}
	w.counts[value] -= weight
	w.total -= weight
	if w.counts[value] == 0 {
		delete(w.counts, value)
	}
	return nil
}

// Total returns the sum of all weights.
func (w *WeightedQuantile) Total() uint64 { return w.total }

// Len returns the number of distinct values with positive weight.
func (w *WeightedQuantile) Len() int { return len(w.counts) }

// Min returns the smallest value with positive weight.
func (w *WeightedQuantile) Min() (int64, bool) {
	if w.total == 0 {
		return 0, false
	}
	min := int64(0)
	first := true
	for v := range w.counts {
		if first || v < min {
			min, first = v, false
		}
	}
	return min, true
}

// Quantile returns the smallest value whose cumulative weight reaches
// q*totalWeight. q <= 0 yields the smallest positive-weight value and q > 1
// is clamped to 1. ok is false when the set is empty (total weight zero),
// keeping the empty set a distinguishable case.
func (w *WeightedQuantile) Quantile(q float64) (value int64, ok bool) {
	if w.total == 0 {
		return 0, false
	}
	if q <= 0 {
		return w.Min()
	}
	if q > 1 {
		q = 1
	}
	keys := make([]int64, 0, len(w.counts))
	for v := range w.counts {
		keys = append(keys, v)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	threshold := q * float64(w.total)
	var cum uint64
	for _, v := range keys {
		cum += w.counts[v]
		if float64(cum) >= threshold {
			return v, true
		}
	}
	return keys[len(keys)-1], true
}
