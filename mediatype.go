package netsec

import (
	"errors"
	"strings"
)

// ErrInvalidMediaType is returned by ParseMediaType when the Content-Type
// value does not parse as a valid MIME media type.
var ErrInvalidMediaType = errors.New("netsec: invalid media type")

// ParseMediaType parses a Content-Type header value such as
//
//	multipart/mixed; boundary="abc 123"
//
// It returns the lower-cased media type and a map of parameter names
// (lower-cased) to their values. Quoted parameter values are unquoted and
// backslash escapes are resolved per RFC 2045.
//
// Unlike mime.ParseMediaType it performs no character-set conversion and
// treats the value strictly: a malformed parameter or an unterminated quoted
// string produces ErrInvalidMediaType instead of a best-effort result.
func ParseMediaType(v string) (string, map[string]string, error) {
	params := make(map[string]string)

	semicolon := strings.IndexByte(v, ';')
	var mediaType, rest string
	if semicolon < 0 {
		mediaType, rest = strings.TrimSpace(v), ""
	} else {
		mediaType = strings.TrimSpace(v[:semicolon])
		rest = v[semicolon+1:]
	}
	if mediaType == "" {
		return "", nil, ErrInvalidMediaType
	}

	for rest != "" {
		rest = skipTSpaces(rest)
		if rest == "" {
			break
		}
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 {
			return "", nil, ErrInvalidMediaType
		}
		name := strings.TrimSpace(rest[:eq])
		if name == "" || !isToken(name) {
			return "", nil, ErrInvalidMediaType
		}
		value, leftover, err := readParamValue(rest[eq+1:])
		if err != nil {
			return "", nil, err
		}
		params[strings.ToLower(name)] = value

		rest = leftover
		if rest != "" {
			if rest[0] != ';' {
				return "", nil, ErrInvalidMediaType
			}
			rest = rest[1:]
		}
	}

	return strings.ToLower(mediaType), params, nil
}

// readParamValue reads either a quoted-string or a token parameter value.
// Quoted strings may contain linear whitespace and backslash escapes.
func readParamValue(s string) (value, leftover string, err error) {
	s = skipTSpaces(s)
	if s == "" {
		return "", "", ErrInvalidMediaType
	}

	if s[0] != '"' {
		end := 0
		for end < len(s) && s[end] != ';' {
			end++
		}
		raw := strings.TrimSpace(s[:end])
		if raw == "" || !isToken(raw) {
			return "", "", ErrInvalidMediaType
		}
		return raw, s[end:], nil
	}

	var b strings.Builder
	i := 1
	for i < len(s) {
		c := s[i]
		switch {
		case c == '"':
			tail := skipTSpaces(s[i+1:])
			if tail != "" && tail[0] != ';' {
				return "", "", ErrInvalidMediaType
			}
			return b.String(), tail, nil
		case c == '\\':
			i++
			if i >= len(s) {
				return "", "", ErrInvalidMediaType
			}
			b.WriteByte(s[i])
		case c == '\r' || c == '\n':
			return "", "", ErrInvalidMediaType
		default:
			b.WriteByte(c)
		}
		i++
	}
	return "", "", ErrInvalidMediaType
}

func skipTSpaces(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	return s
}

func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isTokenChar(s[i]) {
			return false
		}
	}
	return true
}

func isTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}
