// Package format implements the PKV1 wire format: a canonical encoder and a
// strict decoder. This package is the system under test (SUT).
//
// Wire layout (all integers big-endian):
//
//	magic    4 bytes  "PKV1"
//	count    uint16   number of records, must equal the parsed record count
//	record   klen uint16 | key bytes | vlen uint16 | quoted value bytes
//	checksum uint32   CRC32-IEEE of all preceding bytes
//
// Constraints tracked by the test generator (one per single-point mutation):
//
//	C1 magic, C2 count, C3 key length, C4 key charset,
//	C5 value length, C6 quoting/escaping, C7 checksum.
//
// Supported serialization scope (see InScope): keys are 1..64 bytes of
// [A-Za-z0-9_.-]; values contain only printable ASCII (0x20..0x7E), '\n'
// and '\t', escaped as \" \\ \n \t; at most MaxCount records. The total
// encoded size must not exceed MaxMessageSize; the harness treats an
// over-budget encoding of an in-scope message as an implementation defect,
// never as a parse error.
//
// Decoding never executes host code: it is pure byte parsing with no
// reflection, eval, or plugin loading. Every length prefix is validated
// against the remaining input before any allocation or copy happens.
package format

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

const (
	MaxCount       = 1024
	MaxKeyLen      = 64
	MaxValueLen    = 4096 // encoded (quoted) form
	MaxMessageSize = 1 << 16
	minMessageSize = 4 + 2 + 4 // magic + count + checksum
)

var Magic = []byte{'P', 'K', 'V', '1'}

// Class identifies which format constraint an error belongs to.
type Class string

const (
	ClassNone       Class = ""
	ClassTruncated  Class = "truncated"
	ClassTooLarge   Class = "too_large"
	ClassMagic      Class = "magic"
	ClassCount      Class = "count"
	ClassKeyLen     Class = "key_length"
	ClassKeyCharset Class = "key_charset"
	ClassValueLen   Class = "value_length"
	ClassQuoting    Class = "quoting"
	ClassChecksum   Class = "checksum"
)

// Error is a decode/encode failure tagged with its constraint class.
type Error struct {
	Class  Class
	Detail string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Class, e.Detail) }

// ClassOf extracts the constraint class of an error, or ClassNone.
func ClassOf(err error) Class {
	if err == nil {
		return ClassNone
	}
	if e, ok := err.(*Error); ok {
		return e.Class
	}
	return ClassNone
}

type Record struct {
	Key   string
	Value string
}

type Message struct {
	Records []Record
}

func validKeyBytes(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '_' || c == '.' || c == '-':
		default:
			return false
		}
	}
	return true
}

func validValueBytes(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\t' {
			continue
		}
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// EscapedLen returns the encoded length of a value including both quotes.
func EscapedLen(s string) int {
	n := 2
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\\', '\n', '\t':
			n += 2
		default:
			n++
		}
	}
	return n
}

// InScope reports whether m is inside the supported serialization scope.
func InScope(m *Message) bool {
	if m == nil || len(m.Records) > MaxCount {
		return false
	}
	for _, r := range m.Records {
		if len(r.Key) > MaxKeyLen || !validKeyBytes([]byte(r.Key)) {
			return false
		}
		if !validValueBytes(r.Value) || EscapedLen(r.Value) > MaxValueLen {
			return false
		}
	}
	return true
}

// Encode serializes m canonically. It is deterministic: identical messages
// always produce identical bytes.
func Encode(m *Message) ([]byte, error) {
	if !InScope(m) {
		return nil, &Error{Class: ClassTooLarge, Detail: "message outside supported scope"}
	}
	buf := make([]byte, 0, 128)
	buf = append(buf, Magic...)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(m.Records)))
	for _, r := range m.Records {
		buf = binary.BigEndian.AppendUint16(buf, uint16(len(r.Key)))
		buf = append(buf, r.Key...)
		buf = binary.BigEndian.AppendUint16(buf, uint16(EscapedLen(r.Value)))
		buf = append(buf, '"')
		for i := 0; i < len(r.Value); i++ {
			switch c := r.Value[i]; c {
			case '"':
				buf = append(buf, '\\', '"')
			case '\\':
				buf = append(buf, '\\', '\\')
			case '\n':
				buf = append(buf, '\\', 'n')
			case '\t':
				buf = append(buf, '\\', 't')
			default:
				buf = append(buf, c)
			}
		}
		buf = append(buf, '"')
	}
	sum := crc32.ChecksumIEEE(buf)
	buf = binary.BigEndian.AppendUint32(buf, sum)
	return buf, nil
}

// Decode parses b strictly, rejecting any constraint violation with a
// classified error. Length prefixes are always checked against the remaining
// input before allocation or copying.
func Decode(b []byte) (*Message, error) {
	if len(b) > MaxMessageSize {
		return nil, &Error{Class: ClassTooLarge, Detail: fmt.Sprintf("%d bytes exceeds budget %d", len(b), MaxMessageSize)}
	}
	if len(b) < minMessageSize {
		return nil, &Error{Class: ClassTruncated, Detail: fmt.Sprintf("only %d bytes", len(b))}
	}
	if !bytes.Equal(b[:4], Magic) {
		return nil, &Error{Class: ClassMagic, Detail: "bad magic"}
	}
	count := int(binary.BigEndian.Uint16(b[4:6]))
	if count > MaxCount {
		return nil, &Error{Class: ClassCount, Detail: fmt.Sprintf("declared count %d exceeds limit", count)}
	}
	end := len(b) - 4 // checksum trailer
	cur := 6
	recs := make([]Record, 0, min(count, 16))
	for cur < end {
		if len(recs) >= MaxCount {
			return nil, &Error{Class: ClassCount, Detail: "more records than allowed"}
		}
		if cur+2 > end {
			return nil, &Error{Class: ClassTruncated, Detail: "truncated key length"}
		}
		klen := int(binary.BigEndian.Uint16(b[cur : cur+2]))
		cur += 2
		if klen < 1 || klen > MaxKeyLen {
			return nil, &Error{Class: ClassKeyLen, Detail: fmt.Sprintf("key length %d out of range", klen)}
		}
		if klen > end-cur { // validate length before allocating/copying
			return nil, &Error{Class: ClassKeyLen, Detail: "key length exceeds remaining input"}
		}
		key := b[cur : cur+klen]
		cur += klen
		if !validKeyBytes(key) {
			return nil, &Error{Class: ClassKeyCharset, Detail: "invalid key byte"}
		}
		if cur+2 > end {
			return nil, &Error{Class: ClassTruncated, Detail: "truncated value length"}
		}
		vlen := int(binary.BigEndian.Uint16(b[cur : cur+2]))
		cur += 2
		if vlen > MaxValueLen {
			return nil, &Error{Class: ClassValueLen, Detail: fmt.Sprintf("value length %d exceeds limit", vlen)}
		}
		if vlen > end-cur { // validate length before allocating/copying
			return nil, &Error{Class: ClassValueLen, Detail: "value length exceeds remaining input"}
		}
		val, consumed, err := scanQuoted(b[cur:end])
		if err != nil {
			return nil, err
		}
		if consumed != vlen {
			return nil, &Error{Class: ClassValueLen, Detail: fmt.Sprintf("declared %d bytes, quoted form uses %d", vlen, consumed)}
		}
		cur += vlen
		recs = append(recs, Record{Key: string(key), Value: val})
	}
	if len(recs) != count {
		return nil, &Error{Class: ClassCount, Detail: fmt.Sprintf("declared %d records, parsed %d", count, len(recs))}
	}
	want := binary.BigEndian.Uint32(b[end:])
	if got := crc32.ChecksumIEEE(b[:end]); got != want {
		return nil, &Error{Class: ClassChecksum, Detail: fmt.Sprintf("crc mismatch: want %08x got %08x", want, got)}
	}
	return &Message{Records: recs}, nil
}

// scanQuoted parses one quoted, escaped value at the start of b and returns
// the unescaped string and the number of input bytes consumed.
func scanQuoted(b []byte) (string, int, error) {
	if len(b) < 2 || b[0] != '"' {
		return "", 0, &Error{Class: ClassQuoting, Detail: "value does not start with quote"}
	}
	out := make([]byte, 0, min(len(b), 64))
	i := 1
	for {
		if i >= len(b) {
			return "", 0, &Error{Class: ClassQuoting, Detail: "unterminated quoted value"}
		}
		c := b[i]
		switch c {
		case '"':
			return string(out), i + 1, nil
		case '\\':
			if i+1 >= len(b) {
				return "", 0, &Error{Class: ClassQuoting, Detail: "dangling escape"}
			}
			switch b[i+1] {
			case 'n':
				out = append(out, '\n')
			case 't':
				out = append(out, '\t')
			case '"':
				out = append(out, '"')
			case '\\':
				out = append(out, '\\')
			default:
				return "", 0, &Error{Class: ClassQuoting, Detail: fmt.Sprintf("invalid escape \\%c", b[i+1])}
			}
			i += 2
		default:
			if c < 0x20 || c > 0x7e {
				return "", 0, &Error{Class: ClassQuoting, Detail: "unescaped control or non-ASCII byte"}
			}
			out = append(out, c)
			i++
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
