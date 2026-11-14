package netsec

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// CallbackParam is the default query parameter used for the JSONP callback.
	CallbackParam = "callback"

	// DefaultMaxCallbackLength is the default maximum callback name length.
	DefaultMaxCallbackLength = 64

	// CallbackCharset describes the only characters accepted in a callback.
	CallbackCharset = "A-Za-z0-9_$"
)

var (
	// ErrMissingCallback reports that no callback query parameter was supplied.
	ErrMissingCallback = errors.New("netsec: missing JSONP callback")

	// ErrInvalidCallback reports a callback that is not a single safe identifier.
	ErrInvalidCallback = errors.New("netsec: invalid JSONP callback")

	// ErrCallbackTooLong reports a callback longer than the configured limit.
	ErrCallbackTooLong = errors.New("netsec: JSONP callback is too long")

	// ErrReservedCallback reports a JavaScript reserved word used as callback.
	ErrReservedCallback = errors.New("netsec: JSONP callback is a reserved word")
)

// CallbackPattern matches a single JavaScript identifier segment made only of
// ASCII letters, digits, underscore, and dollar. It deliberately does not
// match dots, brackets, parentheses, quotes, backslashes, or whitespace.
var CallbackPattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// JavaScript words that are not valid identifiers in the position where the
// JSONP wrapper is emitted. A few contextual keywords are included as well so
// that the generated script is valid under both classic and module parsing.
var reservedCallbackWords = map[string]struct{}{
	"await":      {},
	"break":      {},
	"case":       {},
	"catch":      {},
	"class":      {},
	"const":      {},
	"continue":   {},
	"debugger":   {},
	"default":    {},
	"delete":     {},
	"do":         {},
	"else":       {},
	"enum":       {},
	"export":     {},
	"extends":    {},
	"false":      {},
	"finally":    {},
	"for":        {},
	"function":   {},
	"if":         {},
	"implements": {},
	"import":     {},
	"in":         {},
	"instanceof": {},
	"interface":  {},
	"let":        {},
	"new":        {},
	"null":       {},
	"package":    {},
	"private":    {},
	"protected":  {},
	"public":     {},
	"return":     {},
	"static":     {},
	"super":      {},
	"switch":     {},
	"this":       {},
	"throw":      {},
	"true":       {},
	"try":        {},
	"typeof":     {},
	"var":        {},
	"void":       {},
	"while":      {},
	"with":       {},
	"yield":      {},
}

// NormalizeCallback decodes any remaining percent-encoding, then validates the
// resulting name. Query parsers normally decode the value once; repeating the
// decoding here closes the double-encoding gap without ever accepting a name
// that contains a percent sign, backslash escape, or other unsafe character.
//
// An optional maxLen overrides DefaultMaxCallbackLength.
func NormalizeCallback(raw string, maxLen ...int) (string, error) {
	if raw == "" {
		return "", ErrMissingCallback
	}

	decoded := raw
	for i := 0; i < 8 && strings.Contains(decoded, "%"); i++ {
		next, err := percentDecode(decoded)
		if err != nil {
			return "", ErrInvalidCallback
		}
		if next == decoded {
			break
		}
		decoded = next
	}

	if err := ValidateCallback(decoded, maxLen...); err != nil {
		return "", err
	}
	return decoded, nil
}

// ValidateCallback validates an already-decoded callback name. The name must
// be one identifier segment, use the declared ASCII character set, and fit
// within the configured length.
//
// An optional maxLen overrides DefaultMaxCallbackLength.
func ValidateCallback(name string, maxLen ...int) error {
	if name == "" {
		return ErrMissingCallback
	}

	limit := callbackLengthLimit(maxLen...)
	if len(name) > limit {
		return ErrCallbackTooLong
	}
	if !utf8.ValidString(name) || !CallbackPattern.MatchString(name) {
		return ErrInvalidCallback
	}
	if _, reserved := reservedCallbackWords[name]; reserved {
		return ErrReservedCallback
	}
	return nil
}

// IsValidCallback reports whether name is an already-decoded, safe callback.
func IsValidCallback(name string, maxLen ...int) bool {
	return ValidateCallback(name, maxLen...) == nil
}

// ValidCallback reports whether raw, including any percent-encoded form, is a
// safe callback after normalization.
func ValidCallback(raw string, maxLen ...int) bool {
	_, err := NormalizeCallback(raw, maxLen...)
	return err == nil
}

func callbackLengthLimit(maxLen ...int) int {
	if len(maxLen) > 0 && maxLen[0] > 0 {
		return maxLen[0]
	}
	return DefaultMaxCallbackLength
}

// percentDecode decodes exactly one layer of percent escapes. Unlike
// url.QueryUnescape it leaves '+' untouched; '+' is not in the callback
// alphabet and will be rejected by validation.
func percentDecode(value string) (string, error) {
	var b strings.Builder
	b.Grow(len(value))

	for i := 0; i < len(value); i++ {
		if value[i] != '%' {
			b.WriteByte(value[i])
			continue
		}
		if i+2 >= len(value) {
			return "", ErrInvalidCallback
		}
		hi, ok := fromHex(value[i+1])
		if !ok {
			return "", ErrInvalidCallback
		}
		lo, ok := fromHex(value[i+2])
		if !ok {
			return "", ErrInvalidCallback
		}
		b.WriteByte(hi<<4 | lo)
		i += 2
	}
	return b.String(), nil
}

func fromHex(c byte) (byte, bool) {
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
