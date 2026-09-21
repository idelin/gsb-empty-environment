package floatconv

import (
	"errors"
	"math/big"
	"strings"
)

// ErrSyntax reports a malformed decimal input string.
var ErrSyntax = errors.New("floatconv: invalid decimal syntax")

// ErrExpRange reports a decimal exponent whose magnitude exceeds the
// supported range (|decimal exponent| > 2^20).
var ErrExpRange = errors.New("floatconv: decimal exponent out of supported range")

// maxDecimalExp bounds |dexp| so that 5^|dexp| stays a manageable exact
// integer.
const maxDecimalExp = 1 << 20

// ParseDecimal converts an exact decimal string directly into the target
// format. The decimal value is parsed into an exact integer ratio and
// rounded once, by the same integer rounding engine used for bit-pattern
// conversion. The value is never materialized as a host float64, so no
// double rounding can occur.
//
// Grammar (case-insensitive specials):
//
//	[+-]? ( digits ["." [digits]] | "." digits ) [ ("e"|"E") [+-]? digits ]
//	[+-]? ("inf" | "infinity" | "nan")
//
// Signed zeros are preserved ("-0" yields negative zero). "nan" yields a
// canonical quiet NaN (quiet bit set, zero payload). "inf" yields an
// infinity, or saturates like an overflowing value when the format has no
// infinity encoding.
func ParseDecimal(f Format, s string, mode RoundingMode) (uint64, Flags, error) {
	if err := f.Validate(); err != nil {
		return 0, 0, err
	}
	v, cls, err := parseDecimal(s)
	if err != nil {
		return 0, 0, err
	}
	switch cls {
	case classZero:
		return v.sign << f.signShift(), 0, nil
	case classInf:
		if f.HasInf {
			return f.infBits(v.sign), 0, nil
		}
		return f.maxFiniteBits(v.sign), FlagOverflow | FlagInexact, nil
	case classNaN:
		bits, fl := convertNaN(f, f, v.sign, f.quietBit())
		return bits, fl, nil
	}
	bits, fl := roundToFormat(f, v, mode)
	return bits, fl, nil
}

// parseDecimal parses s into an exactValue (or a special class).
func parseDecimal(s string) (exactValue, valueClass, error) {
	var v exactValue
	if s == "" {
		return v, classZero, ErrSyntax
	}
	neg := false
	if s[0] == '+' || s[0] == '-' {
		neg = s[0] == '-'
		s = s[1:]
	}
	if neg {
		v.sign = 1
	}
	if low := strings.ToLower(s); low == "inf" || low == "infinity" {
		return v, classInf, nil
	} else if low == "nan" {
		return v, classNaN, nil
	}

	// Mantissa digits with an optional fraction point.
	var digits strings.Builder
	fracDigits := 0
	seenDigit, seenPoint := false, false
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			digits.WriteByte(c)
			seenDigit = true
			if seenPoint {
				fracDigits++
			}
			i++
		case c == '.' && !seenPoint:
			seenPoint = true
			i++
		default:
			goto mantissaDone
		}
	}
mantissaDone:
	if !seenDigit {
		return v, classZero, ErrSyntax
	}

	// Optional decimal exponent.
	expVal := 0
	expNeg := false
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			expNeg = s[i] == '-'
			i++
		}
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return v, classZero, ErrSyntax
		}
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			if expVal <= maxDecimalExp { // saturate the scan; range-checked below
				expVal = expVal*10 + int(s[i]-'0')
			}
			i++
		}
	}
	if i != len(s) {
		return v, classZero, ErrSyntax
	}

	dexp := expVal - fracDigits
	if expNeg {
		dexp = -expVal - fracDigits
	}
	if dexp > maxDecimalExp || dexp < -maxDecimalExp {
		return v, classZero, ErrExpRange
	}

	mant, ok := new(big.Int).SetString(digits.String(), 10)
	if !ok {
		return v, classZero, ErrSyntax
	}
	if mant.Sign() == 0 {
		return v, classZero, nil // signed zero
	}

	// value = mant * 10^dexp.
	// dexp >= 0: 10^dexp = 5^dexp * 2^dexp  -> num = mant*5^dexp, exp2 = dexp
	// dexp <  0: 10^dexp = 1/(5^-dexp * 2^-dexp) -> den = 5^-dexp, exp2 = dexp
	k := dexp
	if k < 0 {
		k = -k
	}
	pow5 := new(big.Int).Exp(big.NewInt(5), big.NewInt(int64(k)), nil)
	v.exp2 = dexp
	if dexp >= 0 {
		v.num = mant.Mul(mant, pow5)
		v.den = bigOne
	} else {
		v.num = mant
		v.den = pow5
	}
	return v, classFinite, nil
}
