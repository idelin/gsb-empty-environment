package decimal128

import "strings"

// String renders the value in fixed-point notation. Trailing zeros are
// padded to exactly Scale() digits after the decimal point; integer values
// (scale 0) carry no decimal point. Negative zero is never emitted.
func (d Decimal128) String() string {
	mag := d.coeff.mag()
	digits := u128ToString(mag)

	var b strings.Builder
	if d.Sign() < 0 {
		b.WriteByte('-')
	}
	switch {
	case d.scale == 0:
		if digits == "" {
			b.WriteByte('0')
		} else {
			b.WriteString(digits)
		}
	case d.scale >= len(digits):
		// 0.00... form: pad enough zeros after the point.
		b.WriteString("0.")
		b.WriteString(strings.Repeat("0", d.scale-len(digits)))
		if digits != "" {
			b.WriteString(digits)
		}
	default:
		split := len(digits) - d.scale
		b.WriteString(digits[:split])
		b.WriteByte('.')
		b.WriteString(digits[split:])
	}
	return b.String()
}

// StringScientific renders the value in normalized scientific notation
// d.dddE±nn with a single non-zero digit before the decimal point (zero
// renders as "0E+0").
func (d Decimal128) StringScientific() string {
	if d.IsZero() {
		return "0E+0"
	}
	mag := d.coeff.mag()
	digits := u128ToString(mag)
	exp := len(digits) - 1 - d.scale

	var b strings.Builder
	if d.Sign() < 0 {
		b.WriteByte('-')
	}
	b.WriteByte(digits[0])
	if len(digits) > 1 {
		b.WriteByte('.')
		b.WriteString(digits[1:])
	}
	b.WriteByte('E')
	if exp >= 0 {
		b.WriteByte('+')
	}
	b.WriteString(itoa(exp))
	return b.String()
}

// Rescale returns d rounded to the requested scale using the context mode.
// The declared precision is widened to 38 when the magnitude requires it.
func (c Context) Rescale(d Decimal128, scale int) (Decimal128, error) {
	if scale < 0 || scale > MaxPrecision {
		return Decimal128{}, ErrInvalidPrecision
	}
	v, err := c.rescaleTo(d.coeff, d.scale, scale)
	if err != nil {
		return Decimal128{}, err
	}
	p := precisionFor(v, scale)
	return finish(v, p, scale)
}

// u128ToString renders an unsigned 128-bit magnitude in base 10 via repeated
// single-limb division; zero yields the empty string (callers add "0").
func u128ToString(v u128) string {
	if v.isZero() {
		return ""
	}
	var buf [39]byte
	i := len(buf)
	for !v.isZero() {
		q, r := v.divSmall(10)
		i--
		buf[i] = byte('0' + r)
		v = q
	}
	return string(buf[i:])
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
