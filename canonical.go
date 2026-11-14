package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The canonical form is a minimal, unambiguous query string:
//
//	field    = escaped-key "=" escaped-value
//	null     = escaped-key
//	document = field *("&" field)
//
// Fields are ordered by the UTF-8 bytes of their unescaped key. Escaping uses
// the RFC 3986 unreserved set (ALPHA / DIGIT / "-" / "." / "_" / "~"). Every
// other UTF-8 byte is written as an uppercase "%HH" escape. In particular,
// space is always "%20", "+" is always "%2B", and a literal percent sign is
// always "%25". There is exactly one spelling for every value.

var (
	// ErrDuplicateKey is returned when a key occurs more than once.
	ErrDuplicateKey = errors.New("netsec: duplicate key")
	// ErrEmptyKey is returned when a key is empty.
	ErrEmptyKey = errors.New("netsec: empty key")
	// ErrInvalidUTF8 is returned for bytes that are not valid UTF-8.
	ErrInvalidUTF8 = errors.New("netsec: invalid UTF-8")
	// ErrIllegalCharacter is returned for NUL, control/format characters, a
	// BOM, Unicode line separators, or Unicode noncharacters.
	ErrIllegalCharacter = errors.New("netsec: illegal character")
	// ErrNullWithValue is returned for a null field carrying a value.
	ErrNullWithValue = errors.New("netsec: null field has a value")
	// ErrMalformedEscape is returned for an incomplete or invalid %HH escape.
	ErrMalformedEscape = errors.New("netsec: malformed percent escape")
	// ErrAmbiguousPlus is returned when a raw query contains "+". The plus
	// character is ambiguous between a literal plus and a form-encoded space,
	// so it is never accepted as input.
	ErrAmbiguousPlus = errors.New("netsec: ambiguous '+' in query")
	// ErrAmbiguousSeparator is returned when unescaped "=" or "#" occurs in a
	// query component.
	ErrAmbiguousSeparator = errors.New("netsec: unescaped query separator")
	// ErrEmptyComponent is returned for empty query segments such as "a=1&&b=2".
	ErrEmptyComponent = errors.New("netsec: empty query component")
	// ErrNonCanonical is returned when input is not already in canonical form.
	ErrNonCanonical = errors.New("netsec: non-canonical encoding")
	// ErrUnsupportedInput is returned for an unsupported input type.
	ErrUnsupportedInput = errors.New("netsec: unsupported input type")
)

// Field is one logical key/value pair. A null field is represented by a bare
// key in canonical form, while a non-null empty value is represented by
// "key=". These two states are deliberately distinct and never inferred from
// one another.
type Field struct {
	Key   string
	Value string
	Null  bool
}

// Params is a convenient map form for callers that do not need null fields.
type Params map[string]string

// Values is an alias for url.Values.
type Values = url.Values

// Fields is a slice form that also permits null fields and explicit ordering
// before canonicalization.
type Fields []Field

// Canonicalize returns the unique canonical byte representation of input.
//
// Accepted inputs are []Field, Fields, Field, map[string]string, Params,
// map[string]*string, url.Values, map[string][]string, string, and []byte. A
// string or []byte is interpreted as a raw query string and decoded exactly
// once before canonicalization.
//
// Map input is never trusted for ordering. Slice and url.Values input is
// checked for duplicate keys.
func Canonicalize(input any) ([]byte, error) {
	switch value := input.(type) {
	case nil:
		return []byte{}, nil
	case []Field:
		return CanonicalizeFields(value)
	case Fields:
		return CanonicalizeFields([]Field(value))
	case Field:
		return CanonicalizeFields([]Field{value})
	case map[string]string:
		return CanonicalizeMap(value)
	case Params:
		return CanonicalizeMap(map[string]string(value))
	case map[string]*string:
		fields := make([]Field, 0, len(value))
		for key, item := range value {
			if item == nil {
				fields = append(fields, Field{Key: key, Null: true})
				continue
			}
			fields = append(fields, Field{Key: key, Value: *item})
		}
		return CanonicalizeFields(fields)
	case map[string]any:
		fields := make([]Field, 0, len(value))
		for key, item := range value {
			switch item := item.(type) {
			case nil:
				fields = append(fields, Field{Key: key, Null: true})
			case string:
				fields = append(fields, Field{Key: key, Value: item})
			case *string:
				if item == nil {
					fields = append(fields, Field{Key: key, Null: true})
					continue
				}
				fields = append(fields, Field{Key: key, Value: *item})
			default:
				return nil, fmt.Errorf("%w: %q has value type %T", ErrUnsupportedInput, key, item)
			}
		}
		return CanonicalizeFields(fields)
	case url.Values:
		return CanonicalizeValues(value)
	case map[string][]string:
		return CanonicalizeValues(url.Values(value))
	case string:
		return CanonicalizeQuery(value)
	case []byte:
		return CanonicalizeQueryBytes(value)
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedInput, input)
	}
}

// CanonicalBytes is an alias for Canonicalize.
func CanonicalBytes(input any) ([]byte, error) {
	return Canonicalize(input)
}

// Canonical is an alias for Canonicalize.
func Canonical(input any) ([]byte, error) {
	return Canonicalize(input)
}

// Normalize is an alias for Canonicalize.
func Normalize(input any) ([]byte, error) {
	return Canonicalize(input)
}

// NormalizeQuery is an alias for CanonicalizeQuery.
func NormalizeQuery(raw string) ([]byte, error) {
	return CanonicalizeQuery(raw)
}

// CanonicalizeStrict returns canonical bytes without accepting an alternate
// spelling for string or []byte input. Structured input is canonicalized
// normally because it has no encoding to reinterpret.
func CanonicalizeStrict(input any) ([]byte, error) {
	switch value := input.(type) {
	case string:
		if err := validateCanonicalBytes([]byte(value)); err != nil {
			return nil, err
		}
		return []byte(value), nil
	case []byte:
		if err := validateCanonicalBytes(value); err != nil {
			return nil, err
		}
		return append([]byte(nil), value...), nil
	default:
		return Canonicalize(input)
	}
}

// CanonicalString is Canonicalize with a string result.
func CanonicalString(input any) (string, error) {
	canonical, err := Canonicalize(input)
	if err != nil {
		return "", err
	}
	return string(canonical), nil
}

// CanonicalizeFields canonicalizes explicit logical fields.
func CanonicalizeFields(fields []Field) ([]byte, error) {
	if len(fields) == 0 {
		return []byte{}, nil
	}

	ordered := make([]Field, len(fields))
	copy(ordered, fields)

	for _, field := range ordered {
		if field.Key == "" {
			return nil, ErrEmptyKey
		}
		if field.Null && field.Value != "" {
			return nil, fmt.Errorf("%w: %q", ErrNullWithValue, field.Key)
		}
		if err := validateComponent("key", field.Key); err != nil {
			return nil, err
		}
		if !field.Null {
			if err := validateComponent("value", field.Value); err != nil {
				return nil, err
			}
		}
	}

	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].Key < ordered[j].Key
	})
	for i := 1; i < len(ordered); i++ {
		if ordered[i].Key == ordered[i-1].Key {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateKey, ordered[i].Key)
		}
	}

	var out bytes.Buffer
	out.Grow(len(fields) * 8)
	for i, field := range ordered {
		if i > 0 {
			out.WriteByte('&')
		}
		writeEscaped(&out, field.Key)
		if !field.Null {
			out.WriteByte('=')
			writeEscaped(&out, field.Value)
		}
	}
	return out.Bytes(), nil
}

// CanonicalizeMap canonicalizes a map of non-null string values.
func CanonicalizeMap(params map[string]string) ([]byte, error) {
	fields := make([]Field, 0, len(params))
	for key, value := range params {
		fields = append(fields, Field{Key: key, Value: value})
	}
	return CanonicalizeFields(fields)
}

// CanonicalizeParams is an alias for CanonicalizeMap.
func CanonicalizeParams(params map[string]string) ([]byte, error) {
	return CanonicalizeMap(params)
}

// CanonicalizeValues canonicalizes url.Values. A key with no values is null;
// a key with exactly one value is non-null; a key with multiple values is a
// duplicate and is rejected.
func CanonicalizeValues(values url.Values) ([]byte, error) {
	fields := make([]Field, 0, len(values))
	for key, items := range values {
		switch len(items) {
		case 0:
			fields = append(fields, Field{Key: key, Null: true})
		case 1:
			fields = append(fields, Field{Key: key, Value: items[0]})
		default:
			return nil, fmt.Errorf("%w: %q has %d values", ErrDuplicateKey, key, len(items))
		}
	}
	return CanonicalizeFields(fields)
}

// EscapeComponent escapes one logical key or value using the canonical rules.
func EscapeComponent(component string) (string, error) {
	if err := validateComponent("component", component); err != nil {
		return "", err
	}
	var out strings.Builder
	out.Grow(len(component))
	writeEscaped(&out, component)
	return out.String(), nil
}

// ValidateCanonical checks that input is already byte-for-byte canonical.
// Strings and byte slices are parsed strictly; structured inputs are checked
// by canonicalizing them. Re-encoding a canonical document (for example,
// changing %2F to %2f or to "/") fails this check.
func ValidateCanonical(input any) error {
	switch value := input.(type) {
	case string:
		return validateCanonicalBytes([]byte(value))
	case []byte:
		return validateCanonicalBytes(value)
	case []Field, Fields, Field, map[string]string, Params, map[string]*string, map[string]any, url.Values, map[string][]string, nil:
		_, err := Canonicalize(input)
		return err
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedInput, input)
	}
}

// ValidateCanonicalQuery checks that a raw query string is already canonical.
func ValidateCanonicalQuery(raw string) error {
	return validateCanonicalBytes([]byte(raw))
}

func validateCanonicalBytes(raw []byte) error {
	if !utf8.Valid(raw) {
		return ErrInvalidUTF8
	}
	if len(raw) == 0 {
		return nil
	}

	fields := make([]Field, 0, bytes.Count(raw, []byte{'&'})+1)
	for _, segment := range bytes.Split(raw, []byte{'&'}) {
		if len(segment) == 0 {
			return fmt.Errorf("%w: empty segment", ErrEmptyComponent)
		}

		keyPart := segment
		valuePart := []byte(nil)
		null := true
		if index := bytes.IndexByte(segment, '='); index >= 0 {
			if bytes.IndexByte(segment[index+1:], '=') >= 0 {
				return fmt.Errorf("%w: repeated '='", ErrNonCanonical)
			}
			keyPart = segment[:index]
			valuePart = segment[index+1:]
			null = false
		}

		key, err := decodeCanonicalComponent(keyPart)
		if err != nil {
			return err
		}
		field := Field{Key: key, Null: null}
		if !null {
			value, err := decodeCanonicalComponent(valuePart)
			if err != nil {
				return err
			}
			field.Value = value
		}
		fields = append(fields, field)
	}

	reencoded, err := CanonicalizeFields(fields)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, reencoded) {
		return fmt.Errorf("%w: input differs from %q", ErrNonCanonical, reencoded)
	}
	return nil
}

func decodeCanonicalComponent(component []byte) (string, error) {
	decoded := make([]byte, 0, len(component))
	for i := 0; i < len(component); i++ {
		current := component[i]
		switch {
		case current == '%':
			if i+2 >= len(component) {
				return "", ErrMalformedEscape
			}
			high, ok := upperHexValue(component[i+1])
			if !ok {
				if _, isHex := hexValue(component[i+1]); isHex {
					return "", fmt.Errorf("%w: lowercase hex in %q", ErrNonCanonical, component[i:])
				}
				return "", fmt.Errorf("%w: invalid hex in %q", ErrMalformedEscape, component[i:])
			}
			low, ok := upperHexValue(component[i+2])
			if !ok {
				if _, isHex := hexValue(component[i+2]); isHex {
					return "", fmt.Errorf("%w: lowercase hex in %q", ErrNonCanonical, component[i:])
				}
				return "", fmt.Errorf("%w: invalid hex in %q", ErrMalformedEscape, component[i:])
			}
			value := high<<4 | low
			if isUnreserved(value) {
				return "", fmt.Errorf("%w: unnecessarily escaped byte %q", ErrNonCanonical, value)
			}
			decoded = append(decoded, value)
			i += 2
		case isUnreserved(current):
			decoded = append(decoded, current)
		default:
			return "", fmt.Errorf("%w: raw byte 0x%02X", ErrNonCanonical, current)
		}
	}

	result := string(decoded)
	if err := validateComponent("component", result); err != nil {
		return "", err
	}
	return result, nil
}

func upperHexValue(value byte) (byte, bool) {
	switch {
	case '0' <= value && value <= '9':
		return value - '0', true
	case 'A' <= value && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

func validateComponent(kind, component string) error {
	if !utf8.ValidString(component) {
		return fmt.Errorf("%w: %s", ErrInvalidUTF8, kind)
	}
	for _, character := range component {
		if isIllegalRune(character) {
			return fmt.Errorf("%w: %s contains U+%04X", ErrIllegalCharacter, kind, character)
		}
	}
	return nil
}

func isIllegalRune(character rune) bool {
	switch {
	case character == 0:
		return true
	case character == '\uFEFF':
		return true
	case 0xFDD0 <= character && character <= 0xFDEF:
		return true
	case character&0xFFFF == 0xFFFE || character&0xFFFF == 0xFFFF:
		return true
	case unicode.Is(unicode.Cf, character):
		return true
	case unicode.Is(unicode.Zl, character) || unicode.Is(unicode.Zp, character):
		return true
	default:
		return unicode.IsControl(character)
	}
}

func writeEscaped(out byteWriter, component string) {
	for i := 0; i < len(component); i++ {
		value := component[i]
		if isUnreserved(value) {
			out.WriteByte(value)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(upperHexDigits[value>>4])
		out.WriteByte(upperHexDigits[value&0x0F])
	}
}

type byteWriter interface {
	WriteByte(byte) error
}

const upperHexDigits = "0123456789ABCDEF"

func isUnreserved(value byte) bool {
	switch {
	case 'A' <= value && value <= 'Z':
		return true
	case 'a' <= value && value <= 'z':
		return true
	case '0' <= value && value <= '9':
		return true
	case value == '-', value == '.', value == '_', value == '~':
		return true
	default:
		return false
	}
}
