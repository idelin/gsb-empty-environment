package fuzz

import (
	"encoding/binary"
	"hash/crc32"

	"kvsfmt/format"
)

// Mutation is a single-point mutation. Each mutation breaks exactly
// the constraint named by Class while keeping every other premise
// legal (e.g. the checksum is recomputed after field edits, so only
// the targeted constraint is violated). Apply returns nil when the
// mutation does not apply to the sample (e.g. no entries).
type Mutation struct {
	Name  string
	Class format.Class
	Apply func(data []byte) []byte
}

func clone(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

// fixCRC recomputes the checksum so a field edit breaks only its own
// constraint and not the crc constraint.
func fixCRC(b []byte) {
	binary.LittleEndian.PutUint32(b[len(b)-4:], crc32.ChecksumIEEE(b[:len(b)-4]))
}

func countOf(d []byte) int {
	return int(binary.LittleEndian.Uint16(d[4:6]))
}

// Mutations returns the single-point mutation operators.
func Mutations() []Mutation {
	return []Mutation{
		{
			Name:  "magic-byte",
			Class: format.ClassMagic,
			Apply: func(d []byte) []byte { b := clone(d); b[0] ^= 0x01; return b },
		},
		{
			Name:  "count-overflow",
			Class: format.ClassCount,
			Apply: func(d []byte) []byte {
				b := clone(d)
				binary.LittleEndian.PutUint16(b[4:6], uint16(format.MaxEntries+1))
				fixCRC(b)
				return b
			},
		},
		{
			Name:  "keylen-zero",
			Class: format.ClassKeyLen,
			Apply: func(d []byte) []byte {
				if countOf(d) == 0 {
					return nil
				}
				b := clone(d)
				b[6] = 0
				fixCRC(b)
				return b
			},
		},
		{
			Name:  "keychar-invalid",
			Class: format.ClassKeyChar,
			Apply: func(d []byte) []byte {
				if countOf(d) == 0 {
					return nil
				}
				b := clone(d)
				b[7] = '\n'
				fixCRC(b)
				return b
			},
		},
		{
			Name:  "vallen-overflow",
			Class: format.ClassValLen,
			Apply: func(d []byte) []byte {
				if countOf(d) == 0 {
					return nil
				}
				b := clone(d)
				klen := int(b[6])
				off := 7 + klen
				binary.LittleEndian.PutUint16(b[off:off+2], uint16(format.MaxValLen+1))
				fixCRC(b)
				return b
			},
		},
		{
			Name:  "crc-flip",
			Class: format.ClassCRC,
			Apply: func(d []byte) []byte { b := clone(d); b[len(b)-1] ^= 0x80; return b },
		},
		{
			// Truncation inherently removes checksum bytes; the
			// decoder checks truncation before the checksum, so the
			// observable violation stays the truncated class.
			Name:  "truncated-crc",
			Class: format.ClassTruncated,
			Apply: func(d []byte) []byte { return clone(d[:len(d)-2]) },
		},
	}
}
