// Package bucketpart partitions an ordered sequence of weighted samples
// into at most k consecutive non-empty buckets so that the total weighted
// absolute deviation from each bucket's representative is minimized.
//
// The representative of a bucket is its lower weighted median: the smallest
// sample value whose cumulative weight (in bucket order, inclusive) reaches
// (totalWeight+1)/2. When the total weight is even this picks the smaller of
// the two middle values. A bucket whose total weight is zero has zero cost
// regardless of its representative.
//
// Among all partitions achieving the minimum cost, the one with fewer
// buckets is preferred; ties are broken by choosing the lexicographically
// smallest sequence of bucket boundaries. All costs are computed with
// math/big so arbitrarily large weights and values are safe.
package bucketpart

import (
	"errors"
	"math/big"
)

// Sample is one ordered observation. Value must be non-decreasing across
// the input slice; Weight must be non-negative.
type Sample struct {
	Value  int64
	Weight int64
}

var (
	// ErrBadK is returned when k is smaller than 1 while samples exist.
	ErrBadK = errors.New("bucketpart: k must be at least 1")
	// ErrNotOrdered is returned when sample values are not non-decreasing.
	ErrNotOrdered = errors.New("bucketpart: sample values must be non-decreasing")
	// ErrNegativeWeight is returned when a sample weight is negative.
	ErrNegativeWeight = errors.New("bucketpart: sample weights must be non-negative")
)

// Partition splits samples into at most k consecutive non-empty buckets and
// returns the bucket boundaries together with the minimal total cost.
//
// The returned boundaries are the start indices of the buckets; the first
// element is always 0 and len(boundaries) is the number of buckets used.
// The cost is the sum over all samples of weight * |value - representative|
// and is never nil. An empty input yields nil boundaries and zero cost.
func Partition(samples []Sample, k int) ([]int, *big.Int, error) {
	n := len(samples)
	if n == 0 {
		return nil, new(big.Int), nil
	}
	if k < 1 {
		return nil, nil, ErrBadK
	}
	for i, s := range samples {
		if s.Weight < 0 {
			return nil, nil, ErrNegativeWeight
		}
		if i > 0 && s.Value < samples[i-1].Value {
			return nil, nil, ErrNotOrdered
		}
	}
	if k > n {
		k = n
	}

	// Prefix sums of weights and of weight*value.
	pw := make([]*big.Int, n+1)
	px := make([]*big.Int, n+1)
	pw[0] = new(big.Int)
	px[0] = new(big.Int)
	for i, s := range samples {
		pw[i+1] = new(big.Int).Add(pw[i], big.NewInt(s.Weight))
		wx := new(big.Int).Mul(big.NewInt(s.Weight), big.NewInt(s.Value))
		px[i+1] = new(big.Int).Add(px[i], wx)
	}

	// cost[i][j] is the cost of the bucket covering samples[i:j].
	cost := make([][]*big.Int, n+1)
	for i := 0; i < n; i++ {
		cost[i] = make([]*big.Int, n+1)
		for j := i + 1; j <= n; j++ {
			cost[i][j] = bucketCost(samples, pw, px, i, j)
		}
	}

	type state struct {
		cost *big.Int // nil means unreachable
		path []int    // bucket start indices, len == number of buckets
	}

	// dp[b][j]: best way to cover samples[:j] with exactly b buckets.
	dp := make([][]state, k+1)
	for b := range dp {
		dp[b] = make([]state, n+1)
	}
	dp[0][0] = state{cost: new(big.Int)}
	for b := 1; b <= k; b++ {
		for j := b; j <= n; j++ {
			var best state
			for i := b - 1; i <= j-1; i++ {
				prev := dp[b-1][i]
				if prev.cost == nil {
					continue
				}
				cand := new(big.Int).Add(prev.cost, cost[i][j])
				path := make([]int, len(prev.path)+1)
				copy(path, prev.path)
				path[len(path)-1] = i
				if best.cost == nil || cand.Cmp(best.cost) < 0 ||
					(cand.Cmp(best.cost) == 0 && lessPath(path, best.path)) {
					best = state{cost: cand, path: path}
				}
			}
			dp[b][j] = best
		}
	}

	// Scan bucket counts ascending so that equal-cost ties keep fewer buckets.
	var best state
	for b := 1; b <= k; b++ {
		s := dp[b][n]
		if s.cost == nil {
			continue
		}
		if best.cost == nil || s.cost.Cmp(best.cost) < 0 {
			best = s
		}
	}
	return best.path, best.cost, nil
}

// bucketCost returns sum_{t in [i,j)} weight_t * |value_t - rep| where rep
// is the lower weighted median of samples[i:j].
func bucketCost(samples []Sample, pw, px []*big.Int, i, j int) *big.Int {
	total := new(big.Int).Sub(pw[j], pw[i])
	if total.Sign() == 0 {
		return new(big.Int)
	}
	// Smallest cumulative weight reaching (total+1)/2.
	threshold := new(big.Int).Add(total, big.NewInt(1))
	threshold.Rsh(threshold, 1)

	lo, hi := i, j-1
	for lo < hi {
		mid := (lo + hi) / 2
		cum := new(big.Int).Sub(pw[mid+1], pw[i])
		if cum.Cmp(threshold) >= 0 {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	m := lo
	rep := big.NewInt(samples[m].Value)

	// Left part [i, m]: rep >= value_t.
	wl := new(big.Int).Sub(pw[m+1], pw[i])
	sl := new(big.Int).Sub(px[m+1], px[i])
	left := new(big.Int).Sub(new(big.Int).Mul(rep, wl), sl)
	// Right part [m+1, j): value_t >= rep.
	wr := new(big.Int).Sub(pw[j], pw[m+1])
	sr := new(big.Int).Sub(px[j], px[m+1])
	right := new(big.Int).Sub(sr, new(big.Int).Mul(rep, wr))

	return left.Add(left, right)
}

// lessPath reports whether a is lexicographically smaller than b.
func lessPath(a, b []int) bool {
	for t := 0; t < len(a) && t < len(b); t++ {
		if a[t] != b[t] {
			return a[t] < b[t]
		}
	}
	return len(a) < len(b)
}
