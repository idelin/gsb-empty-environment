// Package buggy is a deliberately defective PKV1 decoder used to verify the
// harness can actually detect bugs (mutation testing of the test rig).
//
// Known defect: the CRC32 trailer is never verified, so checksum-corrupted
// messages are silently accepted.
package buggy

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"pkv/format"
)

// Decode behaves like format.Decode except the checksum check is missing.
func Decode(b []byte) (*format.Message, error) {
	if len(b) > format.MaxMessageSize {
		return nil, &format.Error{Class: format.ClassTooLarge, Detail: "too big"}
	}
	if len(b) < 10 {
		return nil, &format.Error{Class: format.ClassTruncated, Detail: "too short"}
	}
	if !bytes.Equal(b[:4], format.Magic) {
		return nil, &format.Error{Class: format.ClassMagic, Detail: "bad magic"}
	}
	count := int(binary.BigEndian.Uint16(b[4:6]))
	if count > format.MaxCount {
		return nil, &format.Error{Class: format.ClassCount, Detail: "count limit"}
	}
	end := len(b) - 4
	cur := 6
	var recs []format.Record
	for cur < end {
		if cur+2 > end {
			return nil, &format.Error{Class: format.ClassTruncated, Detail: "klen"}
		}
		klen := int(binary.BigEndian.Uint16(b[cur : cur+2]))
		cur += 2
		if klen < 1 || klen > format.MaxKeyLen || klen > end-cur {
			return nil, &format.Error{Class: format.ClassKeyLen, Detail: "klen range"}
		}
		key := b[cur : cur+klen]
		cur += klen
		for _, c := range key {
			ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
				c == '_' || c == '.' || c == '-'
			if !ok {
				return nil, &format.Error{Class: format.ClassKeyCharset, Detail: "key byte"}
			}
		}
		if cur+2 > end {
			return nil, &format.Error{Class: format.ClassTruncated, Detail: "vlen"}
		}
		vlen := int(binary.BigEndian.Uint16(b[cur : cur+2]))
		cur += 2
		if vlen > format.MaxValueLen || vlen > end-cur {
			return nil, &format.Error{Class: format.ClassValueLen, Detail: "vlen range"}
		}
		val, consumed, err := scanQuoted(b[cur:end])
		if err != nil {
			return nil, err
		}
		if consumed != vlen {
			return nil, &format.Error{Class: format.ClassValueLen, Detail: "vlen mismatch"}
		}
		cur += vlen
		recs = append(recs, format.Record{Key: string(key), Value: val})
	}
	if len(recs) != count {
		return nil, &format.Error{Class: format.ClassCount, Detail: "count mismatch"}
	}
	// BUG: checksum trailer is ignored; any CRC is accepted.
	return &format.Message{Records: recs}, nil
}

func scanQuoted(b []byte) (string, int, error) {
	if len(b) < 2 || b[0] != '"' {
		return "", 0, &format.Error{Class: format.ClassQuoting, Detail: "no quote"}
	}
	out := make([]byte, 0, 16)
	i := 1
	for {
		if i >= len(b) {
			return "", 0, &format.Error{Class: format.ClassQuoting, Detail: "unterminated"}
		}
		switch c := b[i]; c {
		case '"':
			return string(out), i + 1, nil
		case '\\':
			if i+1 >= len(b) {
				return "", 0, &format.Error{Class: format.ClassQuoting, Detail: "dangling escape"}
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
				return "", 0, &format.Error{Class: format.ClassQuoting,
					Detail: fmt.Sprintf("invalid escape \\%c", b[i+1])}
			}
			i += 2
		default:
			if c < 0x20 || c > 0x7e {
				return "", 0, &format.Error{Class: format.ClassQuoting, Detail: "raw byte"}
			}
			out = append(out, c)
			i++
		}
	}
}
