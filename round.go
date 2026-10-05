package decimal

import "math/big"

// RoundingMode selects how a discarded fractional part is handled.
type RoundingMode int

const (
	// ToZero rounds toward zero (truncation).
	ToZero RoundingMode = iota
	// ToPosInf rounds toward positive infinity (ceiling).
	ToPosInf
	// ToNegInf rounds toward negative infinity (floor).
	ToNegInf
	// HalfEven rounds to nearest, ties to the even neighbor (banker's).
	HalfEven
)

// Round returns d rounded to scale fractional digits using mode.
// The value is unchanged when d already has at most scale fractional
// digits; Round never pads with zeros to suggest more precision.
func (d Decimal) Round(scale uint32, mode RoundingMode) Decimal {
	s := int64(scale)
	if d.exp >= -s {
		return d
	}
	div := pow10(-s - d.exp)
	q := new(big.Int)
	r := new(big.Int)
	q.QuoRem(d.mantissa(), div, r)
	roundQuotient(q, r, div, mode)
	return makeDecimal(q, -s)
}

// roundQuotient adjusts q, the truncated quotient num/den with remainder
// rem, according to mode. rem and den must be the QuoRem outputs, so the
// discarded fraction is rem/den. q is modified in place.
func roundQuotient(q, rem, den *big.Int, mode RoundingMode) {
	if rem.Sign() == 0 {
		return
	}
	fracSign := rem.Sign() * den.Sign()
	switch mode {
	case ToZero:
		// QuoRem already truncated toward zero.
	case ToPosInf:
		if fracSign > 0 {
			q.Add(q, bigOne)
		}
	case ToNegInf:
		if fracSign < 0 {
			q.Sub(q, bigOne)
		}
	case HalfEven:
		twice := new(big.Int).Abs(rem)
		twice.Lsh(twice, 1)
		absDen := new(big.Int).Abs(den)
		c := twice.Cmp(absDen)
		// q.Bit(0) is the parity of |q|; on a tie move away from zero
		// only when q is odd, leaving the even neighbor.
		if c > 0 || (c == 0 && q.Bit(0) == 1) {
			if fracSign > 0 {
				q.Add(q, bigOne)
			} else {
				q.Sub(q, bigOne)
			}
		}
	}
}
