package fxacct

import (
	"fmt"
	"math/big"
	"strings"
)

// ParseRat parses an exact rational: "3/4", "1.0773", "-5", ...
func ParseRat(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("fxacct: cannot parse rational %q", s)
	}
	return r, nil
}

// MustRat is ParseRat that panics on error; for literals and tests.
func MustRat(s string) *big.Rat {
	r, err := ParseRat(s)
	if err != nil {
		panic(err)
	}
	return r
}

// DecimalString renders r in decimal notation. Non-terminating
// expansions are truncated after maxDigits fractional digits and
// suffixed with "…" to mark them as inexact displays of an exact
// value.
func DecimalString(r *big.Rat, maxDigits int) string {
	if r.IsInt() {
		return r.Num().String()
	}
	sign := ""
	if r.Sign() < 0 {
		sign = "-"
	}
	abs := new(big.Rat).Abs(r)
	den := abs.Denom()
	intPart := new(big.Int)
	rem := new(big.Int)
	intPart.QuoRem(abs.Num(), den, rem)

	var sb strings.Builder
	sb.WriteString(sign)
	sb.WriteString(intPart.String())
	sb.WriteByte('.')
	ten := big.NewInt(10)
	for i := 0; i < maxDigits; i++ {
		rem.Mul(rem, ten)
		digit := new(big.Int)
		next := new(big.Int)
		digit.QuoRem(rem, den, next)
		rem = next
		sb.WriteString(digit.String())
		if rem.Sign() == 0 {
			return sb.String()
		}
	}
	sb.WriteString("…")
	return sb.String()
}

// FormatValue renders an exact intermediate value for audit: the
// reduced fraction plus a decimal expansion, e.g.
// "10839/1000 (10.839)" or "1/300 (0.003333333333…)".
func FormatValue(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	return fmt.Sprintf("%s (%s)", r.RatString(), DecimalString(r, 12))
}
