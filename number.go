package canonkey

import (
	"fmt"
	"strconv"
	"strings"
)

// canonNumber normalizes a decimal number literal to a unique, exact
// scientific form: [-]<digits>e<exp>, where digits has no leading or
// trailing zeros and value = digits * 10^exp. Zero normalizes to "0".
// The representation is exact: no rounding, no precision loss.
func canonNumber(lit string) (string, error) {
	i := 0
	neg := false
	if i < len(lit) && (lit[i] == '-' || lit[i] == '+') {
		neg = lit[i] == '-'
		i++
	}
	intStart := i
	for i < len(lit) && isDigit(lit[i]) {
		i++
	}
	intDigits := lit[intStart:i]
	if intDigits == "" {
		return "", fmt.Errorf("%w: %q", ErrInvalidNumber, lit)
	}
	fracDigits := ""
	if i < len(lit) && lit[i] == '.' {
		i++
		fs := i
		for i < len(lit) && isDigit(lit[i]) {
			i++
		}
		fracDigits = lit[fs:i]
		if fracDigits == "" {
			return "", fmt.Errorf("%w: %q", ErrInvalidNumber, lit)
		}
	}
	var exp int64
	if i < len(lit) && (lit[i] == 'e' || lit[i] == 'E') {
		i++
		es := i
		if i < len(lit) && (lit[i] == '-' || lit[i] == '+') {
			i++
		}
		ds := i
		for i < len(lit) && isDigit(lit[i]) {
			i++
		}
		if ds == i || i-ds > 15 {
			return "", fmt.Errorf("%w: %q", ErrInvalidNumber, lit)
		}
		e, err := strconv.ParseInt(lit[es:i], 10, 64)
		if err != nil {
			return "", fmt.Errorf("%w: %q", ErrInvalidNumber, lit)
		}
		exp = e
	}
	if i != len(lit) {
		return "", fmt.Errorf("%w: %q", ErrInvalidNumber, lit)
	}

	digits := intDigits + fracDigits
	e := exp - int64(len(fracDigits))
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return "0", nil
	}
	trimmed := strings.TrimRight(digits, "0")
	e += int64(len(digits) - len(trimmed))
	digits = trimmed

	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	b.WriteString(digits)
	b.WriteByte('e')
	b.WriteString(strconv.FormatInt(e, 10))
	return b.String(), nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
