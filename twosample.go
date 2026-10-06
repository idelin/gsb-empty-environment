package permtest

import (
	"errors"
	"fmt"
	"math"
	"math/big"
)

// TwoSample computes the exact two-sided permutation p-value for the
// difference of means mean(x)-mean(y) between two groups of at most
// MaxGroupSize samples each.
//
// Every choice of len(x) positions out of the pooled len(x)+len(y) samples
// is enumerated; duplicate values are distinguished by position, never
// deduplicated. The tail contains every assignment whose absolute mean
// difference is greater than or equal to the observed one. The returned
// fraction is exact: its numerator is the tail count and its denominator is
// C(len(x)+len(y), len(x)).
//
// Values are scaled by their common power-of-two denominator and all
// comparisons are done in exact integer arithmetic, so floating-point
// rounding never affects the tail count.
func TwoSample(x, y []float64) (Fraction, error) {
	if len(x) == 0 || len(y) == 0 {
		return Fraction{}, errors.New("permtest: both groups must be non-empty")
	}
	if len(x) > MaxGroupSize || len(y) > MaxGroupSize {
		return Fraction{}, fmt.Errorf("permtest: each group may hold at most %d samples", MaxGroupSize)
	}
	n1, n2 := len(x), len(y)
	n := n1 + n2

	// Every float64 is num/2^shift exactly; rescale to integers with a
	// common denominator 2^maxShift.
	nums := make([]*big.Int, n)
	shifts := make([]int, n)
	maxShift := 0
	for i, v := range append(append([]float64{}, x...), y...) {
		r, err := rat(v)
		if err != nil {
			return Fraction{}, err
		}
		nums[i] = r.Num()
		shifts[i] = r.Denom().BitLen() - 1 // denom is a power of two
		if shifts[i] > maxShift {
			maxShift = shifts[i]
		}
	}
	scaled := make([]*big.Int, n)
	fits64 := true
	for i := range nums {
		scaled[i] = new(big.Int).Lsh(nums[i], uint(maxShift-shifts[i]))
		if !scaled[i].IsInt64() {
			fits64 = false
		}
	}

	// Compare without divisions:
	//   |sumA/n1 - sumB/n2| >= |obs|  <=>  |sumA*n2 - sumB*n1| >= |obs|*n1*n2
	if fits64 {
		sv := make([]int64, n)
		maxAbs := int64(0)
		for i, s := range scaled {
			sv[i] = s.Int64()
			if a := abs64(sv[i]); a > maxAbs {
				maxAbs = a
			}
		}
		// |sumA*n2 - sumB*n1| <= 2*n1*n2*maxAbs; use the big.Int path if
		// that bound could overflow int64.
		if maxAbs <= math.MaxInt64/(2*int64(n1)*int64(n2)) {
			return twoSampleInt64(sv, n1, n2), nil
		}
	}
	return twoSampleBigInt(scaled, n1, n2), nil
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func twoSampleInt64(sv []int64, n1, n2 int) Fraction {
	n := n1 + n2
	total := int64(0)
	for _, v := range sv {
		total += v
	}
	obsA := int64(0)
	for _, v := range sv[:n1] {
		obsA += v
	}
	obsScaled := abs64(obsA*int64(n2) - (total-obsA)*int64(n1))

	var tail, tot int64
	eachCombination(n, n1, func(idx []int) {
		sumA := int64(0)
		for _, i := range idx {
			sumA += sv[i]
		}
		if abs64(sumA*int64(n2)-(total-sumA)*int64(n1)) >= obsScaled {
			tail++
		}
		tot++
	})
	return Fraction{Num: tail, Den: tot}
}

func twoSampleBigInt(sv []*big.Int, n1, n2 int) Fraction {
	n := n1 + n2
	mul1 := big.NewInt(int64(n1))
	mul2 := big.NewInt(int64(n2))
	total := new(big.Int)
	for _, v := range sv {
		total.Add(total, v)
	}
	obsA := new(big.Int)
	for _, v := range sv[:n1] {
		obsA.Add(obsA, v)
	}
	left := new(big.Int)
	right := new(big.Int)
	obsScaled := new(big.Int).Abs(left.Sub(
		right.Mul(obsA, mul2),
		left.Mul(left.Sub(total, obsA), mul1),
	))

	var tail, tot int64
	sumA := new(big.Int)
	l := new(big.Int)
	r := new(big.Int)
	eachCombination(n, n1, func(idx []int) {
		sumA.SetInt64(0)
		for _, i := range idx {
			sumA.Add(sumA, sv[i])
		}
		l.Mul(sumA, mul2)
		r.Sub(total, sumA)
		r.Mul(r, mul1)
		l.Sub(l, r)
		l.Abs(l)
		if l.Cmp(obsScaled) >= 0 {
			tail++
		}
		tot++
	})
	return Fraction{Num: tail, Den: tot}
}
