// Package buggy contains a decoder with a known defect: it never
// verifies the CRC32 checksum. It exists to validate that the test
// harness can detect the defect.
package buggy

import (
	"encoding/binary"

	"kvsfmt/format"
)

// Decode behaves like format.Decode but skips checksum verification.
func Decode(data []byte) ([]format.Entry, error) {
	if len(data) > format.Budget {
		return nil, &format.Error{Class: format.ClassBudget, Msg: "input exceeds budget"}
	}
	if len(data) < len(format.Magic)+2+4 {
		return nil, &format.Error{Class: format.ClassTruncated, Msg: "shorter than header plus checksum"}
	}
	if string(data[:len(format.Magic)]) != format.Magic {
		return nil, &format.Error{Class: format.ClassMagic, Msg: "bad magic"}
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count > format.MaxEntries {
		return nil, &format.Error{Class: format.ClassCount, Msg: "count exceeds max entries"}
	}
	off := len(format.Magic) + 2
	entries := make([]format.Entry, 0, count)
	for i := 0; i < count; i++ {
		if off+1 > len(data) {
			return nil, &format.Error{Class: format.ClassTruncated, Msg: "missing key length"}
		}
		klen := int(data[off])
		off++
		if klen < 1 || klen > format.MaxKeyLen {
			return nil, &format.Error{Class: format.ClassKeyLen, Msg: "key length out of range"}
		}
		if off+klen > len(data) {
			return nil, &format.Error{Class: format.ClassTruncated, Msg: "key truncated"}
		}
		for j := 0; j < klen; j++ {
			if !format.ValidKeyByte(data[off+j]) {
				return nil, &format.Error{Class: format.ClassKeyChar, Msg: "invalid key byte"}
			}
		}
		key := string(data[off : off+klen])
		off += klen
		if off+2 > len(data) {
			return nil, &format.Error{Class: format.ClassTruncated, Msg: "missing value length"}
		}
		vlen := int(binary.LittleEndian.Uint16(data[off : off+2]))
		off += 2
		if vlen > format.MaxValLen {
			return nil, &format.Error{Class: format.ClassValLen, Msg: "value length out of range"}
		}
		if off+vlen > len(data) {
			return nil, &format.Error{Class: format.ClassTruncated, Msg: "value truncated"}
		}
		val := make([]byte, vlen)
		copy(val, data[off:off+vlen])
		off += vlen
		entries = append(entries, format.Entry{Key: key, Value: val})
	}
	if len(data)-off < 4 {
		return nil, &format.Error{Class: format.ClassTruncated, Msg: "missing checksum"}
	}
	if len(data)-off > 4 {
		return nil, &format.Error{Class: format.ClassTrailing, Msg: "trailing bytes after checksum"}
	}
	// KNOWN DEFECT: checksum bytes are present but never verified.
	return entries, nil
}
