// Package sparseidx implements an append-only store of variable-length
// records with a sparse offset index. Records live in immutable blocks;
// updates (put / tombstone / patch) are appended as new records and new
// blocks, never rewritten in place. The sparse index maps every Nth record
// to a (block, offset) pair and validates record boundaries on use instead
// of blindly trusting offsets.
package sparseidx

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// FormatVersion is written into every block, index and manifest file.
const FormatVersion uint32 = 1

// Record flags.
const (
	FlagPut       byte = 0 // full key/value upsert
	FlagTombstone byte = 1 // logical delete of the key
	FlagPatch     byte = 2 // partial replacement of the current value
)

// Record wire layout (all integers big-endian):
//
//	u32  crc32(body)   where body is everything after this field
//	u8   flags
//	u16  keyLen
//	u32  valueLen
//	[keyLen bytes]  key
//	[valueLen bytes] value
const recordHeaderSize = 4 + 1 + 2 + 4

var (
	// ErrCorrupt reports structurally invalid or checksum-mismatched data,
	// including an index offset that does not land on a record boundary.
	ErrCorrupt = errors.New("sparseidx: corrupt data")
	// ErrTruncated reports a partial (half-written) record.
	ErrTruncated = errors.New("sparseidx: truncated record")
	// ErrTooLarge reports a record that exceeds the configured decode budget.
	ErrTooLarge = errors.New("sparseidx: record exceeds decode limit")
	// ErrNotFound reports a missing key or an out-of-range record index.
	ErrNotFound = errors.New("sparseidx: not found")
)

// Record is one variable-length entry.
type Record struct {
	Flags byte
	Key   []byte
	Value []byte
}

// appendRecord encodes rec onto dst and returns the extended buffer.
func appendRecord(dst []byte, rec Record) []byte {
	start := len(dst)
	dst = append(dst, make([]byte, recordHeaderSize)...)
	dst[start+4] = rec.Flags
	binary.BigEndian.PutUint16(dst[start+5:], uint16(len(rec.Key)))
	binary.BigEndian.PutUint32(dst[start+7:], uint32(len(rec.Value)))
	dst = append(dst, rec.Key...)
	dst = append(dst, rec.Value...)
	binary.BigEndian.PutUint32(dst[start:], crc32.ChecksumIEEE(dst[start+4:]))
	return dst
}

// decodeRecordAt decodes the record starting exactly at data[off]. It never
// allocates more than maxBytes for the payload, so a hostile length field
// cannot cause an unbounded allocation. It returns the record and the offset
// of the next record.
func decodeRecordAt(data []byte, off int, maxBytes int) (Record, int, error) {
	if off < 0 || off+recordHeaderSize > len(data) {
		return Record{}, 0, ErrTruncated
	}
	wantCRC := binary.BigEndian.Uint32(data[off:])
	flags := data[off+4]
	keyLen := int(binary.BigEndian.Uint16(data[off+5:]))
	valLen := int(binary.BigEndian.Uint32(data[off+7:]))
	if maxBytes > 0 && keyLen+valLen > maxBytes {
		return Record{}, 0, ErrTooLarge
	}
	total := recordHeaderSize + keyLen + valLen
	if off+total > len(data) {
		return Record{}, 0, ErrTruncated
	}
	body := data[off+4 : off+total]
	if crc32.ChecksumIEEE(body) != wantCRC {
		return Record{}, 0, ErrCorrupt
	}
	rec := Record{
		Flags: flags,
		Key:   body[7 : 7+keyLen],
		Value: body[7+keyLen:],
	}
	return rec, off + total, nil
}

// encodePatch builds the value payload of a FlagPatch record:
// u64 offset || replacement bytes.
func encodePatch(offset uint64, data []byte) []byte {
	buf := make([]byte, 8, 8+len(data))
	binary.BigEndian.PutUint64(buf, offset)
	return append(buf, data...)
}

// applyRecord folds one record into the live key/value view.
func applyRecord(live map[string][]byte, rec Record) error {
	switch rec.Flags {
	case FlagPut:
		live[string(rec.Key)] = append([]byte(nil), rec.Value...)
	case FlagTombstone:
		delete(live, string(rec.Key))
	case FlagPatch:
		if len(rec.Value) < 8 {
			return ErrCorrupt
		}
		off := binary.BigEndian.Uint64(rec.Value)
		patch := rec.Value[8:]
		cur := live[string(rec.Key)]
		if off > uint64(len(cur)) {
			return ErrCorrupt
		}
		end := int(off) + len(patch)
		next := make([]byte, 0, len(cur)+len(patch))
		next = append(next, cur[:off]...)
		next = append(next, patch...)
		if end < len(cur) {
			next = append(next, cur[end:]...)
		}
		live[string(rec.Key)] = next
	default:
		return ErrCorrupt
	}
	return nil
}
