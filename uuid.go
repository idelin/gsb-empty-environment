package netsec

import (
	"fmt"
	"strings"
)

// UUID is a 16-byte RFC 4122 UUID in the byte order used by the btrfs send
// stream (A_UUID and A_CLONE_UUID attributes are raw UUID bytes).
type UUID [16]byte

// ParseUUID parses a canonical UUID with or without dashes.
func ParseUUID(s string) (UUID, error) {
	var out UUID
	raw := strings.ReplaceAll(s, "-", "")
	if len(raw) != 16*2 {
		return out, fmt.Errorf("netsec: invalid UUID %q", s)
	}
	for i := 0; i < len(out); i++ {
		hi, ok := hexNibble(raw[2*i])
		if !ok {
			return out, fmt.Errorf("netsec: invalid UUID %q", s)
		}
		lo, ok := hexNibble(raw[2*i+1])
		if !ok {
			return out, fmt.Errorf("netsec: invalid UUID %q", s)
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

// MustParseUUID is ParseUUID with a panic on invalid input. It is intended for
// tests and static configuration.
func MustParseUUID(s string) UUID {
	u, err := ParseUUID(s)
	if err != nil {
		panic(err)
	}
	return u
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// IsZero reports whether u is the all-zero UUID.
func (u UUID) IsZero() bool {
	return u == UUID{}
}

// String returns the canonical hyphenated representation of u.
func (u UUID) String() string {
	const groups = "0123456789abcdef"
	raw := make([]byte, 0, 16*2)
	for _, b := range u {
		raw = append(raw, groups[b>>4], groups[b&0x0f])
	}
	return string(raw[0:8]) + "-" + string(raw[8:12]) + "-" +
		string(raw[12:16]) + "-" + string(raw[16:20]) + "-" +
		string(raw[20:32])
}
