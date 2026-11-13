package netsec

import "fmt"

// SerialRelation is the result of an RFC 1982 serial number comparison.
type SerialRelation int

const (
	// SerialLess means a is older than b.
	SerialLess SerialRelation = -1
	// SerialEqual means a and b are the same serial number.
	SerialEqual SerialRelation = 0
	// SerialGreater means a is newer than b.
	SerialGreater SerialRelation = 1
	// SerialUndefined means a and b differ by exactly 2^31, so RFC 1982
	// intentionally gives the comparison no defined result.
	SerialUndefined SerialRelation = 2
)

func (r SerialRelation) String() string {
	switch r {
	case SerialLess:
		return "less"
	case SerialEqual:
		return "equal"
	case SerialGreater:
		return "greater"
	case SerialUndefined:
		return "undefined"
	default:
		return fmt.Sprintf("SerialRelation(%d)", int(r))
	}
}

// CompareSerial compares two 32-bit DNS SOA serial numbers using RFC 1982
// serial number arithmetic.
//
// For a and b, d = (a - b) mod 2^32:
//   - d == 0: equal
//   - d < 2^31: a is greater than b
//   - d == 2^31: comparison is undefined
//   - d > 2^31: a is less than b
func CompareSerial(a, b uint32) SerialRelation {
	if a == b {
		return SerialEqual
	}

	d := a - b
	switch {
	case d < 1<<31:
		return SerialGreater
	case d == 1<<31:
		return SerialUndefined
	default:
		return SerialLess
	}
}

// SerialIsNewer reports whether a is strictly newer than b under RFC 1982.
// An undefined comparison is not treated as newer.
func SerialIsNewer(a, b uint32) bool {
	return CompareSerial(a, b) == SerialGreater
}
