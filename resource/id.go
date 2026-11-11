// Package resource provides a single immutable resource identifier shared by
// the download service's authentication, caching, and object-open layers.
//
// An ID is parsed once from a raw request target and never mutated. Its
// canonical form is the only cache key, the only signed payload, and the
// only object-store key, so the three layers can never disagree about what
// a request refers to.
//
// Normalization rules:
//   - Repeated slashes in the path collapse to one ("/a//b" == "/a/b").
//   - Percent-encoding is decoded strictly; invalid sequences are rejected.
//   - A raw '+' is a literal plus, never a space. Only "%20" decodes to a
//     space, so "q=a+b" and "q=a%20b" are distinct resources.
//   - Query parameters keep their original order and duplicates are
//     preserved, so "a=1&a=2" and "a=2&a=1" are distinct resources.
package resource

import (
	"errors"
	"fmt"
	"strings"
)

// Param is a single decoded query parameter. Order and duplicates matter.
type Param struct {
	Key   string
	Value string
}

// ID is an immutable, canonical resource identifier. The zero value is not
// valid; obtain one via Parse.
type ID struct {
	path      string // decoded, slashes collapsed
	params    []Param
	canonical string // computed once at parse time
}

// ParseError describes a malformed request target.
type ParseError struct {
	Input  string
	Reason string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("resource: invalid identifier %q: %s", e.Input, e.Reason)
}

// Parse builds the canonical identifier for a raw request target such as
// "/dl//report%7Efinal.csv?x=1&x=2&q=a+b". It rejects invalid
// percent-encoding instead of guessing.
func Parse(raw string) (ID, error) {
	if raw == "" {
		return ID{}, &ParseError{Input: raw, Reason: "empty target"}
	}
	if raw[0] != '/' {
		return ID{}, &ParseError{Input: raw, Reason: "path must be absolute"}
	}

	rawPath, rawQuery, hasQuery := strings.Cut(raw, "?")

	path, err := unescape(rawPath)
	if err != nil {
		return ID{}, &ParseError{Input: raw, Reason: "path: " + err.Error()}
	}
	path = collapseSlashes(path)

	var params []Param
	if hasQuery && rawQuery != "" {
		for _, pair := range strings.Split(rawQuery, "&") {
			if pair == "" {
				continue
			}
			rawKey, rawValue, _ := strings.Cut(pair, "=")
			key, err := unescape(rawKey)
			if err != nil {
				return ID{}, &ParseError{Input: raw, Reason: "query key: " + err.Error()}
			}
			value, err := unescape(rawValue)
			if err != nil {
				return ID{}, &ParseError{Input: raw, Reason: "query value: " + err.Error()}
			}
			params = append(params, Param{Key: key, Value: value})
		}
	}

	id := ID{path: path, params: params}
	id.canonical = id.buildCanonical()
	return id, nil
}

// MustParse is Parse for tests and constants; it panics on error.
func MustParse(raw string) ID {
	id, err := Parse(raw)
	if err != nil {
		panic(err)
	}
	return id
}

// Path returns the decoded, slash-collapsed path.
func (id ID) Path() string { return id.path }

// Params returns a copy of the ordered query parameters, duplicates included.
func (id ID) Params() []Param {
	out := make([]Param, len(id.params))
	copy(out, id.params)
	return out
}

// String returns the canonical form. It is the one and only representation
// used for signing, cache keys, and object-store keys. Reparsing it yields
// an equal ID.
func (id ID) String() string { return id.canonical }

// CacheKey is the canonical form; it exists so call sites read naturally.
func (id ID) CacheKey() string { return id.canonical }

// Equal reports whether two identifiers denote the same resource.
func (id ID) Equal(other ID) bool { return id.canonical == other.canonical }

func (id ID) buildCanonical() string {
	var b strings.Builder
	b.WriteString(escape(id.path, true))
	if len(id.params) > 0 {
		b.WriteByte('?')
		for i, p := range id.params {
			if i > 0 {
				b.WriteByte('&')
			}
			b.WriteString(escape(p.Key, false))
			b.WriteByte('=')
			b.WriteString(escape(p.Value, false))
		}
	}
	return b.String()
}

func collapseSlashes(s string) string {
	if !strings.Contains(s, "//") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	prevSlash := false
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			if prevSlash {
				continue
			}
			prevSlash = true
		} else {
			prevSlash = false
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

const upperhex = "0123456789ABCDEF"

func isUnreserved(c byte) bool {
	return 'A' <= c && c <= 'Z' ||
		'a' <= c && c <= 'z' ||
		'0' <= c && c <= '9' ||
		c == '-' || c == '_' || c == '.' || c == '~'
}

// escape percent-encodes every byte that is not unreserved (and not '/'
// when allowSlash is set). Spaces become "%20", never '+', and a literal
// plus becomes "%2B", keeping the two distinguishable in canonical form.
func escape(s string, allowSlash bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) || (allowSlash && c == '/') {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperhex[c>>4])
		b.WriteByte(upperhex[c&0x0f])
	}
	return b.String()
}

var errBadEscape = errors.New("invalid percent-encoding")

func fromHex(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// unescape decodes percent-encoding strictly. A raw '+' is passed through
// as a literal plus; it is never treated as a space.
func unescape(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		if i+2 >= len(s) {
			return "", fmt.Errorf("%w: truncated sequence at byte %d", errBadEscape, i)
		}
		hi, okHi := fromHex(s[i+1])
		lo, okLo := fromHex(s[i+2])
		if !okHi || !okLo {
			return "", fmt.Errorf("%w: %q at byte %d", errBadEscape, s[i:i+3], i)
		}
		b.WriteByte(hi<<4 | lo)
		i += 2
	}
	return b.String(), nil
}
