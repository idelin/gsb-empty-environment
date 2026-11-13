package netsec

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxPeerIdentityBytes is the maximum accepted size of a peer identity.
// The limit applies both to the value supplied by the caller and to its
// RFC 4514-normalized form.
const MaxPeerIdentityBytes = 256

var (
	// ErrPeerIdentityTooLong indicates that a peer identity exceeds
	// MaxPeerIdentityBytes.
	ErrPeerIdentityTooLong = errors.New("peer identity exceeds 256 bytes")
	// ErrPeerIdentityControlCharacter indicates that a peer identity contains
	// a NUL or another control/format character.
	ErrPeerIdentityControlCharacter = errors.New("peer identity contains a control character")
	// ErrInvalidDistinguishedName indicates that a certificate subject could
	// not be parsed as an RFC 4514 distinguished name.
	ErrInvalidDistinguishedName = errors.New("invalid distinguished name")
)

// NormalizePeerIdentity validates a peer identity and returns the value that
// must be handed to a hook.
//
// FQDN and other non-DN identities are returned byte-for-byte unchanged. A
// certificate subject is recognized by an unescaped '=' and is reserialized
// with RFC 4514 escaping for every attribute value. NUL, other control
// characters, invalid UTF-8, and identities longer than
// MaxPeerIdentityBytes are rejected.
func NormalizePeerIdentity(raw string) (string, error) {
	if len(raw) > MaxPeerIdentityBytes {
		return "", fmt.Errorf("%w: got %d bytes", ErrPeerIdentityTooLong, len(raw))
	}
	if err := validateIdentityCharacters(raw); err != nil {
		return "", err
	}

	normalized := raw
	if looksLikeDistinguishedName(raw) {
		var err error
		normalized, err = normalizeDistinguishedName(raw)
		if err != nil {
			return "", err
		}
	}
	if len(normalized) > MaxPeerIdentityBytes {
		return "", fmt.Errorf("%w: normalized value is %d bytes", ErrPeerIdentityTooLong, len(normalized))
	}
	if err := validateIdentityCharacters(normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

// ValidatePeerIdentity checks an identity without retaining its normalized
// form. Call NormalizePeerIdentity when the value is needed for a hook.
func ValidatePeerIdentity(raw string) error {
	_, err := NormalizePeerIdentity(raw)
	return err
}

// EscapeDNComponent escapes one attribute value as required by RFC 4514.
// The caller is responsible for adding the attribute type, '=', and RDN
// separators.
func EscapeDNComponent(value string) string {
	if value == "" {
		return ""
	}

	var out strings.Builder
	last := len(value) - 1
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c == '\\', c == '"', c == '+', c == ',', c == ';', c == '<', c == '>':
			out.WriteByte('\\')
			out.WriteByte(c)
		case i == 0 && (c == '#' || c == ' '):
			out.WriteByte('\\')
			out.WriteByte(c)
		case i == last && c == ' ':
			out.WriteByte('\\')
			out.WriteByte(c)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&out, `\%02X`, c)
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

func validateIdentityCharacters(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: invalid UTF-8", ErrPeerIdentityControlCharacter)
	}
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return fmt.Errorf("%w: U+%04X", ErrPeerIdentityControlCharacter, r)
		}
	}
	return nil
}

func looksLikeDistinguishedName(value string) bool {
	_, _, ok := splitAttributeTypeAndValue(value)
	return ok
}

func normalizeDistinguishedName(raw string) (string, error) {
	rdns, err := splitUnescaped(raw, ',')
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidDistinguishedName, err)
	}

	var normalizedRDNs []string
	for _, rdn := range rdns {
		rdn = trimDNFormattingSpace(rdn)
		if rdn == "" {
			return "", fmt.Errorf("%w: empty RDN", ErrInvalidDistinguishedName)
		}
		avas, err := splitUnescaped(rdn, '+')
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidDistinguishedName, err)
		}

		var normalizedAVAs []string
		for _, ava := range avas {
			attributeType, rawValue, ok := splitAttributeTypeAndValue(ava)
			if !ok {
				return "", fmt.Errorf("%w: RDN component %q has no attribute type", ErrInvalidDistinguishedName, ava)
			}
			attributeType = strings.TrimSpace(attributeType)
			if !validAttributeType(attributeType) {
				return "", fmt.Errorf("%w: invalid attribute type %q", ErrInvalidDistinguishedName, attributeType)
			}
			value, err := decodeAttributeValue(rawValue)
			if err != nil {
				return "", fmt.Errorf("%w: %v", ErrInvalidDistinguishedName, err)
			}
			if err := validateIdentityCharacters(value); err != nil {
				return "", err
			}
			normalizedAVAs = append(normalizedAVAs, attributeType+"="+EscapeDNComponent(value))
		}
		normalizedRDNs = append(normalizedRDNs, strings.Join(normalizedAVAs, "+"))
	}
	return strings.Join(normalizedRDNs, ","), nil
}

func splitUnescaped(value string, separator byte) ([]string, error) {
	var parts []string
	start := 0
	quoted := false
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\\':
			if i+1 >= len(value) {
				return nil, errors.New("trailing escape")
			}
			i++
		case '"':
			quoted = !quoted
		case separator:
			if !quoted {
				parts = append(parts, value[start:i])
				start = i + 1
			}
		}
	}
	if quoted {
		return nil, errors.New("unterminated quoted value")
	}
	return append(parts, value[start:]), nil
}

func splitAttributeTypeAndValue(component string) (attributeType, rawValue string, ok bool) {
	quoted := false
	for i := 0; i < len(component); i++ {
		switch component[i] {
		case '\\':
			if i+1 >= len(component) {
				return "", "", false
			}
			i++
		case '"':
			quoted = !quoted
		case '=':
			if !quoted {
				return component[:i], component[i+1:], true
			}
		}
	}
	return "", "", false
}

func validAttributeType(value string) bool {
	if value == "" {
		return false
	}
	if value[0] == '#' {
		return false
	}

	if value[0] >= '0' && value[0] <= '9' {
		segmentHasDigit := false
		for i := 0; i < len(value); i++ {
			switch {
			case value[i] >= '0' && value[i] <= '9':
				segmentHasDigit = true
			case value[i] == '.':
				if !segmentHasDigit {
					return false
				}
				segmentHasDigit = false
			default:
				return false
			}
		}
		return segmentHasDigit
	}

	for i := 0; i < len(value); i++ {
		c := value[i]
		if i == 0 {
			if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
				return false
			}
			continue
		}
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') &&
			(c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

func decodeAttributeValue(raw string) (string, error) {
	quoted := false
	if strings.HasPrefix(raw, `"`) {
		quoted = true
		raw = raw[1:]
	}

	var out strings.Builder
	closedQuote := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch c {
		case '\\':
			if quoted && closedQuote {
				return "", errors.New("content after closing quote")
			}
			if i+1 >= len(raw) {
				return "", errors.New("trailing escape in attribute value")
			}
			if isHexDigit(raw[i+1]) {
				if i+2 >= len(raw) || !isHexDigit(raw[i+2]) {
					return "", fmt.Errorf("invalid hexadecimal escape at byte %d", i)
				}
				out.WriteByte(hexByte(raw[i+1], raw[i+2]))
				i += 2
				continue
			}
			if !isEscapable(raw[i+1]) {
				return "", fmt.Errorf("invalid escape sequence \\%c", raw[i+1])
			}
			out.WriteByte(raw[i+1])
			i++
		case '"':
			if !quoted {
				return "", errors.New("unescaped quote in attribute value")
			}
			if closedQuote {
				return "", errors.New("content after closing quote")
			}
			closedQuote = true
		default:
			if quoted && closedQuote {
				if !isASCIIWhitespace(c) {
					return "", errors.New("content after closing quote")
				}
			} else {
				out.WriteByte(c)
			}
		}
	}
	if quoted && !closedQuote {
		return "", errors.New("unterminated quoted attribute value")
	}
	return out.String(), nil
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'F') || (c >= 'a' && c <= 'f')
}

func hexByte(high, low byte) byte {
	return hexNibble(high)<<4 | hexNibble(low)
}

func hexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	default:
		return c - 'a' + 10
	}
}

func isEscapable(c byte) bool {
	switch c {
	case '\\', '"', '+', ',', ';', '<', '>', '=', '#', ' ':
		return true
	default:
		return false
	}
}

func isASCIIWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

func trimDNFormattingSpace(value string) string {
	start := 0
	for start < len(value) && isASCIIWhitespace(value[start]) {
		start++
	}

	end := len(value)
	for end > start && isASCIIWhitespace(value[end-1]) && !isEscapedAt(value, end-1) {
		end--
	}
	return value[start:end]
}

func isEscapedAt(value string, index int) bool {
	backslashes := 0
	for i := index - 1; i >= 0 && value[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}
