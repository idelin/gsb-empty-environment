package roaring

import (
	"encoding/binary"
	"hash/crc32"
)

// WAL record op codes.
const (
	walOpAdd      byte = 1
	walOpAddRange byte = 2
)

const maxRecordSize = 1 << 20

func encodeAddRecord(key uint16, vals []uint16) []byte {
	w := &byteWriter{}
	w.u8(walOpAdd)
	w.u16(key)
	w.u32(uint32(len(vals)))
	for _, v := range vals {
		w.u16(v)
	}
	return w.buf
}

// EncodeAddRecord builds a replication/WAL payload that inserts vals under
// the shared high-16-bit key. It can be applied to a bitmap with
// ApplyRecord.
func EncodeAddRecord(key uint16, vals ...uint16) []byte {
	return encodeAddRecord(key, vals)
}

// EncodeAddRangeRecord builds a replication/WAL payload that inserts the
// inclusive range [lo, hi] under key.
func EncodeAddRangeRecord(key uint16, lo, hi uint16) []byte {
	return encodeRangeRecord(key, lo, hi)
}

// ApplyRecord replays one record built by EncodeAddRecord or
// EncodeAddRangeRecord against bm.
func ApplyRecord(bm *Bitmap, record []byte) error {
	return applyRecord(bm, record)
}

func encodeRangeRecord(key uint16, lo, hi uint16) []byte {
	w := &byteWriter{}
	w.u8(walOpAddRange)
	w.u16(key)
	w.u16(lo)
	w.u16(hi)
	return w.buf
}

// frameRecord wraps a payload with a length prefix and a CRC32 checksum so
// that torn or corrupted tail records are detected during recovery.
func frameRecord(payload []byte) []byte {
	w := &byteWriter{}
	w.u32(uint32(len(payload)))
	w.u32(crc32.ChecksumIEEE(payload))
	w.bytes(payload)
	return w.buf
}

type walRecord struct {
	payload   []byte
	endOffset int64
}

// parseRecords decodes the valid prefix of a WAL image. Parsing stops at the
// first truncated or corrupt (CRC mismatch) record; anything beyond is a
// torn tail left behind by a crash.
func parseRecords(data []byte) []walRecord {
	var recs []walRecord
	off := 0
	for off+8 <= len(data) {
		n := binary.LittleEndian.Uint32(data[off : off+4])
		if n > maxRecordSize || off+8+int(n) > len(data) {
			break
		}
		payload := data[off+8 : off+8+int(n)]
		crc := binary.LittleEndian.Uint32(data[off+4 : off+8])
		if crc32.ChecksumIEEE(payload) != crc {
			break
		}
		recs = append(recs, walRecord{payload: payload, endOffset: int64(off + 8 + int(n))})
		off += 8 + int(n)
	}
	return recs
}

// applyRecord replays one WAL record against bm. Adds are idempotent, so
// replaying a record that is already reflected in a snapshot is harmless.
func applyRecord(bm *Bitmap, payload []byte) error {
	r := &byteReader{data: payload}
	op := r.u8()
	key := r.u16()
	switch op {
	case walOpAdd:
		n := r.u32()
		if r.err != nil || n > 1<<16 {
			return errCorrupt
		}
		for i := uint32(0); i < n; i++ {
			v := r.u16()
			if r.err != nil {
				return r.err
			}
			bm.Add(uint32(key)<<16 | uint32(v))
		}
	case walOpAddRange:
		lo := r.u16()
		hi := r.u16()
		if r.err != nil {
			return r.err
		}
		bm.AddRange(key, lo, hi)
	default:
		return errCorrupt
	}
	return nil
}
