// Package permtest implements exact permutation tests.
//
// TwoSampleExact enumerates all C(nx+ny, nx) ways to split the pooled
// samples into two groups of the observed sizes. Samples are selected by
// position, so duplicate values are treated as distinct items. The
// two-sided p-value is the fraction of splits whose absolute mean
// difference is greater than or equal to the observed one. All arithmetic
// is exact (big.Int / big.Rat), so no floating-point tolerance is needed.
package permtest

import (
	"errors"
	"math/big"
)

// MaxGroupSize bounds each group so that C(2*MaxGroupSize, MaxGroupSize)
// stays comfortably enumerable.
const MaxGroupSize = 12

// TwoSampleResult reports the exact two-sided permutation p-value for the
// difference of means, together with the exact tail evidence.
type TwoSampleResult struct {
	// ObservedDiff is mean(x) - mean(y), exact.
	ObservedDiff *big.Rat
	// Tail is the number of splits with |diff| >= |ObservedDiff|.
	Tail *big.Int
	// Total is the number of splits, C(nx+ny, nx).
	Total *big.Int
	// PValue is Tail/Total, already in lowest terms.
	PValue *big.Rat
}

// NumDen returns the reduced numerator and denominator of the p-value.
func (r *TwoSampleResult) NumDen() (num, den *big.Int) {
	return r.PValue.Num(), r.PValue.Denom()
}

// TwoSampleExact computes the exact two-sided permutation p-value for the
// mean difference of two groups of at most MaxGroupSize samples each.
func TwoSampleExact(x, y []float64) (*TwoSampleResult, error) {
	nx, ny := len(x), len(y)
	if nx == 0 || ny == 0 {
		return nil, errors.New("permtest: both groups must be non-empty")
	}
	if nx > MaxGroupSize || ny > MaxGroupSize {
		return nil, errors.New("permtest: group size exceeds MaxGroupSize")
	}

	n := nx + ny
	// Scale all values to integers: float64 -> exact rational -> multiply
	// by the LCM of all denominators. This keeps every comparison exact.
	rats := make([]*big.Rat, n)
	for i, v := range x {
		rats[i] = new(big.Rat).SetFloat64(v)
	}
	for j, v := range y {
		rats[nx+j] = new(big.Rat).SetFloat64(v)
	}
	scale := big.NewInt(1)
	for _, r := range rats {
		d := r.Denom()
		g := new(big.Int).GCD(nil, nil, scale, d)
		scale.Mul(scale, d.Quo(d, g))
	}
	vals := make([]*big.Int, n)
	for i, r := range rats {
		vals[i] = new(big.Int).Mul(r.Num(), new(big.Int).Quo(scale, r.Denom()))
	}

	total := sumOf(vals)

	// For a split with group-x integer sum sx (scaled values):
	//   diff = sx/(nx*scale) - (total-sx)/(ny*scale)
	//   D = nx*ny*scale*diff = sx*(nx+ny) - total*nx
	// |diff| >= |observed diff|  <=>  |D| >= |D0|, all in big.Int.
	nn := big.NewInt(int64(n))
	nxI := big.NewInt(int64(nx))
	d0 := new(big.Int).Mul(sumOf(vals[:nx]), nn)
	d0.Sub(d0, new(big.Int).Mul(total, nxI))
	absD0 := new(big.Int).Abs(d0)

	observed := new(big.Rat).Sub(meanRat(rats[:nx]), meanRat(rats[nx:]))

	tail := big.NewInt(0)
	count := big.NewInt(0)
	sx := new(big.Int)
	d := new(big.Int)
	t := new(big.Int)
	forEachCombination(n, nx, func(idx []int) {
		sx.SetInt64(0)
		for _, i := range idx {
			sx.Add(sx, vals[i])
		}
		d.Mul(sx, nn)
		t.Mul(total, nxI)
		d.Sub(d, t)
		if new(big.Int).Abs(d).Cmp(absD0) >= 0 {
			tail.Add(tail, big.NewInt(1))
		}
		count.Add(count, big.NewInt(1))
	})

	return &TwoSampleResult{
		ObservedDiff: observed,
		Tail:         tail,
		Total:        count,
		PValue:       new(big.Rat).SetFrac(tail, count),
	}, nil
}

func meanRat(rs []*big.Rat) *big.Rat {
	s := new(big.Rat)
	for _, r := range rs {
		s.Add(s, r)
	}
	return s.Quo(s, new(big.Rat).SetInt64(int64(len(rs))))
}

func sumOf(vals []*big.Int) *big.Int {
	s := new(big.Int)
	for _, v := range vals {
		s.Add(s, v)
	}
	return s
}

// forEachCombination calls fn with every size-k index subset of
// {0, ..., n-1}, in lexicographic order.
func forEachCombination(n, k int, fn func(idx []int)) {
	idx := make([]int, k)
	for i := range idx {
		idx[i] = i
	}
	for {
		fn(idx)
		i := k - 1
		for i >= 0 && idx[i] == n-k+i {
			i--
		}
		if i < 0 {
			return
		}
		idx[i]++
		for j := i + 1; j < k; j++ {
			idx[j] = idx[j-1] + 1
		}
	}
}
