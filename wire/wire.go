// Package wire implements a resumable parser for an explicit subset of a
// protobuf-style binary wire format, using only the Go standard library.
//
// Supported wire types:
//
//	0  varint, at most 10 bytes, decoded as uint64
//	1  64-bit little-endian
//	2  varint length followed by that many raw bytes
//	5  32-bit little-endian
//
// All other wire types are rejected. Field numbers must be in
// [1, MaxFieldNumber]; the key is (field<<3)|wireType.
package wire

import "errors"

// Wire type constants.
const (
	TypeVarint  = 0
	TypeFixed64 = 1
	TypeBytes   = 2
	TypeFixed32 = 5
)

// MaxFieldNumber is the largest legal field number (2^29 - 1).
const MaxFieldNumber = 1<<29 - 1

var (
	errIncomplete     = errors.New("wire: incomplete input")
	errVarintOverflow = errors.New("varint overflows uint64")
)

// decodeVarint decodes a base-128 varint from the front of b.
// It returns errIncomplete if b ends mid-varint, and errVarintOverflow
// if the varint needs more than 64 bits (more than 10 bytes, or a 10th
// byte greater than 1).
func decodeVarint(b []byte) (v uint64, n int, err error) {
	for i := 0; i < len(b) && i < 10; i++ {
		c := b[i]
		if i == 9 && c > 1 {
			return 0, 0, errVarintOverflow
		}
		v |= uint64(c&0x7f) << uint(7*i)
		if c < 0x80 {
			return v, i + 1, nil
		}
	}
	if len(b) >= 10 {
		return 0, 0, errVarintOverflow
	}
	return 0, 0, errIncomplete
}

func appendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func appendFixed32(dst []byte, v uint32) []byte {
	return append(dst, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func appendFixed64(dst []byte, v uint64) []byte {
	return append(dst,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// keyOf builds the field key (field<<3)|wireType.
func keyOf(field uint32, typ int) uint64 {
	return uint64(field)<<3 | uint64(typ)
}
