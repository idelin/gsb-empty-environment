// Package frsimp simplifies polylines under the discrete Frechet distance.
//
// Simplify keeps the first and last point and returns a subsequence of the
// input vertices such that the discrete Frechet distance between the original
// polyline and the simplified one does not exceed a given epsilon, under a
// caller-supplied metric. Among all feasible subsequences it first minimizes
// the number of retained points and then the lexicographic order of the
// retained indices. Every result carries a matching (a monotone coupling)
// that witnesses the distance guarantee. A configurable evaluation budget
// bounds the search; when the budget is exhausted a feasible (not necessarily
// minimal) upper bound is returned instead.
package frsimp

import "math"

// Point is a coordinate vector. All points of a polyline must share the same
// dimension.
type Point []float64

// Metric returns the distance between two points. It is the caller's
// responsibility that it is a valid metric (or at least non-negative and
// symmetric) over the supplied points.
type Metric func(a, b Point) float64

// Euclidean is the default L2 metric.
func Euclidean(a, b Point) float64 {
	var sum float64
	for i := 0; i < len(a) && i < len(b); i++ {
		d := a[i] - b[i]
		sum += d * d
	}
	return math.Sqrt(sum)
}

// Manhattan is the L1 metric.
func Manhattan(a, b Point) float64 {
	var sum float64
	for i := 0; i < len(a) && i < len(b); i++ {
		sum += math.Abs(a[i] - b[i])
	}
	return sum
}
