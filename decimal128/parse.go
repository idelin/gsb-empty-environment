package decimal128

import "strings"

// Parse parses a decimal literal using the default half-away-from-zero
// rounding when more than 38 significant digits are supplied.
func Parse(s string) (Decimal128, error) {
	return DefaultContext.Parse(s)
}

// Parse parses a decimal literal. Accepted grammar:
//
//	[+-]? (digits ['.' digits]? | '.' digits) [eE [+-]? digits]?
//
// Leading zeros, scientific notation and trailing zeros are all handled.
// Negative zero ("-0", "-0.000", "-0e5") is normalized to unsigned zero.
// More than 38 significant digits round with the context; an integer part
// that cannot fit in 38 digits fails with ErrOverflow.
func (c Context) Parse(s string) (Decimal128, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Decimal128{}, ErrInvalidSyntax
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
		return Decimal128{}, ErrInvalidSyntax
	}

	exp := 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		var err error
		exp, err = parseSignedInt(s[i+1:])
		if err != nil {
			return Decimal128{}, ErrInvalidSyntax
		}
		s = s[:i]
	}

	intPart, fracPart, ok := splitPoint(s)
	if !ok {
		return Decimal128{}, ErrInvalidSyntax
	}

	// Leading zeros do not change the numeric value; the fractional length
	// together with the exponent fixes the unscaled scale.
	digits := intPart + fracPart
	first := strings.TrimLeft(digits, "0")
	scale0 := len(fracPart) - exp

	if len(first) == 0 {
		// All-zero mantissa: normalize negative zero and keep the declared
		// scale when a decimal point was present.
		scale := scale0
		if scale < 0 {
			scale = 0
		}
		if scale > MaxPrecision {
			return Decimal128{}, ErrOverflow
		}
		return Decimal128{scale: scale, prec: max6(1, scale)}, nil
	}

	// Keep up to 38 significant digits; remember the first discarded digit
	// and whether any later discarded digit is non-zero for exact rounding.
	keep := first
	dropped := 0
	var droppedFirst byte
	droppedNonZero := false
	if len(keep) > MaxPrecision {
		dropped = len(keep) - MaxPrecision
		droppedFirst = keep[MaxPrecision]
		for _, ch := range keep[MaxPrecision+1:] {
			if ch != '0' {
				droppedNonZero = true
				break
			}
		}
		keep = keep[:MaxPrecision]
	}

	mag := u128{}
	for i := 0; i < len(keep); i++ {
		ch := keep[i]
		if ch < '0' || ch > '9' {
			return Decimal128{}, ErrInvalidSyntax
		}
		var carry uint64
		mag, carry = mag.mulSmall(10, uint64(ch-'0'))
		if carry != 0 {
			return Decimal128{}, ErrOverflow
		}
	}

	if droppedFirst != 0 || droppedNonZero {
		up := false
		switch {
		case droppedFirst > '5' || (droppedFirst == '5' && droppedNonZero):
			up = true
		case droppedFirst == '5':
			switch c.Mode {
			case HalfAwayFromZero:
				up = true
			case HalfToEven:
				up = mag.lo&1 == 1
			}
		}
		if up {
			mag, _ = mag.add(u128{lo: 1}, 0)
			if mag.digits() > MaxPrecision {
				return Decimal128{}, ErrOverflow
			}
		}
	}

	// scale0 is the point position measured from the end of the full
	// significand. Each dropped digit removes one position from that tail;
	// a negative result means trailing integer zeros from a positive exponent.
	scale := scale0 - dropped
	if scale < 0 {
		// A negative scale means trailing integer zeros from the exponent.
		m256 := mag.to256().mul(pow10u256[-scale])
		if !m256.fits128() {
			return Decimal128{}, ErrOverflow
		}
		mag = m256.as128()
		scale = 0
	}

	if scale > MaxPrecision || mag.cmp(limitForPrecision(MaxPrecision)) >= 0 {
		return Decimal128{}, ErrOverflow
	}
	if mag.isZero() {
		negative = false
	}
	prec := precisionFor(fromMag(mag, negative), scale)
	return finish(fromMag(mag, negative), prec, scale)
}

func parseSignedInt(s string) (int, error) {
	if s == "" {
		return 0, ErrInvalidSyntax
	}
	neg := false
	switch s[0] {
	case '+':
		s = s[1:]
	case '-':
		neg = true
		s = s[1:]
	}
	if s == "" {
		return 0, ErrInvalidSyntax
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, ErrInvalidSyntax
		}
		n = n*10 + int(s[i]-'0')
		if n > 1000 {
			return 0, ErrInvalidSyntax
		}
	}
	if neg {
		return -n, nil
	}
	return n, nil
}

func splitPoint(s string) (string, string, bool) {
	i := strings.IndexByte(s, '.')
	if i < 0 {
		if !allDigits(s) {
			return "", "", false
		}
		return s, "", true
	}
	left, right := s[:i], s[i+1:]
	if strings.IndexByte(right, '.') >= 0 || !allDigits(left) || !allDigits(right) || (left == "" && right == "") {
		return "", "", false
	}
	return left, right, true
}

func allDigits(s string) bool {
	if s == "" {
		return true
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
