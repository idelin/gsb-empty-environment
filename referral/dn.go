package referral

import (
	"errors"
	"strings"
)

// dnWithin reports whether dn is equal to or subordinate to base. Both are
// compared as RFC 4514 distinguished names after unescaping. A dn that
// contains a ".." traversal segment (only meaningful once percent-encoding
// has been decoded) is always rejected.
func dnWithin(dn, base string) bool {
	dn = strings.TrimSpace(dn)
	base = strings.TrimSpace(base)
	if dn == "" || base == "" {
		return false
	}

	dnRDNs, err := parseDN(dn)
	if err != nil {
		return false
	}
	baseRDNs, err := parseDN(base)
	if err != nil {
		return false
	}
	if len(dnRDNs) < len(baseRDNs) {
		return false
	}

	// Compare the suffix of dn against base (RDN order runs leaf to root).
	offset := len(dnRDNs) - len(baseRDNs)
	for i, baseRDN := range baseRDNs {
		if !rdnEqual(dnRDNs[offset+i], baseRDN) {
			return false
		}
	}
	return true
}

// rdn is one relative distinguished name: a set of attribute/value pairs
// (multi-valued RDNs joined by '+').
type rdn struct {
	pairs []attrValue
}

type attrValue struct {
	attr  string
	value string
}

func parseDN(dn string) ([]rdn, error) {
	components := splitDNComponents(dn)
	out := make([]rdn, 0, len(components))
	for _, comp := range components {
		comp = strings.TrimSpace(comp)
		if comp == "" {
			return nil, errors.New("referral: empty RDN")
		}
		parts := splitEscaped(comp, '+')
		rdnVal := rdn{}
		for _, part := range parts {
			eq := strings.IndexByte(part, '=')
			if eq <= 0 {
				return nil, errors.New("referral: malformed RDN")
			}
			attr := strings.TrimSpace(strings.ToLower(part[:eq]))
			value := strings.TrimSpace(part[eq+1:])
			value = unescapeRDNValue(value)
			if attr == "" || value == "" {
				return nil, errors.New("referral: malformed RDN")
			}
			rdnVal.pairs = append(rdnVal.pairs, attrValue{attr: attr, value: value})
		}
		out = append(out, rdnVal)
	}
	return out, nil
}

// splitDNComponents splits a DN on unescaped commas.
func splitDNComponents(dn string) []string {
	return splitEscaped(dn, ',')
}

// splitEscaped splits s on sep while honoring RFC 4514 backslash escapes and
// double-quoted values.
func splitEscaped(s string, sep byte) []string {
	var parts []string
	var buf strings.Builder
	quoted := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '\\' && i+1 < len(s):
			buf.WriteByte(ch)
			buf.WriteByte(s[i+1])
			i++
		case ch == '"':
			quoted = !quoted
			buf.WriteByte(ch)
		case ch == sep && !quoted:
			parts = append(parts, buf.String())
			buf.Reset()
		default:
			buf.WriteByte(ch)
		}
	}
	parts = append(parts, buf.String())
	return parts
}

// unescapeRDNValue resolves RFC 4514 escapes (\, \+ \" \\ \< \> \= \; \#
// and two-hex-digit escapes), strips surrounding quotes, and rejects ".."
// traversal segments.
func unescapeRDNValue(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] != '\\' {
			b.WriteByte(v[i])
			continue
		}
		if i+2 < len(v) && isHex(v[i+1]) && isHex(v[i+2]) {
			b.WriteByte(decodeHex(v[i+1])<<4 | decodeHex(v[i+2]))
			i += 2
			continue
		}
		if i+1 < len(v) {
			b.WriteByte(v[i+1])
			i++
		}
	}

	decoded := b.String()
	if containsTraversal(decoded) {
		return ""
	}
	return decoded
}

func containsTraversal(v string) bool {
	for _, segment := range strings.FieldsFunc(v, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if segment == ".." {
			return true
		}
	}
	return false
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func decodeHex(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	default:
		return b - 'A' + 10
	}
}

func rdnEqual(a, b rdn) bool {
	if len(a.pairs) != len(b.pairs) {
		return false
	}
	for i := range a.pairs {
		if a.pairs[i].attr != b.pairs[i].attr ||
			!strings.EqualFold(a.pairs[i].value, b.pairs[i].value) {
			return false
		}
	}
	return true
}
