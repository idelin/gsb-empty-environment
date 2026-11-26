package decimal128

import (
	"strconv"
	"strings"
)

// Parse decodes a decimal literal. It accepts optional sign, a coefficient
// with optional decimal point, and an optional base-10 exponent (e.g.
// "1.5", "-0.0002", "3.14e-2", "1E+12", "007.50"). More than 38 significant
// digits trigger scale reduction with the given rounding mode; values whose
// integer part exceeds 38 digits overflow. Negative zero is normalized to
// positive zero.
func Parse(s string, mode RoundingMode) (Decimal128, error) {
	if s == "" {
		return Decimal128{}, ErrSyntax
	}
	negative := false
	switch s[0] {
	case '+':
		s = s[1:]
	case '-':
		negative = true
		s = s[1:]
	}
	if s == "" {
		return Decimal128{}, ErrSyntax
	}

	// Split off an optional decimal exponent first.
	mantissa := s
	exp := 0
	if e := strings.IndexAny(s, "eE"); e >= 0 {
		if e == 0 || e == len(s)-1 {
			return Decimal128{}, ErrSyntax
		}
		v, err := strconv.Atoi(s[e+1:])
		if err != nil {
			return Decimal128{}, ErrSyntax
		}
		exp = v
		mantissa = s[:e]
	}

	var intPart, fracPart string
	if dot := strings.IndexByte(mantissa, '.'); dot >= 0 {
		intPart, fracPart = mantissa[:dot], mantissa[dot+1:]
		if fracPart == "" || strings.IndexByte(fracPart, '.') >= 0 {
			return Decimal128{}, ErrSyntax
		}
	} else {
		intPart = mantissa
	}
	for _, ch := range intPart + fracPart {
		if ch < '0' || ch > '9' {
			return Decimal128{}, ErrSyntax
		}
	}

	digits := intPart + fracPart
	scale := len(fracPart) - exp
	trimmed := strings.TrimLeft(digits, "0")
	if trimmed == "" {
		// Zero: negative zero is normalized to positive zero.
		sc := scale
		if sc < 0 {
			sc = 0
		}
		if sc > maxScale {
			sc = maxScale
		}
		return Decimal128{coeff: i128{}, precision: uint8(maxInt(1, sc)), scale: uint8(sc)}, nil
	}

	// Accumulate the full exact coefficient in 256 bits: a literal may carry
	// more than 38 digits before the scale is reduced (e.g. long fractions).
	var wideMag u256
	for i := 0; i < len(trimmed); i++ {
		wideMag = wideMag.mulPow10(1).add64(uint64(trimmed[i] - '0'))
		// Anything beyond 77 coefficient digits cannot be represented even as
		// an intermediate 256-bit value.
		if i >= 2*maxScale {
			return Decimal128{}, ErrOverflow
		}
	}
	if scale < 0 {
		// A positive exponent beyond the fractional digits shifts left.
		if wideMag.decimalDigits()+(-scale) > 2*maxScale {
			return Decimal128{}, ErrOverflow
		}
		wideMag = wideMag.mulPow10(-scale)
		scale = 0
	}

	if wideMag.cmp(pow10u256[maxPrecision]) < 0 {
		digitsCount := wideMag.decimalDigits()
		integerDigits := digitsCount - scale
		if integerDigits < 1 {
			integerDigits = 1
		}
		precision := maxInt(digitsCount, integerDigits+scale)
		if precision < 1 {
			precision = 1
		}
		return Decimal128{
			coeff:     i128FromMagnitude(u128{lo: wideMag.v[0], hi: wideMag.v[1]}, negative),
			precision: uint8(precision),
			scale:     uint8(scale),
		}, nil
	}

	return canonicalize(wide{mag: wideMag, negative: negative}, scale, mode)
}

// String renders the value in fixed-point notation. Trailing zeros are kept
// so that exactly scale fractional digits appear; negative zero is emitted as
// plain "0"-style zero.
func (d Decimal128) String() string {
	mag, sign := d.coeff.magnitude()
	negative := sign < 0
	if mag.isZero() {
		negative = false
	}
	digits := mag.string()
	scale := int(d.scale)

	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	switch {
	case scale == 0:
		b.WriteString(digits)
	case len(digits) > scale:
		b.WriteString(digits[:len(digits)-scale])
		b.WriteByte('.')
		b.WriteString(digits[len(digits)-scale:])
	default:
		b.WriteByte('0')
		b.WriteByte('.')
		for i := 0; i < scale-len(digits); i++ {
			b.WriteByte('0')
		}
		b.WriteString(digits)
	}
	return b.String()
}

// Scientific renders the value in normalized scientific notation, e.g.
// 12.30 (scale 2) -> "1.230E+1".
func (d Decimal128) Scientific() string {
	if d.IsZero() {
		scale := int(d.scale)
		var b strings.Builder
		b.WriteString("0.")
		for i := 0; i < scale; i++ {
			b.WriteByte('0')
		}
		b.WriteString("E+0")
		return b.String()
	}
	mag, sign := d.coeff.magnitude()
	negative := sign < 0
	digits := mag.string()
	exp10 := len(digits) - int(d.scale) - 1

	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	b.WriteByte(digits[0])
	if len(digits) > 1 {
		b.WriteByte('.')
		b.WriteString(digits[1:])
	}
	b.WriteByte('E')
	if exp10 >= 0 {
		b.WriteByte('+')
	}
	b.WriteString(strconv.Itoa(exp10))
	return b.String()
}

// string renders u in base 10 without using math/big.
func (u u128) string() string {
	if u.isZero() {
		return "0"
	}
	var buf [40]byte
	i := len(buf)
	for !u.isZero() {
		// Use the general 256/128 long division: the divisor is small enough
		// that Div64's precondition (hi < divisor) would otherwise fail.
		q256, r256 := u256FromU128(u).divMod(u256FromU128(u128From64(10)))
		q := u128{lo: q256.v[0], hi: q256.v[1]}
		r := r256.v[0]
		i--
		buf[i] = byte('0' + r)
		u = q
	}
	return string(buf[i:])
}
