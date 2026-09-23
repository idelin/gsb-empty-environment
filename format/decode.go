package format

import (
	"encoding/binary"
	"hash/crc32"
)

// Decode parses data into entries. It is pure (executes no host code),
// and validates every length against the remaining input before
// allocating or copying.
func Decode(data []byte) ([]Entry, error) {
	if len(data) > Budget {
		return nil, &Error{Class: ClassBudget, Msg: "input exceeds budget"}
	}
	if len(data) < len(Magic) {
		return nil, &Error{Class: ClassTruncated, Msg: "shorter than magic"}
	}
	if string(data[:len(Magic)]) != Magic {
		return nil, &Error{Class: ClassMagic, Msg: "bad magic"}
	}
	if len(data) < len(Magic)+2 {
		return nil, &Error{Class: ClassTruncated, Msg: "missing entry count"}
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count > MaxEntries {
		return nil, &Error{Class: ClassCount, Msg: "count exceeds max entries"}
	}
	off := len(Magic) + 2
	entries := make([]Entry, 0, count)
	for i := 0; i < count; i++ {
		if off+1 > len(data) {
			return nil, &Error{Class: ClassTruncated, Msg: "missing key length"}
		}
		klen := int(data[off])
		off++
		if klen < 1 || klen > MaxKeyLen {
			return nil, &Error{Class: ClassKeyLen, Msg: "key length out of range"}
		}
		if off+klen > len(data) {
			return nil, &Error{Class: ClassTruncated, Msg: "key truncated"}
		}
		for j := 0; j < klen; j++ {
			if !ValidKeyByte(data[off+j]) {
				return nil, &Error{Class: ClassKeyChar, Msg: "invalid key byte"}
			}
		}
		key := string(data[off : off+klen])
		off += klen
		if off+2 > len(data) {
			return nil, &Error{Class: ClassTruncated, Msg: "missing value length"}
		}
		vlen := int(binary.LittleEndian.Uint16(data[off : off+2]))
		off += 2
		if vlen > MaxValLen {
			return nil, &Error{Class: ClassValLen, Msg: "value length out of range"}
		}
		if off+vlen > len(data) {
			return nil, &Error{Class: ClassTruncated, Msg: "value truncated"}
		}
		val := make([]byte, vlen)
		copy(val, data[off:off+vlen])
		off += vlen
		entries = append(entries, Entry{Key: key, Value: val})
	}
	if len(data)-off < 4 {
		return nil, &Error{Class: ClassTruncated, Msg: "missing checksum"}
	}
	if len(data)-off > 4 {
		return nil, &Error{Class: ClassTrailing, Msg: "trailing bytes after checksum"}
	}
	want := binary.LittleEndian.Uint32(data[off:])
	if got := crc32.ChecksumIEEE(data[:off]); want != got {
		return nil, &Error{Class: ClassCRC, Msg: "checksum mismatch"}
	}
	return entries, nil
}
