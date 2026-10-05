package rowconv

import (
	"fmt"
	"math/big"
)

var bigOne = big.NewInt(1)

// RescaleDecimal returns coeff * 10^(fromScale^-1) re-expressed at toScale,
// i.e. out * 10^-toScale == coeff * 10^-fromScale.
//
// When toScale < fromScale the coefficient must be divided by a power of
// ten. In Reject mode any nonzero remainder is an error; in TiesToEven
// mode the quotient is rounded to the nearest integer, ties toward even.
// The input coefficient is not modified.
func RescaleDecimal(coeff *big.Int, fromScale, toScale int, mode RoundingMode) (*big.Int, error) {
	if fromScale < 0 || toScale < 0 {
		return nil, fmt.Errorf("negative scale")
	}
	if toScale >= fromScale {
		mul := pow10(toScale - fromScale)
		return new(big.Int).Mul(coeff, mul), nil
	}
	div := pow10(fromScale - toScale)
	q := new(big.Int)
	r := new(big.Int)
	q.QuoRem(coeff, div, r) // truncated toward zero
	if r.Sign() == 0 {
		return q, nil
	}
	if mode != TiesToEven {
		return nil, fmt.Errorf("decimal %s cannot be represented exactly at scale %d (remainder %s/10^%d)",
			coeff.String(), toScale, new(big.Int).Abs(r).String(), fromScale-toScale)
	}
	// Round half to even on the magnitude; the sign of q already matches
	// the sign of coeff because QuoRem truncates toward zero.
	twice := new(big.Int).Lsh(new(big.Int).Abs(r), 1)
	cmp := twice.Cmp(div)
	roundAway := cmp > 0 || (cmp == 0 && new(big.Int).Abs(q).Bit(0) == 1)
	if roundAway {
		if coeff.Sign() < 0 {
			q.Sub(q, bigOne)
		} else {
			q.Add(q, bigOne)
		}
	}
	return q, nil
}

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// decimalDigits returns the number of base-10 digits of |c| (zero counts
// as one digit).
func decimalDigits(c *big.Int) int {
	if c.Sign() == 0 {
		return 1
	}
	return len(new(big.Int).Abs(c).String())
}
