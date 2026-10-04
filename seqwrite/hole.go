package seqwrite

import (
	"encoding/binary"
	"hash/crc32"
)

// HoleHeaderSize is the fixed size of a hole record. Reservations smaller
// than this cannot be turned into holes and will poison the log instead.
const HoleHeaderSize = 64

// Hole reasons recorded in a hole record.
const (
	HoleReasonFailed   byte = 1
	HoleReasonCanceled byte = 2
)

// Hole record layout (64 bytes, little-endian):
//   [0:8]   magic "SEQWHOL1"
//   [8]     reason (HoleReason*)
//   [9:12]  reserved, zero
//   [12:20] offset of the hole
//   [20:28] length of the hole (the full reserved range)
//   [28:32] cause string length (<= 28)
//   [32:60] cause string, truncated, zero padded
//   [60:64] crc32 IEEE of bytes [0:60]
var holeMagic = []byte{'S', 'E', 'Q', 'W', 'H', 'O', 'L', '1'}

const holeCauseMax = 28

// HoleInfo describes a parsed hole record.
type HoleInfo struct {
	Offset uint64
	Length uint64
	Reason byte
	Cause  string
}

// EncodeHole builds the 64-byte hole record for the given range.
func EncodeHole(offset, length uint64, reason byte, cause error) []byte {
	b := make([]byte, HoleHeaderSize)
	copy(b[0:8], holeMagic)
	b[8] = reason
	binary.LittleEndian.PutUint64(b[12:20], offset)
	binary.LittleEndian.PutUint64(b[20:28], length)
	if cause != nil {
		s := cause.Error()
		if len(s) > holeCauseMax {
			s = s[:holeCauseMax]
		}
		binary.LittleEndian.PutUint32(b[28:32], uint32(len(s)))
		copy(b[32:60], s)
	}
	binary.LittleEndian.PutUint32(b[60:64], crc32.ChecksumIEEE(b[0:60]))
	return b
}

// ParseHole validates and decodes a hole record from the head of p.
func ParseHole(p []byte) (HoleInfo, bool) {
	if len(p) < HoleHeaderSize {
		return HoleInfo{}, false
	}
	if string(p[0:8]) != string(holeMagic) {
		return HoleInfo{}, false
	}
	if crc32.ChecksumIEEE(p[0:60]) != binary.LittleEndian.Uint32(p[60:64]) {
		return HoleInfo{}, false
	}
	causeLen := binary.LittleEndian.Uint32(p[28:32])
	if causeLen > holeCauseMax {
		return HoleInfo{}, false
	}
	return HoleInfo{
		Offset: binary.LittleEndian.Uint64(p[12:20]),
		Length: binary.LittleEndian.Uint64(p[20:28]),
		Reason: p[8],
		Cause:  string(p[32 : 32+causeLen]),
	}, true
}

// IsHole reports whether p starts with a valid hole record.
func IsHole(p []byte) bool {
	_, ok := ParseHole(p)
	return ok
}
