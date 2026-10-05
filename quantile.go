package slideq

import "sort"

// Quantile returns the q-quantile of values, defined as the element at
// index floor(q*(n-1)) of the values sorted in ascending order, where
// n = len(values). q must satisfy 0 <= q <= 1. It panics on empty input
// or an out-of-range q. The input slice is not modified.
func Quantile(values []float64, q Fraction) float64 {
	n := len(values)
	if n == 0 {
		panic("slideq: quantile of empty set")
	}
	if q.Num < 0 || q.Num > q.Den {
		panic("slideq: q out of [0,1]")
	}
	sorted := make([]float64, n)
	copy(sorted, values)
	sort.Float64s(sorted)
	return sorted[q.MulFloor(int64(n-1))]
}
