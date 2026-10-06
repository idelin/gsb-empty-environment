// Package permtest implements exact permutation (randomization) tests.
//
// All enumeration is over sample positions, never over distinct values, so
// duplicate observations are treated as separate samples. Tail probabilities
// are two-sided: an assignment lies in the tail when the absolute value of
// its statistic is greater than or equal to the absolute observed statistic.
// All statistic arithmetic uses exact rational numbers, so the reported tail
// probabilities are exact fractions.
package permtest

import (
	"fmt"
	"math"
	"math/big"
)

// MaxGroupSize bounds each group of TwoSample so that full enumeration stays
// feasible (the worst case is C(24, 12) = 2,704,156 assignments).
const MaxGroupSize = 12

// Fraction is an exact tail probability: Num tail assignments out of Den
// enumerated assignments.
type Fraction struct {
	Num int64
	Den int64
}

// Reduced returns the fraction in lowest terms.
func (f Fraction) Reduced() Fraction {
	g := gcd(f.Num, f.Den)
	return Fraction{Num: f.Num / g, Den: f.Den / g}
}

// Float returns the probability as a float64.
func (f Fraction) Float() float64 {
	return float64(f.Num) / float64(f.Den)
}

// String reports the numerator, the denominator and the reduced fraction.
func (f Fraction) String() string {
	r := f.Reduced()
	return fmt.Sprintf("%d/%d = %d/%d (~%g)", f.Num, f.Den, r.Num, r.Den, f.Float())
}

func gcd(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

// rat converts v to its exact rational value.
func rat(v float64) (*big.Rat, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, fmt.Errorf("permtest: non-finite sample value %v", v)
	}
	return new(big.Rat).SetFloat64(v), nil
}

// eachCombination calls fn for every k-subset of {0, ..., n-1}, in
// lexicographic order. idx is reused between calls.
func eachCombination(n, k int, fn func(idx []int)) {
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

// binomial returns C(n, k).
func binomial(n, k int) int64 {
	if k < 0 || k > n {
		return 0
	}
	if k > n-k {
		k = n - k
	}
	r := int64(1)
	for i := 0; i < k; i++ {
		r = r * int64(n-i) / int64(i+1)
	}
	return r
}
