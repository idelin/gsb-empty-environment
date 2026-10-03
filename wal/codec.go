package wal

import (
	"encoding/binary"
	"hash/crc32"
)

// On-disk layout (little endian), fixed 40-byte header:
//
//	[0:4]   magic "ARIS" (0x41524953)
//	[4]     format version (current = 1)
//	[5]     record type
//	[6:8]   reserved flags
//	[8:16]  LSN (byte offset of this record in the log)
//	[16:24] transaction ID
//	[24:32] prevLSN of the transaction
//	[32:36] payload length in bytes
//	[36:40] crc32-IEEE of header[0:36] || payload
//
// Payloads:
//
//	UPDATE:     pageID u64 | offset u16 | beforeLen u16 | afterLen u16 | before | after
//	CLR:        pageID u64 | undoNext u64 | offset u16 | beforeLen u16 | afterLen u16 | before | after
//	CHECKPOINT: txnCount u32 | (txnID u64 | lastLSN u64 | status u8)* |
//	            pageCount u32 | (pageID u64 | recLSN u64)*
//	others:     empty
//
// Compatibility: readers accept any version >= minVersion. Records written by
// a *newer* binary (version > FormatVersion) are still decoded as long as the
// fixed header and known payload prefix parse; unknown trailing payload bytes
// are skipped, giving forward compatibility. Versions below minVersion are
// rejected explicitly (backward-compatibility floor).
const (
	magic         = 0x41524953 // "ARIS"
	HeaderSize    = 40
	FormatVersion = 1
	minVersion    = 1

	// MaxPayload bounds a single record so a corrupt length field cannot
	// trigger an out-of-range allocation or integer overflow.
	MaxPayload = 1 << 26 // 64 MiB

	maxImage = 0xFFFF
)

var le = binary.LittleEndian

// EncodedSize returns the number of bytes Encode will emit for r.
func EncodedSize(r *Record) int {
	return HeaderSize + payloadSize(r)
}

func payloadSize(r *Record) int {
	switch r.Type {
	case RecUpdate:
		return 14 + len(r.Before) + len(r.After)
	case RecCLR:
		return 22 + len(r.Before) + len(r.After)
	case RecCheckpoint:
		return 4 + len(r.Txns)*17 + 4 + len(r.Pages)*16
	default:
		return 0
	}
}

// Encode appends the binary form of r to dst and returns the extended slice.
// r.LSN is stamped into the header as-is; the Writer sets it before calling.
func Encode(dst []byte, r *Record) ([]byte, error) {
	if len(r.Before) > maxImage || len(r.After) > maxImage {
		return nil, ErrImageTooBig
	}
	switch r.Type {
	case RecUpdate, RecCommit, RecAbort, RecCLR, RecCheckpoint, RecEnd:
	default:
		return nil, ErrBadType
	}
	size := EncodedSize(r)
	base := len(dst)
	dst = append(dst, make([]byte, size)...)
	buf := dst[base:]

	le.PutUint32(buf[0:4], magic)
	buf[4] = FormatVersion
	buf[5] = byte(r.Type)
	le.PutUint16(buf[6:8], 0)
	le.PutUint64(buf[8:16], r.LSN)
	le.PutUint64(buf[16:24], r.TxnID)
	le.PutUint64(buf[24:32], r.PrevLSN)
	le.PutUint32(buf[32:36], uint32(size-HeaderSize))

	p := buf[HeaderSize:]
	switch r.Type {
	case RecUpdate:
		le.PutUint64(p[0:8], r.PageID)
		le.PutUint16(p[8:10], r.Offset)
		le.PutUint16(p[10:12], uint16(len(r.Before)))
		le.PutUint16(p[12:14], uint16(len(r.After)))
		copy(p[14:], r.Before)
		copy(p[14+len(r.Before):], r.After)
	case RecCLR:
		le.PutUint64(p[0:8], r.PageID)
		le.PutUint64(p[8:16], r.UndoNext)
		le.PutUint16(p[16:18], r.Offset)
		le.PutUint16(p[18:20], uint16(len(r.Before)))
		le.PutUint16(p[20:22], uint16(len(r.After)))
		copy(p[22:], r.Before)
		copy(p[22+len(r.Before):], r.After)
	case RecCheckpoint:
		le.PutUint32(p[0:4], uint32(len(r.Txns)))
		off := 4
		for _, t := range r.Txns {
			le.PutUint64(p[off:off+8], t.TxnID)
			le.PutUint64(p[off+8:off+16], t.LastLSN)
			p[off+16] = byte(t.Status)
			off += 17
		}
		le.PutUint32(p[off:off+4], uint32(len(r.Pages)))
		off += 4
		for _, pg := range r.Pages {
			le.PutUint64(p[off:off+8], pg.PageID)
			le.PutUint64(p[off+8:off+16], pg.RecLSN)
			off += 16
		}
	}
	le.PutUint32(buf[36:40], crc32.ChecksumIEEE(buf[0:36]))
	return dst, nil
}

// Decode parses one record from the front of buf. It returns the record and
// the total number of bytes consumed (header + payload). The returned
// Before/After slices alias buf (zero-copy).
//
// Decode never panics and never allocates proportional to untrusted length
// fields: every read is bounds-checked first.
func Decode(buf []byte) (*Record, int, error) {
	if len(buf) < HeaderSize {
		return nil, 0, ErrShortHeader
	}
	if le.Uint32(buf[0:4]) != magic {
		return nil, 0, ErrBadMagic
	}
	version := buf[4]
	if version < minVersion {
		return nil, 0, ErrBadVersion
	}
	payloadLen := le.Uint32(buf[32:36])
	if payloadLen > MaxPayload {
		return nil, 0, ErrCorrupt
	}
	total := HeaderSize + int(payloadLen)
	if len(buf) < total {
		return nil, 0, ErrShortPayload
	}
	if crc32.ChecksumIEEE(buf[0:36]) != le.Uint32(buf[36:40]) {
		return nil, 0, ErrBadCRC
	}

	r := &Record{
		LSN:     le.Uint64(buf[8:16]),
		Type:    RecType(buf[5]),
		TxnID:   le.Uint64(buf[16:24]),
		PrevLSN: le.Uint64(buf[24:32]),
	}
	p := buf[HeaderSize:total]
	if err := decodePayload(r, p); err != nil {
		return nil, 0, err
	}
	return r, total, nil
}

// decodePayload parses the known prefix of p. Extra trailing bytes (written
// by a newer format version) are ignored for forward compatibility.
func decodePayload(r *Record, p []byte) error {
	switch r.Type {
	case RecCommit, RecAbort, RecEnd:
		return nil
	case RecUpdate, RecCLR:
		fixed := 14
		if r.Type == RecCLR {
			fixed = 22
		}
		if len(p) < fixed {
			return ErrCorrupt
		}
		r.PageID = le.Uint64(p[0:8])
		var off int
		if r.Type == RecCLR {
			r.UndoNext = le.Uint64(p[8:16])
			r.Offset = le.Uint16(p[16:18])
			off = 18
		} else {
			r.Offset = le.Uint16(p[8:10])
			off = 10
		}
		beforeLen := int(le.Uint16(p[off : off+2]))
		afterLen := int(le.Uint16(p[off+2 : off+4]))
		off += 4
		if len(p)-off < beforeLen+afterLen {
			return ErrCorrupt
		}
		r.Before = p[off : off+beforeLen]
		r.After = p[off+beforeLen : off+beforeLen+afterLen]
		return nil
	case RecCheckpoint:
		if len(p) < 4 {
			return ErrCorrupt
		}
		nTxn := int(le.Uint32(p[0:4]))
		off := 4
		if nTxn > (len(p)-off)/17 {
			return ErrCorrupt
		}
		if nTxn > 0 {
			r.Txns = make([]CheckpointTxn, nTxn)
		}
		for i := 0; i < nTxn; i++ {
			r.Txns[i] = CheckpointTxn{
				TxnID:   le.Uint64(p[off : off+8]),
				LastLSN: le.Uint64(p[off+8 : off+16]),
				Status:  TxnStatus(p[off+16]),
			}
			off += 17
		}
		if len(p)-off < 4 {
			return ErrCorrupt
		}
		nPage := int(le.Uint32(p[off : off+4]))
		off += 4
		if nPage > (len(p)-off)/16 {
			return ErrCorrupt
		}
		if nPage > 0 {
			r.Pages = make([]CheckpointPage, nPage)
		}
		for i := 0; i < nPage; i++ {
			r.Pages[i] = CheckpointPage{
				PageID: le.Uint64(p[off : off+8]),
				RecLSN: le.Uint64(p[off+8 : off+16]),
			}
			off += 16
		}
		return nil
	default:
		return ErrBadType
	}
}
