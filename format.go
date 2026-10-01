// Package cidx implements a compact, read-only sorted-string index with a
// real binary encoding, an independent byte-layout checker, and lease-based
// mapping lifetime management.
//
// File layout (all integers little-endian):
//
//	Header (40 bytes)
//	  [0:8]   magic "CIDXIDX1"
//	  [8:12]  format version uint32 (== 1)
//	  [12:16] entry count uint32
//	  [16:24] offset table absolute offset uint64
//	  [24:32] data region absolute offset uint64
//	  [32:40] data region length uint64
//	Offset table: entryCount+1 uint32 offsets relative to data region start.
//	  Must be non-decreasing; offsets[0] == 0; offsets[last] == dataLength.
//	Data region: entryCount records. Record i spans
//	  [dataOffset+offsets[i], dataOffset+offsets[i+1]) and encodes:
//	  uvarint keyLen | key bytes | uvarint valueLen | value bytes
//	  The record must consume its extent exactly.
//	Keys are strictly increasing across records, enabling binary search.
//	The file must end exactly at dataOffset+dataLength: no trailing bytes.
package cidx

import "encoding/binary"

const (
	HeaderSize      = 40
	offsetEntrySize = 4
	// FormatVersion is the only supported on-disk version.
	FormatVersion = 1
)

var magic = [8]byte{'C', 'I', 'D', 'X', 'I', 'D', 'X', '1'}

type header struct {
	entryCount  uint32
	tableOffset uint64
	dataOffset  uint64
	dataLength  uint64
}

func encodeHeader(h header) []byte {
	buf := make([]byte, HeaderSize)
	copy(buf[0:8], magic[:])
	binary.LittleEndian.PutUint32(buf[8:12], FormatVersion)
	binary.LittleEndian.PutUint32(buf[12:16], h.entryCount)
	binary.LittleEndian.PutUint64(buf[16:24], h.tableOffset)
	binary.LittleEndian.PutUint64(buf[24:32], h.dataOffset)
	binary.LittleEndian.PutUint64(buf[32:40], h.dataLength)
	return buf
}

func decodeHeader(b []byte) (header, error) {
	var h header
	if len(b) < HeaderSize {
		return h, ErrTooShort
	}
	for i := 0; i < 8; i++ {
		if b[i] != magic[i] {
			return h, ErrBadMagic
		}
	}
	if v := binary.LittleEndian.Uint32(b[8:12]); v != FormatVersion {
		return h, ErrUnsupportedVersion
	}
	h.entryCount = binary.LittleEndian.Uint32(b[12:16])
	h.tableOffset = binary.LittleEndian.Uint64(b[16:24])
	h.dataOffset = binary.LittleEndian.Uint64(b[24:32])
	h.dataLength = binary.LittleEndian.Uint64(b[32:40])
	return h, nil
}
