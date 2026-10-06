// Package wmpart partitions an ordered, integer-weighted sample into at
// most k contiguous non-empty buckets. Each bucket is represented by its
// lower weighted median (for an even total weight the smaller of the two
// middle representatives is chosen), and the partition minimizes the total
// weighted absolute error. Ties are broken by preferring fewer buckets and
// then the lexicographically smallest sequence of cut positions.
package wmpart

import (
	"errors"
	"sort"
)

// Bucket is one contiguous segment of the input.
type Bucket struct {
	Start  int   // inclusive index into the input slices
	End    int   // exclusive index into the input slices
	Median int64 // lower weighted median of the bucket's values
	Cost   int64 // sum of w[i]*|x[i]-Median| over Start <= i < End
}

// Result is an optimal partition of the whole input.
type Result struct {
	Buckets   []Bucket // in order, covering the input without gaps
	TotalCost int64    // sum of the per-bucket costs
}

var (
	ErrLengthMismatch = errors.New("wmpart: values and weights have different lengths")
	ErrNonPositiveK   = errors.New("wmpart: k must be at least 1")
	ErrNegativeWeight = errors.New("wmpart: weights must be non-negative")
	ErrUnsorted       = errors.New("wmpart: values must be sorted in non-decreasing order")
)

// Partition splits the sorted values x with weights w into at most k
// contiguous non-empty buckets, minimizing the total weighted absolute
// deviation from each bucket's lower weighted median. Among all optimal
// partitions it prefers fewer buckets, then the lexicographically smallest
// cut positions. If k exceeds len(x) it is clamped to len(x).
//
// Costs are accumulated in int64: the total weight must stay below 1<<62
// and every intermediate and total cost must fit in an int64.
//
// It runs in O(n^2*log n + n^2*k) time and O(n^2 + n*k) space.
func Partition(x, w []int64, k int) (Result, error) {
	if err := validate(x, w, k); err != nil {
		return Result{}, err
	}
	n := len(x)
	if n == 0 {
		return Result{}, nil
	}
	if k > n {
		k = n
	}
	cost, pw := bucketCosts(x, w)

	const unreachable = int64(-1)
	// dp[i][b] is the minimum cost of partitioning x[i:] into exactly b
	// non-empty buckets, or unreachable when impossible.
	dp := make([][]int64, n+1)
	for i := range dp {
		dp[i] = make([]int64, k+1)
		for b := range dp[i] {
			dp[i][b] = unreachable
		}
	}
	dp[n][0] = 0
	for i := n - 1; i >= 0; i-- {
		for b := 1; b <= k && b <= n-i; b++ {
			best := unreachable
			for e := i + 1; e+(b-1) <= n; e++ {
				prev := dp[e][b-1]
				if prev == unreachable {
					continue
				}
				if c := cost[i][e-1] + prev; best == unreachable || c < best {
					best = c
				}
			}
			dp[i][b] = best
		}
	}

	// Minimum total cost, ties broken towards fewer buckets.
	total := dp[0][1]
	buckets := 1
	for b := 2; b <= k; b++ {
		if dp[0][b] < total {
			total, buckets = dp[0][b], b
		}
	}

	// Reconstruct the lexicographically smallest cut sequence: greedily
	// take the smallest cut that still admits an optimal completion.
	res := Result{TotalCost: total}
	i, target := 0, total
	for rem := buckets; rem > 0; rem-- {
		e := n
		if rem > 1 {
			for e = i + 1; ; e++ {
				if dp[e][rem-1] != unreachable && cost[i][e-1]+dp[e][rem-1] == target {
					break
				}
			}
		}
		res.Buckets = append(res.Buckets, Bucket{
			Start:  i,
			End:    e,
			Median: x[medianIndex(pw, i, e-1)],
			Cost:   cost[i][e-1],
		})
		target -= cost[i][e-1]
		i = e
	}
	return res, nil
}

func validate(x, w []int64, k int) error {
	if len(x) != len(w) {
		return ErrLengthMismatch
	}
	if k < 1 {
		return ErrNonPositiveK
	}
	for i, wi := range w {
		if wi < 0 {
			return ErrNegativeWeight
		}
		if i > 0 && x[i] < x[i-1] {
			return ErrUnsorted
		}
	}
	return nil
}

// bucketCosts returns cost[l][r], the minimum weighted absolute deviation
// for a bucket covering x[l..r], together with the prefix weight sums.
func bucketCosts(x, w []int64) ([][]int64, []int64) {
	n := len(x)
	pw := prefixWeights(w)
	ps := make([]int64, n+1) // prefix sums of w[i]*x[i]
	for i := range x {
		ps[i+1] = ps[i] + w[i]*x[i]
	}
	cost := make([][]int64, n)
	for l := 0; l < n; l++ {
		cost[l] = make([]int64, n)
		for r := l; r < n; r++ {
			j := medianIndex(pw, l, r)
			m := x[j]
			leftW := pw[j+1] - pw[l]
			leftS := ps[j+1] - ps[l]
			rightW := pw[r+1] - pw[j+1]
			rightS := ps[r+1] - ps[j+1]
			cost[l][r] = m*leftW - leftS + rightS - m*rightW
		}
	}
	return cost, pw
}

func prefixWeights(w []int64) []int64 {
	pw := make([]int64, len(w)+1)
	for i, wi := range w {
		pw[i+1] = pw[i] + wi
	}
	return pw
}

// medianIndex returns the smallest index j in [l, r] whose cumulative
// weight reaches half of the bucket's total weight; x[j] is the lower
// weighted median. A bucket with zero total weight yields l.
func medianIndex(pw []int64, l, r int) int {
	total := pw[r+1] - pw[l]
	return l + sort.Search(r-l+1, func(t int) bool {
		return 2*(pw[l+t+1]-pw[l]) >= total
	})
}
