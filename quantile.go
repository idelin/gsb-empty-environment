package sliding

import "sort"

// Quantile returns the element at index floor(q*(n-1)) of the sorted values.
// It reports ok=false for an empty input. The input slice is not mutated.
func Quantile(values []float64, q Fraction) (v float64, ok bool) {
	n := len(values)
	if n == 0 {
		return 0, false
	}
	sorted := make([]float64, n)
	copy(sorted, values)
	sort.Float64s(sorted)
	idx := q.Num * int64(n-1) / q.Den
	return sorted[idx], true
}
