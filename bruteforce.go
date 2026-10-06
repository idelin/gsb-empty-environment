package wmpart

import "math/big"

// BruteForce solves the same problem as Partition by exhaustively
// enumerating every set of cut positions (every composition of n into at
// most k non-empty parts). It runs in O(2^n * n) time and is intended as a
// reference implementation for tests and cross-validation.
//
// Costs are accumulated with big.Int, so it stays exact even where an
// int64 accumulation would overflow; the reported Result still holds
// int64 costs and is only meaningful when they fit.
func BruteForce(x, w []int64, k int) (Result, error) {
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
	pw := prefixWeights(w)

	var best Result
	bestCost := new(big.Int)
	found := false

	var walk func(start, remaining int, acc *big.Int, buckets []Bucket)
	walk = func(start, remaining int, acc *big.Int, buckets []Bucket) {
		if start == n {
			if !found || lessCandidate(acc, buckets, bestCost, best.Buckets) {
				found = true
				bestCost.Set(acc)
				best = Result{
					TotalCost: acc.Int64(),
					Buckets:   append([]Bucket(nil), buckets...),
				}
			}
			return
		}
		if remaining == 0 {
			return
		}
		for e := start + 1; e <= n; e++ {
			j := medianIndex(pw, start, e-1)
			m := x[j]
			c := new(big.Int)
			for i := start; i < e; i++ {
				d := new(big.Int).Sub(big.NewInt(x[i]), big.NewInt(m))
				d.Abs(d)
				c.Add(c, d.Mul(d, big.NewInt(w[i])))
			}
			b := Bucket{Start: start, End: e, Median: m, Cost: c.Int64()}
			walk(e, remaining-1, new(big.Int).Add(acc, c), append(buckets, b))
		}
	}
	walk(0, k, new(big.Int), nil)
	return best, nil
}

// lessCandidate reports whether candidate a is strictly preferred over
// candidate b: lower total cost, then fewer buckets, then the
// lexicographically smaller cut positions.
func lessCandidate(aCost *big.Int, a []Bucket, bCost *big.Int, b []Bucket) bool {
	if c := aCost.Cmp(bCost); c != 0 {
		return c < 0
	}
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	for i := range a {
		if a[i].Start != b[i].Start {
			return a[i].Start < b[i].Start
		}
	}
	return false
}
