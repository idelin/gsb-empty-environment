package netsec

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

var (
	// ErrPathRejected reports a request path that cannot safely be matched or
	// forwarded. Callers should reject the request instead of falling back to
	// a different route.
	ErrPathRejected = errors.New("netsec: path rejected")

	// ErrInvalidRule reports an Ingress rule whose path or match kind is
	// invalid.
	ErrInvalidRule = errors.New("netsec: invalid rule")

	// ErrDuplicateRule reports two rules of the same kind that resolve to the
	// same canonical path.
	ErrDuplicateRule = errors.New("netsec: duplicate rule")
)

// CanonicalPath validates an escaped HTTP request path and returns the one
// canonical path that must be used for both route selection and forwarding.
//
// The input is the escaped path portion of the request target, without a
// query or fragment. In net/http terms, use URL.EscapedPath rather than
// URL.Path, because URL.Path has already decoded %2F.
//
// CanonicalPath percent-decodes the path once, rejects encoded separators and
// NUL bytes, removes dot segments, and collapses repeated slashes. The result
// is escaped again so it can safely be sent upstream. Matching rule paths are
// canonicalized the same way.
func CanonicalPath(rawPath string) (string, error) {
	if rawPath == "" {
		return "", rejectPath("empty path")
	}
	if rawPath[0] != '/' {
		return "", rejectPath("path must be absolute")
	}

	decoded, err := decodeRequestPath(rawPath)
	if err != nil {
		return "", err
	}
	if err := rejectNestedSeparators(decoded); err != nil {
		return "", err
	}

	cleaned := path.Clean(decoded)
	if cleaned == "." {
		cleaned = "/"
	}
	return escapePath(cleaned), nil
}

func decodeRequestPath(rawPath string) (string, error) {
	var decoded strings.Builder
	decoded.Grow(len(rawPath))

	for i := 0; i < len(rawPath); {
		c := rawPath[i]
		if c == '%' {
			if i+2 >= len(rawPath) {
				return "", rejectPath("truncated percent escape at offset %d", i)
			}
			v, ok := decodeHex(rawPath[i+1], rawPath[i+2])
			if !ok {
				return "", rejectPath("invalid percent escape %q at offset %d", rawPath[i:i+3], i)
			}
			switch v {
			case '/':
				return "", rejectPath("encoded slash at offset %d", i)
			case '\\':
				return "", rejectPath("encoded backslash at offset %d", i)
			case 0:
				return "", rejectPath("encoded NUL at offset %d", i)
			}
			decoded.WriteByte(v)
			i += 3
			continue
		}

		switch c {
		case 0:
			return "", rejectPath("NUL at offset %d", i)
		case '\\':
			return "", rejectPath("backslash at offset %d", i)
		case '?', '#':
			return "", rejectPath("query or fragment delimiter at offset %d", i)
		}

		decoded.WriteByte(c)
		i++
	}

	return decoded.String(), nil
}

// rejectNestedSeparators catches double-encoded separators such as %252F.
// They are harmless if every later component decodes exactly once, but
// rejecting them avoids depending on that assumption.
func rejectNestedSeparators(decoded string) error {
	probe := decoded
	for depth := 0; depth < 16; depth++ {
		if offset := encodedSeparatorOffset(probe); offset >= 0 {
			return rejectPath("nested encoded separator at offset %d", offset)
		}
		next, changed := decodeValidEscapes(probe)
		if !changed {
			return nil
		}
		probe = next
	}
	return rejectPath("too many encoding layers")
}

func encodedSeparatorOffset(s string) int {
	for i := 0; i+2 < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		v, ok := decodeHex(s[i+1], s[i+2])
		if ok && (v == '/' || v == '\\' || v == 0) {
			return i
		}
	}
	return -1
}

func decodeValidEscapes(s string) (string, bool) {
	var decoded strings.Builder
	decoded.Grow(len(s))
	changed := false

	for i := 0; i < len(s); {
		if s[i] == '%' && i+2 < len(s) {
			if v, ok := decodeHex(s[i+1], s[i+2]); ok {
				decoded.WriteByte(v)
				i += 3
				changed = true
				continue
			}
		}
		decoded.WriteByte(s[i])
		i++
	}

	return decoded.String(), changed
}

func escapePath(cleaned string) string {
	if cleaned == "/" {
		return "/"
	}

	var escaped strings.Builder
	escaped.Grow(len(cleaned))
	for i, segment := range strings.Split(cleaned, "/") {
		if i > 0 {
			escaped.WriteByte('/')
		}
		escaped.WriteString(url.PathEscape(segment))
	}
	return escaped.String()
}

func decodeHex(hi, lo byte) (byte, bool) {
	h, ok := hexValue(hi)
	if !ok {
		return 0, false
	}
	l, ok := hexValue(lo)
	if !ok {
		return 0, false
	}
	return h<<4 | l, true
}

func hexValue(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func rejectPath(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrPathRejected, fmt.Sprintf(format, args...))
}
