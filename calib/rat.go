package calib

import "math/big"

var bigOne = big.NewInt(1)

// maxRat returns the larger of of a and b; nil means -infinity.
func maxRat(a, b *big.Rat) *big.Rat {
	if a == nil {
		return new(big.Rat).Set(b)
	}
	if b == nil {
		return new(big.Rat).Set(a)
	}
	if a.Cmp(b) >= 0 {
		return new(big.Rat).Set(a)
	}
	return new(big.Rat).Set(b)
}

// minRat returns the smaller of a and b; nil means +infinity.
func minRat(a, b *big.Rat) *big.Rat {
	if a == nil {
		return new(big.Rat).Set(b)
	}
	if b == nil {
		return new(big.Rat).Set(a)
	}
	if a.Cmp(b) <= 0 {
		return new(big.Rat).Set(a)
	}
	return new(big.Rat).Set(b)
}

// ratFloor returns floor(r). big.Rat is always normalized with a positive
// denominator, so Euclidean division yields the floor directly.
func ratFloor(r *big.Rat) *big.Int {
	q := new(big.Int)
	m := new(big.Int)
	q.DivMod(r.Num(), r.Denom(), m)
	return q
}

// ratCeil returns ceil(r).
func ratCeil(r *big.Rat) *big.Int {
	f := ratFloor(r)
	if r.Denom().Cmp(bigOne) != 0 {
		f.Add(f, bigOne)
	}
	return f
}

func ratIsInt(r *big.Rat) bool {
	return r.Denom().Cmp(bigOne) == 0
}
