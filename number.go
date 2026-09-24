package canonicalkey

import (
	"strings"
)

func canonicalNumber(raw string, maxOutput int) (string, error) {
	if raw == "" {
		return "", ErrInvalidValue
	}
	pos := 0
	negative := false
	if raw[pos] == '-' {
		negative = true
		pos++
	}

	intPart := ""
	for pos < len(raw) && raw[pos] >= '0' && raw[pos] <= '9' {
		intPart += string(raw[pos])
		pos++
	}
	fracPart := ""
	if pos < len(raw) && raw[pos] == '.' {
		pos++
		start := pos
		for pos < len(raw) && raw[pos] >= '0' && raw[pos] <= '9' {
			fracPart += string(raw[pos])
			pos++
		}
		if pos == start {
			return "", ErrInvalidValue
		}
	}
	if intPart == "" && fracPart == "" {
		return "", ErrInvalidValue
	}

	exponent := 0
	if pos < len(raw) {
		if raw[pos] != 'e' && raw[pos] != 'E' {
			return "", ErrInvalidValue
		}
		pos++
		negativeExponent := false
		if pos < len(raw) && (raw[pos] == '+' || raw[pos] == '-') {
			negativeExponent = raw[pos] == '-'
			pos++
		}
		start := pos
		for pos < len(raw) && raw[pos] >= '0' && raw[pos] <= '9' {
			if exponent <= maxOutput {
				digit := int(raw[pos] - '0')
				exponent = exponent*10 + digit
			}
			pos++
		}
		if pos == start {
			return "", ErrInvalidValue
		}
		if pos != len(raw) || exponent > maxOutput {
			return "", ErrBudgetExceeded
		}
		if negativeExponent {
			exponent = -exponent
		}
	}

	digits := intPart + fracPart
	leadingZeros := 0
	for leadingZeros < len(digits)-1 && digits[leadingZeros] == '0' {
		leadingZeros++
	}
	digits = digits[leadingZeros:]
	decimal := len(intPart) - leadingZeros + exponent

	if digits == "0" {
		return "0", nil
	}
	removableFractionalZeros := len(digits) - decimal
	if removableFractionalZeros < 0 {
		removableFractionalZeros = 0
	}
	if removableFractionalZeros > len(digits)-1 {
		removableFractionalZeros = len(digits) - 1
	}
	trailingZeros := 0
	for trailingZeros < removableFractionalZeros && digits[len(digits)-1-trailingZeros] == '0' {
		trailingZeros++
	}
	digits = digits[:len(digits)-trailingZeros]

	var result string
	switch {
	case decimal <= 0:
		result = "0." + strings.Repeat("0", -decimal) + digits
	case decimal >= len(digits):
		result = digits + strings.Repeat("0", decimal-len(digits))
	default:
		result = digits[:decimal] + "." + digits[decimal:]
	}
	if negative {
		result = "-" + result
	}
	if len(result) > maxOutput {
		return "", ErrBudgetExceeded
	}
	return result, nil
}
