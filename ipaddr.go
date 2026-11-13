package netsec

import (
	"net/netip"
	"strings"
)

// parseStrictIP parses a literal IPv4 or IPv6 address using a strict textual
// grammar.
//
// Unlike net.ParseIP, it rejects:
//   - octal notation (010.0.0.1), hexadecimal notation (0x7f.0.0.1) and any
//     IPv4 octet with a leading zero (0177.0.0.1);
//   - non-dotted or non-canonical IPv4 forms (bare integers, 1.2.3);
//   - IPv6 addresses with a zone identifier.
//
// IPv4-mapped IPv6 addresses of the form ::ffff:a.b.c.d are reduced to the
// embedded IPv4 address (via Addr.Unmap) before further evaluation, so that
// an attacker cannot smuggle an IPv4 address through the mapped form.
func parseStrictIP(literal string) (netip.Addr, bool) {
	if literal == "" || strings.ContainsAny(literal, " \t\r\n") {
		return netip.Addr{}, false
	}
	// Zone identifiers are never valid for the addresses under policy.
	if strings.Contains(literal, "%") {
		return netip.Addr{}, false
	}

	switch {
	case strings.Contains(literal, "."):
		if strings.Contains(literal, ":") {
			// A dotted address inside an IPv6 literal is only accepted in the
			// standard mapped form ::ffff:a.b.c.d. The embedded IPv4 portion
			// must itself pass the strict IPv4 grammar, then the address is
			// reduced to IPv4.
			addr, err := netip.ParseAddr(literal)
			if err != nil || !addr.Is6() || !addr.Is4In6() {
				return netip.Addr{}, false
			}
			mapped := addr.Unmap()
			if !mapped.Is4() {
				return netip.Addr{}, false
			}
			return mapped, true
		}
		// Plain IPv4.
		return parseStrictIPv4(literal)
	case strings.Contains(literal, ":"):
		// Plain IPv6 (no embedded dotted form). netip already enforces the
		// canonical hex grammar; leading zeros inside hex groups are legal
		// for IPv6 (they are fixed-width hex, unlike IPv4 decimals).
		addr, err := netip.ParseAddr(literal)
		if err != nil || !addr.Is6() {
			return netip.Addr{}, false
		}
		// Unmap is a no-op for genuine IPv6 addresses.
		return addr.Unmap(), true
	default:
		// A bare integer or anything else is not a valid IP literal.
		return netip.Addr{}, false
	}
}

// parseStrictIPv4 parses a dotted-decimal IPv4 address. Every octet must be
// 1-3 decimal digits; "0" alone is allowed, but any multi-digit octet with a
// leading zero is rejected, and values above 255 are rejected.
func parseStrictIPv4(literal string) (netip.Addr, bool) {
	parts := strings.Split(literal, ".")
	if len(parts) != 4 {
		return netip.Addr{}, false
	}

	var octets [4]byte
	for i, part := range parts {
		if len(part) == 0 || len(part) > 3 {
			return netip.Addr{}, false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return netip.Addr{}, false
			}
		}
		if len(part) > 1 && part[0] == '0' {
			return netip.Addr{}, false
		}
		value := 0
		for _, r := range part {
			value = value*10 + int(r-'0')
		}
		if value > 255 {
			return netip.Addr{}, false
		}
		octets[i] = byte(value)
	}

	return netip.AddrFrom4(octets), true
}

// looksLikeIPLiteral reports whether the string could be interpreted as an IP
// literal by a permissive resolver. It first applies the strict grammar; if
// that fails it falls back to net.ParseIP so that loose forms that some
// libraries still accept (leading-zero, hex or octal notation) are also
// caught when validating ExternalName.
func looksLikeIPLiteral(value string) bool {
	if _, ok := parseStrictIP(value); ok {
		return true
	}
	return parseIPLenient(value)
}
