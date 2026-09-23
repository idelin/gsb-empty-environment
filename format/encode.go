package format

import (
	"encoding/binary"
	"hash/crc32"
)

// Encode serializes entries. Output is deterministic and never exceeds
// Budget; exceeding the budget is reported as an implementation error.
func Encode(entries []Entry) ([]byte, error) {
	if len(entries) > MaxEntries {
		return nil, &Error{Class: ClassCount, Msg: "too many entries"}
	}
	size := len(Magic) + 2 + 4
	for _, e := range entries {
		if len(e.Key) < 1 || len(e.Key) > MaxKeyLen {
			return nil, &Error{Class: ClassKeyLen, Msg: "key length out of range"}
		}
		for i := 0; i < len(e.Key); i++ {
			if !ValidKeyByte(e.Key[i]) {
				return nil, &Error{Class: ClassKeyChar, Msg: "invalid key byte"}
			}
		}
		if len(e.Value) > MaxValLen {
			return nil, &Error{Class: ClassValLen, Msg: "value too large"}
		}
		size += 1 + len(e.Key) + 2 + len(e.Value)
	}
	if size > Budget {
		return nil, &Error{Class: ClassBudget, Msg: "encoded output exceeds budget"}
	}
	buf := make([]byte, 0, size)
	buf = append(buf, Magic...)
	var tmp [2]byte
	binary.LittleEndian.PutUint16(tmp[:], uint16(len(entries)))
	buf = append(buf, tmp[:]...)
	for _, e := range entries {
		buf = append(buf, byte(len(e.Key)))
		buf = append(buf, e.Key...)
		binary.LittleEndian.PutUint16(tmp[:], uint16(len(e.Value)))
		buf = append(buf, tmp[:]...)
		buf = append(buf, e.Value...)
	}
	var sum [4]byte
	binary.LittleEndian.PutUint32(sum[:], crc32.ChecksumIEEE(buf))
	buf = append(buf, sum[:]...)
	return buf, nil
}
