// Package primeinterp provides polynomial interpolation over a prime field
// and a revision-aware sample stream that tracks the current interpolant.
package primeinterp

import (
	"errors"
	"math/big"
)

// ErrNotPrime is returned when the field modulus is not a prime >= 2.
var ErrNotPrime = errors.New("primeinterp: modulus is not prime")

// ErrConflict is returned when two samples share an x-coordinate but
// disagree on the y value.
var ErrConflict = errors.New("primeinterp: conflicting samples at same x")

// ConflictError describes a duplicate x-coordinate with mismatched y values.
type ConflictError struct {
	X      int64
	Y1, Y2 int64
}

func (e *ConflictError) Error() string {
	return ErrConflict.Error()
}

// mod reduces a into [0, p). It handles negative inputs.
func mod(a, p int64) int64 {
	r := a % p
	if r < 0 {
		r += p
	}
	return r
}

// mulMod returns a*b mod p. It assumes a, b are already reduced into
// [0, p) and p fits in an int64. Falls back to big.Int when the plain
// product could overflow.
func mulMod(a, b, p int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	// sqrt(MaxInt64) ~ 3037000499; below that a*b cannot overflow int64.
	if p <= 3037000499 {
		return (a * b) % p
	}
	prod := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	return prod.Mod(prod, big.NewInt(p)).Int64()
}

// powMod returns base^exp mod p for exp >= 0.
func powMod(base, exp, p int64) int64 {
	result := int64(1)
	b := mod(base, p)
	for exp > 0 {
		if exp&1 == 1 {
			result = mulMod(result, b, p)
		}
		b = mulMod(b, b, p)
		exp >>= 1
	}
	return result
}

// invMod returns the multiplicative inverse of a mod p. p must be prime
// and a must be non-zero mod p.
func invMod(a, p int64) int64 {
	return powMod(a, p-2, p)
}

// isPrime reports whether n is prime (deterministic for int64).
func isPrime(n int64) bool {
	if n < 2 {
		return false
	}
	small := []int64{2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37}
	for _, s := range small {
		if n == s {
			return true
		}
		if n%s == 0 {
			return false
		}
	}
	// Deterministic Miller-Rabin bases for 64-bit integers.
	d := n - 1
	s := 0
	for d&1 == 0 {
		d >>= 1
		s++
	}
	bases := []int64{2, 325, 9375, 28178, 450775, 9780504, 1795265022}
	for _, a := range bases {
		a %= n
		if a == 0 {
			continue
		}
		x := powMod(a, int64(d), n)
		if x == 1 || x == n-1 {
			continue
		}
		composite := true
		for r := 0; r < s-1; r++ {
			x = mulMod(x, x, n)
			if x == n-1 {
				composite = false
				break
			}
		}
		if composite {
			return false
		}
	}
	return true
}
