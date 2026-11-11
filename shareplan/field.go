// Package shareplan implements nested-threshold secret sharing over GF(257).
//
// A policy is a tree whose internal nodes are t-of-n thresholds and whose
// leaves name participants. The secret is split recursively with Shamir
// sharing: every internal node turns its node secret into one share per
// child, and every leaf occurrence receives an independently identified
// share even when the same participant appears at several leaves.
//
// Reconstruction only combines shares produced for the same node. Shares
// belonging to different nodes are never interpolated together and a
// recovery attempt mixing them is rejected.
package shareplan

import (
	"crypto/rand"
	"errors"
)

// P is the prime defining the field GF(P).
const P = 257

// MaxParticipants is the largest number of distinct participants a policy
// may contain.
const MaxParticipants = 16

// maxNodes bounds the size of a policy tree so that exhaustive checks stay
// tractable.
const maxNodes = 256

var (
	// ErrInvalidSecret is returned when a secret is not a field element.
	ErrInvalidSecret = errors.New("shareplan: secret must be in [0, 256]")
	// ErrInvalidValue is returned when a presented share value is not a
	// field element.
	ErrInvalidValue = errors.New("shareplan: share value must be in [0, 256]")
)

func mod(v int) int {
	v %= P
	if v < 0 {
		v += P
	}
	return v
}

func add(a, b int) int { return mod(a + b) }

func sub(a, b int) int { return mod(a - b) }

func mul(a, b int) int { return mod(a * b) }

// pow returns base^exp in GF(P) for exp >= 0.
func pow(base, exp int) int {
	result := 1
	for exp > 0 {
		if exp&1 == 1 {
			result = mul(result, base)
		}
		base = mul(base, base)
		exp >>= 1
	}
	return result
}

func inverse(v int) int {
	if v == 0 {
		panic("shareplan: inverse of zero")
	}
	return pow(v, P-2)
}

// randField returns a uniformly distributed field element using rng. When
// rng is nil crypto/rand is used. Rejection sampling avoids the bias that a
// naive modulo would introduce because 65536 is not a multiple of 257.
func randField(rng interface{ Read([]byte) (int, error) }) (int, error) {
	if rng == nil {
		rng = rand.Reader
	}
	buf := make([]byte, 2)
	const limit = 65535 // largest value accepted by rejection sampling
	for {
		if _, err := rng.Read(buf); err != nil {
			return 0, err
		}
		v := int(buf[0])<<8 | int(buf[1])
		if v <= limit {
			return v % P, nil
		}
	}
}

// evalPoly evaluates coefficients at x in GF(P), where coefficients[0] is
// the constant term.
func evalPoly(coefficients []int, x int) int {
	y := 0
	for i := len(coefficients) - 1; i >= 0; i-- {
		y = add(mul(y, x), coefficients[i])
	}
	return y
}
