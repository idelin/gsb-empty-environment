// Package exact provides exact decimal fixed-point parsing, rational
// arithmetic chains, and half-even rounding without any intermediate
// floating-point conversion.
package exact

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// ErrDivideByZero is returned when a division by a zero rational is attempted.
var ErrDivideByZero = errors.New("exact: division by zero")

// ParseDecimal parses a decimal fixed-point literal (optional sign, integer
// part, optional fractional part) into an exact rational. No floating point
// is involved at any point.
func ParseDecimal(s string) (*big.Rat, error) {
	orig := s
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("exact: empty decimal literal")
	}
	neg := false
	if s[0] == '+' || s[0] == '-' {
		neg = s[0] == '-'
		s = s[1:]
	}
	if s == "" {
		return nil, fmt.Errorf("exact: invalid decimal literal %q", orig)
	}
	intPart := s
	fracPart := ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart = s[:i]
		fracPart = s[i+1:]
		if strings.IndexByte(fracPart, '.') >= 0 {
			return nil, fmt.Errorf("exact: invalid decimal literal %q", orig)
		}
	}
	if intPart == "" && fracPart == "" {
		return nil, fmt.Errorf("exact: invalid decimal literal %q", orig)
	}
	for _, c := range intPart + fracPart {
		if c < '0' || c > '9' {
			return nil, fmt.Errorf("exact: invalid decimal literal %q", orig)
		}
	}
	digits := intPart + fracPart
	num := new(big.Int)
	num.SetString(digits, 10)
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(len(fracPart))), nil)
	r := new(big.Rat).SetFrac(num, den)
	if neg {
		r.Neg(r)
	}
	return r, nil
}

// MustDecimal is a helper for tests and literals; it panics on invalid input.
func MustDecimal(s string) *big.Rat {
	r, err := ParseDecimal(s)
	if err != nil {
		panic(err)
	}
	return r
}

// Rational builds an exact rational factor num/den. A zero denominator is an
// explicit error.
func Rational(num, den int64) (*big.Rat, error) {
	if den == 0 {
		return nil, ErrDivideByZero
	}
	return big.NewRat(num, den), nil
}
