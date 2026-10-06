package settle

import (
	"math/big"
)

// RoundHalfEven rounds r to the given number of decimal places using
// banker's rounding (ties to even). The result is an exact multiple of
// 10^-decimals.
func RoundHalfEven(r *big.Rat, decimals int) *big.Rat {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(scale))

	num := scaled.Num()
	den := scaled.Denom() // always positive

	floor := new(big.Int).Div(new(big.Int).Set(num), den) // floored division
	rem := new(big.Rat).Sub(scaled, new(big.Rat).SetInt(floor))

	half := big.NewRat(1, 2)
	chosen := floor
	switch rem.Cmp(half) {
	case 1:
		chosen = new(big.Int).Add(floor, big.NewInt(1))
	case 0:
		if floor.Bit(0) == 1 {
			chosen = new(big.Int).Add(floor, big.NewInt(1))
		}
	}
	return new(big.Rat).SetFrac(chosen, scale)
}

// RoundToCents rounds r to cents (2 decimal places) with half-even ties.
func RoundToCents(r *big.Rat) *big.Rat {
	return RoundHalfEven(r, 2)
}

// Money formats an exact rational as a fixed 2-decimal string. The value
// must already be rounded to cents (as produced by RoundToCents).
func Money(r *big.Rat) string {
	return r.FloatString(2)
}
