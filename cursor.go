package netsec

import (
	"errors"
	"strconv"
)

// ErrMalformedCursor is returned when a subscription cursor is not a strict
// decimal unsigned integer. Acceptable inputs are "0" and decimal strings that
// start with a non-zero digit. Empty strings, signs, leading zeros, whitespace,
// hexadecimal ("0x..") and other-base notations, and values that overflow
// uint64 are all rejected.
var ErrMalformedCursor = errors.New("netsec: malformed cursor")

// ParseCursor validates and decodes a subscription cursor.
//
// The grammar accepted on purpose is narrower than strconv.ParseUint: only
// canonical decimal unsigned integers are allowed, so
//
//	"0", "1", "42", "18446744073709551615"   -> accepted
//	"", "+1", "-1", "00", "01", " 1", "0x1f", "1_0", "ff" -> rejected
//
// Callers must treat any error as an invalid handshake parameter rather than
// coercing the value to zero.
func ParseCursor(s string) (uint64, error) {
	if s == "" {
		return 0, ErrMalformedCursor
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, ErrMalformedCursor
		}
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, ErrMalformedCursor
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		// Only reachable for an overflowing run of digits.
		return 0, ErrMalformedCursor
	}
	return v, nil
}
