package canonkey

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Key is the canonical, unambiguous encoding of an ordered list of typed
// nullable fields.
//
// Wire format:
//
//	K1#<field-count>#<field><field>...
//
// Each field is length-prefixed, so payloads may contain any byte —
// including the structural characters '#', ':' and digits — without ever
// being confused with framing:
//
//	<tag><payload-len>:<payload>
//
// Tags: N=null, S=string, I=int, F=float, B=bool, X=bytes.
// Null is the only field with an empty payload, and its tag keeps it
// distinct from an empty string ("S0:") or empty bytes ("X0:").
type Key string

const keyPrefix = "K1#"

// ErrMalformedKey is returned when decoding a key that was not produced by
// EncodeKey (or was corrupted).
var ErrMalformedKey = errors.New("canonkey: malformed key")

// EncodeKey builds the canonical key for the given fields. The encoding is
// injective: two field lists encode to the same key if and only if every
// field is Equal.
func EncodeKey(fields ...Value) Key {
	var b strings.Builder
	b.WriteString(keyPrefix)
	b.WriteString(strconv.Itoa(len(fields)))
	b.WriteByte('#')
	for _, f := range fields {
		writeField(&b, f)
	}
	return Key(b.String())
}

func writeField(b *strings.Builder, f Value) {
	switch f.Kind {
	case KindNull:
		b.WriteString("N0:")
	case KindString:
		writePayload(b, 'S', []byte(f.Str))
	case KindInt:
		writePayload(b, 'I', []byte(strconv.FormatInt(f.Int, 10)))
	case KindFloat:
		// 'g' with precision -1 is the shortest round-trip form and
		// normalizes NaN/+Inf/-Inf to stable spellings.
		writePayload(b, 'F', []byte(strconv.FormatFloat(f.Float, 'g', -1, 64)))
	case KindBool:
		if f.Bool {
			b.WriteString("B1:1")
		} else {
			b.WriteString("B1:0")
		}
	case KindBytes:
		writePayload(b, 'X', f.Blob)
	default:
		panic(fmt.Sprintf("canonkey: unknown field kind %d", f.Kind))
	}
}

func writePayload(b *strings.Builder, tag byte, payload []byte) {
	b.WriteByte(tag)
	b.WriteString(strconv.Itoa(len(payload)))
	b.WriteByte(':')
	b.Write(payload)
}

// Fields decodes the key back into its original field list.
func (k Key) Fields() ([]Value, error) {
	s := string(k)
	if !strings.HasPrefix(s, keyPrefix) {
		return nil, ErrMalformedKey
	}
	rest := s[len(keyPrefix):]
	sep := strings.IndexByte(rest, '#')
	if sep < 0 {
		return nil, ErrMalformedKey
	}
	count, err := strconv.Atoi(rest[:sep])
	if err != nil || count < 0 {
		return nil, ErrMalformedKey
	}
	rest = rest[sep+1:]

	fields := make([]Value, 0, count)
	for i := 0; i < count; i++ {
		v, remaining, err := readField(rest)
		if err != nil {
			return nil, err
		}
		fields = append(fields, v)
		rest = remaining
	}
	if rest != "" {
		return nil, ErrMalformedKey
	}
	return fields, nil
}

func readField(s string) (Value, string, error) {
	if len(s) < 2 {
		return Value{}, "", ErrMalformedKey
	}
	tag := s[0]
	sep := strings.IndexByte(s, ':')
	if sep < 1 {
		return Value{}, "", ErrMalformedKey
	}
	n, err := strconv.Atoi(s[1:sep])
	if err != nil || n < 0 {
		return Value{}, "", ErrMalformedKey
	}
	payload := s[sep+1:]
	if len(payload) < n {
		return Value{}, "", ErrMalformedKey
	}
	body, rest := payload[:n], payload[n:]

	switch tag {
	case 'N':
		if n != 0 {
			return Value{}, "", ErrMalformedKey
		}
		return Null(), rest, nil
	case 'S':
		return String(body), rest, nil
	case 'I':
		i, err := strconv.ParseInt(body, 10, 64)
		if err != nil {
			return Value{}, "", ErrMalformedKey
		}
		return Int(i), rest, nil
	case 'F':
		f, err := strconv.ParseFloat(body, 64)
		if err != nil {
			return Value{}, "", ErrMalformedKey
		}
		return Float(f), rest, nil
	case 'B':
		switch body {
		case "0":
			return Bool(false), rest, nil
		case "1":
			return Bool(true), rest, nil
		default:
			return Value{}, "", ErrMalformedKey
		}
	case 'X':
		return Bytes([]byte(body)), rest, nil
	default:
		return Value{}, "", ErrMalformedKey
	}
}
