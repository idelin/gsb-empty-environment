package rowconv

import (
	"fmt"
	"math"
	"math/big"
)

var (
	bigOne    = big.NewInt(1)
	bigMaxInt = big.NewInt(math.MaxInt64)
	bigMinInt = big.NewInt(math.MinInt64)
)

func fitsInt64(x *big.Int) bool {
	return x.Cmp(bigMinInt) >= 0 && x.Cmp(bigMaxInt) <= 0
}

func pow10(n int32) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// roundQuotientHalfEven adjusts q (the truncated quotient of c/d) to the
// nearest integer, ties to even, based on the remainder r = c - q*d.
// c may be negative; rounding is done on the magnitude.
func roundQuotientHalfEven(q, r, d *big.Int, negative bool) {
	absR := new(big.Int).Abs(r)
	twice := new(big.Int).Lsh(absR, 1)
	cmp := twice.Cmp(d)
	up := cmp > 0 || (cmp == 0 && new(big.Int).Abs(q).Bit(0) == 1)
	if !up {
		return
	}
	if negative {
		q.Sub(q, bigOne)
	} else {
		q.Add(q, bigOne)
	}
}

// RescaleDecimal converts a coefficient from fromScale to toScale.
// Narrowing that is not exact is rejected unless mode is ModeHalfEven.
func RescaleDecimal(coef int64, fromScale, toScale int32, mode Mode) (int64, *Error) {
	switch {
	case toScale == fromScale:
		return coef, nil
	case toScale > fromScale:
		c := new(big.Int).Mul(big.NewInt(coef), pow10(toScale-fromScale))
		if !fitsInt64(c) {
			return 0, &Error{Code: CodeOverflow,
				Msg: fmt.Sprintf("coefficient %d overflows int64 at scale %d", coef, toScale)}
		}
		return c.Int64(), nil
	default:
		d := pow10(fromScale - toScale)
		q, r := new(big.Int), new(big.Int)
		q.QuoRem(big.NewInt(coef), d, r)
		if r.Sign() == 0 {
			return q.Int64(), nil
		}
		if mode != ModeHalfEven {
			return 0, &Error{Code: CodeInexact,
				Msg: fmt.Sprintf("coefficient %d not divisible by 10^%d", coef, fromScale-toScale)}
		}
		roundQuotientHalfEven(q, r, d, coef < 0)
		if !fitsInt64(q) {
			return 0, &Error{Code: CodeOverflow,
				Msg: fmt.Sprintf("rounded coefficient overflows int64 at scale %d", toScale)}
		}
		return q.Int64(), nil
	}
}
