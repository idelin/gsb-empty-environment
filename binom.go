// Package binom computes exact binomial coefficients and their residues
// modulo an arbitrary positive integer, including composite moduli whose
// factorials are not invertible.
//
// Modular evaluation factors the modulus into prime powers (with a
// configurable work budget), evaluates C(n, k) mod p^a without relying on
// factorial inverses, and recombines the residues via CRT. A Calculator
// caches factorizations so batched queries against the same modulus share
// the factorization work.
package binom

import (
	"errors"
	"math/big"
)

// ErrNegativeN is returned when n is negative.
var ErrNegativeN = errors.New("binom: n must be non-negative")

// ErrNonPositiveModulus is returned when the modulus is not a positive integer.
var ErrNonPositiveModulus = errors.New("binom: modulus must be a positive integer")

// ErrFactorBudgetExceeded is returned when factoring the modulus requires
// more trial-division work than the configured budget allows.
var ErrFactorBudgetExceeded = errors.New("binom: factorization budget exceeded")

// Exact returns the exact value of C(n, k) as a big integer.
//
// By convention C(n, k) = 0 when k < 0 or k > n. n must be non-negative.
func Exact(n, k int64) (*big.Int, error) {
	if n < 0 {
		return nil, ErrNegativeN
	}
	return exact(n, k), nil
}

// exact computes C(n, k) multiplicatively; each intermediate is an integer.
func exact(n, k int64) *big.Int {
	if k < 0 || k > n {
		return big.NewInt(0)
	}
	if k > n-k {
		k = n - k
	}
	res := big.NewInt(1)
	tmp := new(big.Int)
	for i := int64(1); i <= k; i++ {
		res.Mul(res, tmp.SetInt64(n-k+i))
		res.Quo(res, tmp.SetInt64(i))
	}
	return res
}
