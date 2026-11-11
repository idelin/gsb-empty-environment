package netsec

import (
	"encoding/hex"
	"strings"
)

// parseMAC parses an EUI-48 address in the canonical aa:bb:cc:dd:ee:ff form
// and returns its canonical lowercase representation.
func parseMAC(s string) (MAC, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 6 {
		return "", ErrInvalidMAC
	}
	raw := make([]byte, 6)
	for i, part := range parts {
		if len(part) != 2 {
			return "", ErrInvalidMAC
		}
		b, err := hex.DecodeString(part)
		if err != nil {
			return "", ErrInvalidMAC
		}
		raw[i] = b[0]
	}
	return MAC(encodeMAC(raw)), nil
}

// encodeMAC renders six bytes as canonical lowercase colon hex.
func encodeMAC(raw []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, 17)
	for i, b := range raw {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, hexdigits[b>>4], hexdigits[b&0x0f])
	}
	return string(out)
}

// ParseMAC validates and canonicalizes a MAC address string.
func ParseMAC(s string) (MAC, error) {
	return parseMAC(s)
}
