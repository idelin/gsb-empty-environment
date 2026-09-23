// Package oracle is an independent second implementation of the PKV1 spec,
// written separately from the format package (the SUT). The harness
// cross-checks the SUT against this oracle so the test oracle never merely
// loops through the same implementation under test.
package oracle

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"strings"
)

// Limits, duplicated from the spec on purpose (no shared constants).
const (
	maxCount    = 1024
	maxKeyLen   = 64
	maxValueLen = 4096
	maxMessage  = 1 << 16
)

type Record struct {
	Key   string
	Value string
}

type Message struct {
	Records []Record
}

// Error carries a spec-constraint class string identical to the SUT's
// classes so the harness can compare them.
type Error struct {
	Class string
	Msg   string
}

func (e *Error) Error() string { return e.Class + ": " + e.Msg }

func errf(class, format string, args ...any) *Error {
	return &Error{Class: class, Msg: fmt.Sprintf(format, args...)}
}

// ClassOf extracts the class string from an oracle error.
func ClassOf(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Class
	}
	return ""
}

var keyCharset [128]bool

func init() {
	for c := byte('a'); c <= 'z'; c++ {
		keyCharset[c] = true
	}
	for c := byte('A'); c <= 'Z'; c++ {
		keyCharset[c] = true
	}
	for c := byte('0'); c <= '9'; c++ {
		keyCharset[c] = true
	}
	keyCharset['_'] = true
	keyCharset['.'] = true
	keyCharset['-'] = true
}

// Encode serializes m using an independent code path (strings.Builder and
// rune-level escaping). Canonical output must equal the SUT's byte for byte.
func Encode(m *Message) ([]byte, error) {
	if m == nil || len(m.Records) > maxCount {
		return nil, errf("too_large", "record count out of scope")
	}
	var sb strings.Builder
	sb.WriteString("PKV1")
	var hdr [4]byte
	binary.BigEndian.PutUint16(hdr[:2], uint16(len(m.Records)))
	sb.Write(hdr[:2])
	for _, r := range m.Records {
		if len(r.Key) < 1 || len(r.Key) > maxKeyLen {
			return nil, errf("key_length", "key %q out of scope", r.Key)
		}
		for i := 0; i < len(r.Key); i++ {
			if r.Key[i] > 0x7f || !keyCharset[r.Key[i]] {
				return nil, errf("key_charset", "key %q out of scope", r.Key)
			}
		}
		var val strings.Builder
		val.WriteByte('"')
		for _, c := range r.Value {
			switch c {
			case '"':
				val.WriteString(`\"`)
			case '\\':
				val.WriteString(`\\`)
			case '\n':
				val.WriteString(`\n`)
			case '\t':
				val.WriteString(`\t`)
			default:
				if c < 0x20 || c > 0x7e {
					return nil, errf("quoting", "value byte %q out of scope", c)
				}
				val.WriteRune(c)
			}
		}
		val.WriteByte('"')
		if val.Len() > maxValueLen {
			return nil, errf("value_length", "value out of scope")
		}
		binary.BigEndian.PutUint16(hdr[:2], uint16(len(r.Key)))
		sb.Write(hdr[:2])
		sb.WriteString(r.Key)
		binary.BigEndian.PutUint16(hdr[:2], uint16(val.Len()))
		sb.Write(hdr[:2])
		sb.WriteString(val.String())
	}
	body := []byte(sb.String())
	binary.BigEndian.PutUint32(hdr[:4], crc32.ChecksumIEEE(body))
	return append(body, hdr[:4]...), nil
}

// Decode parses b with an independent cursor implementation.
func Decode(b []byte) (*Message, error) {
	switch {
	case len(b) > maxMessage:
		return nil, errf("too_large", "%d bytes", len(b))
	case len(b) < 10:
		return nil, errf("truncated", "%d bytes", len(b))
	}
	if string(b[:4]) != "PKV1" {
		return nil, errf("magic", "bad magic")
	}
	want := int(binary.BigEndian.Uint16(b[4:6]))
	if want > maxCount {
		return nil, errf("count", "declared %d", want)
	}
	pos := 6
	limit := len(b) - 4
	msg := &Message{}
	for pos < limit {
		if len(msg.Records) >= maxCount {
			return nil, errf("count", "record overflow")
		}
		klen, err := takeU16(b, &pos, limit)
		if err != nil {
			return nil, err
		}
		if klen < 1 || klen > maxKeyLen {
			return nil, errf("key_length", "klen %d", klen)
		}
		if limit-pos < klen {
			return nil, errf("key_length", "klen %d past end", klen)
		}
		key := string(b[pos : pos+klen])
		pos += klen
		for i := 0; i < len(key); i++ {
			if key[i] > 0x7f || !keyCharset[key[i]] {
				return nil, errf("key_charset", "key %q", key)
			}
		}
		vlen, err := takeU16(b, &pos, limit)
		if err != nil {
			return nil, err
		}
		if vlen > maxValueLen {
			return nil, errf("value_length", "vlen %d", vlen)
		}
		if limit-pos < vlen {
			return nil, errf("value_length", "vlen %d past end", vlen)
		}
		val, used, err := unquote(b[pos:limit])
		if err != nil {
			return nil, err
		}
		if used != vlen {
			return nil, errf("value_length", "declared %d, used %d", vlen, used)
		}
		pos += vlen
		msg.Records = append(msg.Records, Record{Key: key, Value: val})
	}
	if len(msg.Records) != want {
		return nil, errf("count", "declared %d, parsed %d", want, len(msg.Records))
	}
	stored := binary.BigEndian.Uint32(b[limit:])
	if crc32.ChecksumIEEE(b[:limit]) != stored {
		return nil, errf("checksum", "crc mismatch")
	}
	return msg, nil
}

func takeU16(b []byte, pos *int, limit int) (int, error) {
	if limit-*pos < 2 {
		return 0, errf("truncated", "length prefix past end")
	}
	v := int(binary.BigEndian.Uint16(b[*pos:]))
	*pos += 2
	return v, nil
}

func unquote(b []byte) (string, int, error) {
	if len(b) == 0 || b[0] != '"' {
		return "", 0, errf("quoting", "missing opening quote")
	}
	var sb strings.Builder
	i := 1
	for i < len(b) {
		switch c := b[i]; c {
		case '"':
			return sb.String(), i + 1, nil
		case '\\':
			if i+1 >= len(b) {
				return "", 0, errf("quoting", "dangling escape")
			}
			switch b[i+1] {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case '"':
				sb.WriteByte('"')
			case '\\':
				sb.WriteByte('\\')
			default:
				return "", 0, errf("quoting", "bad escape")
			}
			i += 2
		default:
			if c < 0x20 || c > 0x7e {
				return "", 0, errf("quoting", "raw byte %d", c)
			}
			sb.WriteByte(c)
			i++
		}
	}
	return "", 0, errf("quoting", "unterminated")
}

// Equal reports structural equality without importing the SUT types.
func Equal(m *Message, keys, vals []string) bool {
	if m == nil || len(m.Records) != len(keys) {
		return false
	}
	for i, r := range m.Records {
		if r.Key != keys[i] || r.Value != vals[i] {
			return false
		}
	}
	return true
}
