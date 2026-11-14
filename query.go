package netsec

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ParseQuery decodes a raw query string exactly once. It rejects constructs
// that have more than one plausible interpretation: a raw "+", an unescaped
// "=" inside a component, an unescaped "#", raw ASCII whitespace or control
// bytes, and empty segments. Percent escapes may use either hex case here;
// CanonicalizeQuery rewrites them to uppercase.
//
// A segment without "=" is a null field. A segment with a trailing "=" is an
// empty, non-null value.
func ParseQuery(raw string) ([]Field, error) {
	if !utf8.ValidString(raw) {
		return nil, ErrInvalidUTF8
	}
	if raw == "" {
		return []Field{}, nil
	}

	segments := strings.Split(raw, "&")
	fields := make([]Field, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			return nil, ErrEmptyComponent
		}

		keyPart := segment
		valuePart := ""
		null := true
		if index := strings.IndexByte(segment, '='); index >= 0 {
			keyPart = segment[:index]
			valuePart = segment[index+1:]
			null = false
		}

		key, err := decodeQueryComponent(keyPart)
		if err != nil {
			return nil, err
		}
		field := Field{Key: key, Null: null}
		if !null {
			value, err := decodeQueryComponent(valuePart)
			if err != nil {
				return nil, err
			}
			field.Value = value
		}
		fields = append(fields, field)
	}
	return fields, nil
}

// CanonicalizeQuery decodes raw exactly once, then applies the canonical
// ordering and escaping rules. Equivalent legal spellings such as "%2f" and
// "%2F" collapse to the same canonical bytes. A literal "%20" value
// (encoded as "%2520") remains distinct from a space (encoded as "%20").
func CanonicalizeQuery(raw string) ([]byte, error) {
	fields, err := ParseQuery(raw)
	if err != nil {
		return nil, err
	}
	return CanonicalizeFields(fields)
}

// CanonicalizeQueryBytes is CanonicalizeQuery for byte input.
func CanonicalizeQueryBytes(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) {
		return nil, ErrInvalidUTF8
	}
	return CanonicalizeQuery(string(raw))
}

func decodeQueryComponent(component string) (string, error) {
	decoded := make([]byte, 0, len(component))
	for index := 0; index < len(component); index++ {
		current := component[index]
		switch {
		case current == '%':
			if index+2 >= len(component) {
				return "", ErrMalformedEscape
			}
			high, ok := hexValue(component[index+1])
			if !ok {
				return "", fmt.Errorf("%w: %q", ErrMalformedEscape, component[index:])
			}
			low, ok := hexValue(component[index+2])
			if !ok {
				return "", fmt.Errorf("%w: %q", ErrMalformedEscape, component[index:])
			}
			decoded = append(decoded, high<<4|low)
			index += 2
		case current == '+':
			return "", ErrAmbiguousPlus
		case current == '=' || current == '#':
			return "", fmt.Errorf("%w: %q", ErrAmbiguousSeparator, current)
		case current <= 0x20 || current == 0x7F:
			return "", fmt.Errorf("%w: raw byte 0x%02X", ErrIllegalCharacter, current)
		default:
			decoded = append(decoded, current)
		}
	}

	result := string(decoded)
	if err := validateComponent("query component", result); err != nil {
		return "", err
	}
	return result, nil
}

func hexValue(value byte) (byte, bool) {
	switch {
	case '0' <= value && value <= '9':
		return value - '0', true
	case 'a' <= value && value <= 'f':
		return value - 'a' + 10, true
	case 'A' <= value && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}
