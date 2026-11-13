package netsec

import (
	"net"
	"strings"
)

// parseIPLenient reports whether value is accepted as an IP literal by the
// standard library's permissive parser. It exists solely so that ExternalName
// values written in loose notations that strict clients reject but resolvers
// may still honour are recognised as IP literals.
func parseIPLenient(value string) bool {
	if value == "" {
		return false
	}
	if net.ParseIP(value) != nil {
		return true
	}
	return looksLikeLooseIPv4(value)
}

// looksLikeLooseIPv4 recognises dotted-quad spellings that the strict
// grammar rejects but some resolver/client libraries still interpret:
// octal (0177.0.0.1), hexadecimal (0x7f.0.0.1) and leading-zero decimals.
// It is deliberately only used to reject ExternalName IP literals.
func looksLikeLooseIPv4(value string) bool {
	if strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if !looseOctet(part) {
			return false
		}
	}
	return true
}

func looseOctet(token string) bool {
	base := 10
	body := token
	if strings.HasPrefix(token, "0x") || strings.HasPrefix(token, "0X") {
		base = 16
		body = token[2:]
	} else if len(token) > 1 && token[0] == '0' {
		base = 8
		body = token[1:]
	}
	if body == "" {
		return false
	}
	value := 0
	for _, r := range body {
		var digit int
		switch {
		case r >= '0' && r <= '9':
			digit = int(r - '0')
		case base == 16 && r >= 'a' && r <= 'f':
			digit = int(r-'a') + 10
		case base == 16 && r >= 'A' && r <= 'F':
			digit = int(r-'A') + 10
		default:
			return false
		}
		if digit >= base {
			return false
		}
		value = value*base + digit
		if value > 255 {
			return false
		}
	}
	return true
}
