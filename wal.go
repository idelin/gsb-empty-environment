package pagestore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// WAL record layout:
//
//	magic(4) | length(4) | lsn(8) | txn(8) | type(1) | pageID(8) | payload(n) | crc32(4)
//
// length counts the bytes from lsn through payload. crc32 covers the same
// range. The magic prefix lets the scanner resynchronize after a corrupt
// record to decide whether the damage is a torn tail (nothing valid after
// it) or mid-log corruption (valid records follow, which is fatal).
const recMagic uint32 = 0x57414C52 // "WALR"

type recType byte

const (
	recBegin  recType = 1
	recPage   recType = 2 // payload is a full PageSize page image (physical logging)
	recCommit recType = 3
)

type record struct {
	lsn    uint64
	txn    uint64
	typ    recType
	pageID uint64
	data   []byte
}

const recBodyFixed = 8 + 8 + 1 + 8 // lsn, txn, type, pageID

func encodeRecord(r record) []byte {
	n := recBodyFixed + len(r.data)
	buf := make([]byte, 8+n+4)
	binary.LittleEndian.PutUint32(buf[0:4], recMagic)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(n))
	binary.LittleEndian.PutUint64(buf[8:16], r.lsn)
	binary.LittleEndian.PutUint64(buf[16:24], r.txn)
	buf[24] = byte(r.typ)
	binary.LittleEndian.PutUint64(buf[25:33], r.pageID)
	copy(buf[33:], r.data)
	binary.LittleEndian.PutUint32(buf[8+n:], crc32.ChecksumIEEE(buf[8:8+n]))
	return buf
}

var (
	errTruncatedRecord = errors.New("wal: truncated record")
	errBadMagic        = errors.New("wal: bad record magic")
	errBadLength       = errors.New("wal: bad record length")
	errBadCRC          = errors.New("wal: record checksum mismatch")
)

// parseRecord parses the record starting at buf[off]. It returns the record
// and its total encoded size, or an error classifying the failure.
func parseRecord(buf []byte, off int) (record, int, error) {
	var r record
	if len(buf)-off < 8 {
		return r, 0, errTruncatedRecord
	}
	if binary.LittleEndian.Uint32(buf[off:off+4]) != recMagic {
		return r, 0, errBadMagic
	}
	n := int(binary.LittleEndian.Uint32(buf[off+4 : off+8]))
	if n < recBodyFixed || n > recBodyFixed+PageSize {
		return r, 0, fmt.Errorf("%w: %d", errBadLength, n)
	}
	if len(buf)-off < 8+n+4 {
		return r, 0, errTruncatedRecord
	}
	body := buf[off+8 : off+8+n]
	if crc32.ChecksumIEEE(body) != binary.LittleEndian.Uint32(buf[off+8+n:]) {
		return r, 0, errBadCRC
	}
	r.lsn = binary.LittleEndian.Uint64(body[0:8])
	r.txn = binary.LittleEndian.Uint64(body[8:16])
	r.typ = recType(body[16])
	r.pageID = binary.LittleEndian.Uint64(body[17:25])
	if r.typ == recPage && len(body[25:]) != PageSize {
		return r, 0, fmt.Errorf("%w: page record payload %d", errBadLength, len(body[25:]))
	}
	r.data = append([]byte(nil), body[25:]...)
	return r, 8 + n + 4, nil
}

// findValidRecord searches for the next offset >= from that begins a fully
// valid record (magic + length + CRC all check out). Returns -1 if none.
func findValidRecord(buf []byte, from int) int {
	for i := from; i+4 <= len(buf); i++ {
		if binary.LittleEndian.Uint32(buf[i:i+4]) != recMagic {
			continue
		}
		if _, _, err := parseRecord(buf, i); err == nil {
			return i
		}
	}
	return -1
}

// scanWAL parses a WAL image. It returns the valid records and the length of
// the valid prefix. A torn tail (crash during append: partial record, or
// garbage with no valid record after it) is reported via validLen < len(data)
// and is not an error. Corruption followed by more valid records cannot be a
// torn tail and is returned as a hard error.
func scanWAL(data []byte) (recs []record, validLen int, err error) {
	off := 0
	for off < len(data) {
		r, size, perr := parseRecord(data, off)
		if perr == nil {
			recs = append(recs, r)
			off += size
			continue
		}
		if next := findValidRecord(data, off+1); next >= 0 {
			return nil, 0, fmt.Errorf("wal: corrupt record at offset %d (%v); valid records follow at offset %d, not a torn tail", off, perr, next)
		}
		return recs, off, nil
	}
	return recs, off, nil
}
