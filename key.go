package speckey

import (
	"errors"
	"strconv"
	"strings"
)

// keyPrefix versions the canonical encoding so the format can evolve.
const keyPrefix = "v1|"

// ErrMalformedKey is returned by ParseKey for input that is not a
// canonical key encoding.
var ErrMalformedKey = errors.New("speckey: malformed key encoding")

// SpecKey is a specification key composed of an ordered list of
// nullable fields. Two keys are equal iff their field lists are equal
// element-wise under Field.Equal.
type SpecKey struct {
	fields []Field
}

// NewKey builds a SpecKey from the given ordered fields.
func NewKey(fields ...Field) SpecKey {
	cp := make([]Field, len(fields))
	copy(cp, fields)
	return SpecKey{fields: cp}
}

// Fields returns a copy of the key's fields.
func (k SpecKey) Fields() []Field {
	cp := make([]Field, len(k.fields))
	copy(cp, k.fields)
	return cp
}

// Equal reports whether k and o have identical field lists.
func (k SpecKey) Equal(o SpecKey) bool {
	if len(k.fields) != len(o.fields) {
		return false
	}
	for i := range k.fields {
		if !k.fields[i].Equal(o.fields[i]) {
			return false
		}
	}
	return true
}

// String returns the canonical encoding of the key:
//
//	"v1|" <field count> ("|" <field encoding>)...
//
// The encoding is injective: distinct field lists always produce
// distinct strings, and null is never confused with the empty string.
func (k SpecKey) String() string {
	var b strings.Builder
	b.WriteString(keyPrefix)
	b.WriteString(strconv.Itoa(len(k.fields)))
	for _, f := range k.fields {
		b.WriteByte('|')
		appendField(&b, f)
	}
	return b.String()
}

// ParseKey decodes a canonical key encoding produced by String.
func ParseKey(s string) (SpecKey, error) {
	if !strings.HasPrefix(s, keyPrefix) {
		return SpecKey{}, ErrMalformedKey
	}
	rest := s[len(keyPrefix):]
	sep := strings.IndexByte(rest, '|')
	head, tail := rest, ""
	if sep >= 0 {
		// Keep the separator: each field token is "|" + encoding.
		head, tail = rest[:sep], rest[sep:]
	}
	count, err := strconv.Atoi(head)
	if err != nil || count < 0 {
		return SpecKey{}, ErrMalformedKey
	}
	fields := make([]Field, 0, count)
	for i := 0; i < count; i++ {
		if tail == "" {
			return SpecKey{}, ErrMalformedKey
		}
		var f Field
		f, tail, err = parseField(tail)
		if err != nil {
			return SpecKey{}, err
		}
		fields = append(fields, f)
	}
	if tail != "" {
		return SpecKey{}, ErrMalformedKey
	}
	return SpecKey{fields: fields}, nil
}

// parseField consumes one "|" + field token from s and returns the
// field plus the remaining input.
func parseField(s string) (Field, string, error) {
	if !strings.HasPrefix(s, "|") || len(s) < 2 {
		return Field{}, "", ErrMalformedKey
	}
	body := s[1:]
	switch body[0] {
	case 'N':
		return Null(), body[1:], nil
	case 'E':
		return Str(""), body[1:], nil
	case 'V':
		colon := strings.IndexByte(body, ':')
		if colon < 0 {
			return Field{}, "", ErrMalformedKey
		}
		n, err := strconv.Atoi(body[1:colon])
		if err != nil || n <= 0 {
			return Field{}, "", ErrMalformedKey
		}
		payload := body[colon+1:]
		if len(payload) < n {
			return Field{}, "", ErrMalformedKey
		}
		return Str(payload[:n]), payload[n:], nil
	default:
		return Field{}, "", ErrMalformedKey
	}
}
