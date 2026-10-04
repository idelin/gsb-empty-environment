package calib

import (
	"fmt"
	"math/big"
)

// Bound represents an optional exact rational bound.
// A nil *big.Rat denotes an unbounded (infinite) side.
type Interval struct {
	Lo *big.Rat // nil means -inf
	Hi *big.Rat // nil means +inf
}

func ParseRat(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("invalid rational number %q", s)
	}
	return r, nil
}

func RatInt(v int64) *big.Rat { return big.NewRat(v, 1) }

func RatStr(r *big.Rat) string {
	if r == nil {
		return "null"
	}
	return r.RatString()
}

// CeilInt returns the smallest integer >= r.
func CeilInt(r *big.Rat) *big.Int {
	q, rem := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	if rem.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

// FloorInt returns the largest integer <= r.
func FloorInt(r *big.Rat) *big.Int {
	q, rem := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	if rem.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
	}
	return q
}

// minRat returns the smaller bound; nil is treated as +inf for "min" semantics.
func minBound(a, b *big.Rat) *big.Rat {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

// maxBound treats nil as -inf.
func maxBound(a, b *big.Rat) *big.Rat {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}

func addRat(a, b *big.Rat) *big.Rat { return new(big.Rat).Add(a, b) }
func subRat(a, b *big.Rat) *big.Rat { return new(big.Rat).Sub(a, b) }
func mulRat(a, b *big.Rat) *big.Rat { return new(big.Rat).Mul(a, b) }
