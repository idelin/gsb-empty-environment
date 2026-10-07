package infer

import "strings"

// Classify reports the narrowest Kind that can losslessly represent a
// single value. The empty string is classified as Text, not as a null
// or missing value.
//
// The rules are:
//   - Bool: exactly "true" or "false".
//   - Int: an optionally negated base-10 integer in canonical form,
//     i.e. no leading zeros ("0" itself is allowed, "-0" is not, since
//     it would round-trip as "0"). There is no size limit: integers
//     beyond int64/uint64 range are still Int.
//   - Rational: a decimal "123.45" or fraction "22/7" in canonical
//     form: no leading zeros in the integer part, numerator, or
//     denominator, and a non-zero denominator. Any finite decimal or
//     fraction is exactly representable as a rational.
//   - Text: everything else, including "".
func Classify(s string) Kind {
	switch {
	case isBool(s):
		return Bool
	case isInt(s):
		return Int
	case isRational(s):
		return Rational
	default:
		return Text
	}
}

func isBool(s string) bool {
	return s == "true" || s == "false"
}

func isInt(s string) bool {
	body := strings.TrimPrefix(s, "-")
	if body == "" {
		return false
	}
	// "-0" would round-trip as "0", losing the sign, so it is text.
	if body == "0" && len(s) != len(body) {
		return false
	}
	return isCanonicalUint(body)
}

// isCanonicalUint reports whether s is a base-10 non-negative integer
// with no leading zeros: "0", "1", "42", ... but not "", "00", "007".
func isCanonicalUint(s string) bool {
	if s == "" {
		return false
	}
	if s == "0" {
		return true
	}
	if s[0] < '1' || s[0] > '9' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isRational(s string) bool {
	return isDecimal(s) || isFraction(s)
}

// isDecimal matches canonical decimals: -?UINT "." DIGITS, where UINT
// has no leading zeros and at least one digit follows the point.
// Trailing zeros in the fractional part are allowed because any finite
// decimal is exactly representable as a rational number.
func isDecimal(s string) bool {
	body := strings.TrimPrefix(s, "-")
	dot := strings.IndexByte(body, '.')
	if dot < 0 {
		return false
	}
	intPart, fracPart := body[:dot], body[dot+1:]
	if !isCanonicalUint(intPart) || fracPart == "" {
		return false
	}
	zero := intPart == "0"
	for i := 0; i < len(fracPart); i++ {
		if fracPart[i] < '0' || fracPart[i] > '9' {
			return false
		}
		if fracPart[i] != '0' {
			zero = false
		}
	}
	// A negated zero ("-0.0") would lose its sign, so it is text.
	if zero && len(s) != len(body) {
		return false
	}
	return true
}

// isFraction matches canonical fractions: -?UINT "/" UINT with a
// non-zero denominator and no leading zeros anywhere.
func isFraction(s string) bool {
	body := strings.TrimPrefix(s, "-")
	slash := strings.IndexByte(body, '/')
	if slash < 0 {
		return false
	}
	num, den := body[:slash], body[slash+1:]
	// A negated zero numerator ("-0/3") would lose its sign.
	if num == "0" && len(s) != len(body) {
		return false
	}
	return isCanonicalUint(num) && isCanonicalUint(den) && den != "0"
}
