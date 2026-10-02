// Package wal implements the write-ahead log used by the ARIES recovery
// engine: log record types, binary framing with CRC32 checksums, a
// buffered WAL writer with fault injection hooks, and a lossy log
// shipper used to emulate network packet loss.
package wal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// RecordType identifies the kind of a log record.
type RecordType uint8

const (
	TypeInvalid RecordType = iota
	TypeBegin
	TypeUpdate
	TypeCommit
	TypeAbort
	TypeEnd
	TypeCLR
	TypeCheckpoint
)

func (t RecordType) String() string {
	switch t {
	case TypeBegin:
		return "BEGIN"
	case TypeUpdate:
		return "UPDATE"
	case TypeCommit:
		return "COMMIT"
	case TypeAbort:
		return "ABORT"
	case TypeEnd:
		return "END"
	case TypeCLR:
		return "CLR"
	case TypeCheckpoint:
		return "CHECKPOINT"
	}
	return "INVALID"
}

// Record is a single ARIES log record.
type Record struct {
	LSN     uint64
	PrevLSN uint64 // previous LSN of the same transaction (0 = none)
	TxnID   uint64
	Type    RecordType

	// Update / CLR payload.
	PageID  uint32
	Offset  uint32
	OldData []byte // before-image (UPDATE) / undone-image (CLR)
	NewData []byte // after-image (UPDATE) / redo-of-undo image (CLR)

	// UndoNextLSN is only meaningful for CLR records: it points to the
	// next record of the transaction that still has to be undone.
	UndoNextLSN uint64

	// Checkpoint payload.
	DirtyPages map[uint32]uint64 // pageID -> recLSN
	Txns       map[uint64]uint64 // txnID  -> lastLSN
}

func (r *Record) String() string {
	return fmt.Sprintf("LSN=%d %s txn=%d prev=%d page=%d off=%d undoNext=%d",
		r.LSN, r.Type, r.TxnID, r.PrevLSN, r.PageID, r.Offset, r.UndoNextLSN)
}

// headerSize is the fixed part of an encoded record body:
// LSN, PrevLSN, TxnID (8 each) + Type (1) + PageID, Offset (4 each) +
// UndoNextLSN (8) + len(OldData), len(NewData) (4 each).
const headerSize = 8 + 8 + 8 + 1 + 4 + 4 + 8 + 4 + 4

// ErrCorrupt is returned when a framed record is truncated or fails its
// CRC check. Scanners treat it as the end of the durable log.
var ErrCorrupt = errors.New("wal: corrupt or truncated record")

// Encode serializes the record as: [uint32 bodyLen][body][uint32 crc32(body)].
func (r *Record) Encode() []byte {
	body := make([]byte, 0, headerSize+len(r.OldData)+len(r.NewData)+64)
	put64 := func(v uint64) {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], v)
		body = append(body, b[:]...)
	}
	put32 := func(v uint32) {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], v)
		body = append(body, b[:]...)
	}
	put64(r.LSN)
	put64(r.PrevLSN)
	put64(r.TxnID)
	body = append(body, byte(r.Type))
	put32(r.PageID)
	put32(r.Offset)
	put64(r.UndoNextLSN)
	put32(uint32(len(r.OldData)))
	put32(uint32(len(r.NewData)))
	body = append(body, r.OldData...)
	body = append(body, r.NewData...)
	if r.Type == TypeCheckpoint {
		put32(uint32(len(r.DirtyPages)))
		for pid, lsn := range r.DirtyPages {
			put32(pid)
			put64(lsn)
		}
		put32(uint32(len(r.Txns)))
		for tid, lsn := range r.Txns {
			put64(tid)
			put64(lsn)
		}
	}
	frame := make([]byte, 4, len(body)+8)
	binary.LittleEndian.PutUint32(frame, uint32(len(body)))
	frame = append(frame, body...)
	var crc [4]byte
	binary.LittleEndian.PutUint32(crc[:], crc32.ChecksumIEEE(body))
	frame = append(frame, crc[:]...)
	return frame
}

// DecodeFrame decodes one framed record from the head of b and returns the
// record plus the total number of bytes consumed (length prefix + body +
// CRC). It returns ErrCorrupt when the frame is incomplete or invalid.
func DecodeFrame(b []byte) (*Record, int, error) {
	if len(b) < 4 {
		return nil, 0, ErrCorrupt
	}
	bodyLen := int(binary.LittleEndian.Uint32(b))
	if bodyLen < headerSize || len(b) < 4+bodyLen+4 {
		return nil, 0, ErrCorrupt
	}
	body := b[4 : 4+bodyLen]
	wantCRC := binary.LittleEndian.Uint32(b[4+bodyLen:])
	if crc32.ChecksumIEEE(body) != wantCRC {
		return nil, 0, ErrCorrupt
	}
	r := &Record{}
	off := 0
	u64 := func() uint64 {
		v := binary.LittleEndian.Uint64(body[off:])
		off += 8
		return v
	}
	u32 := func() uint32 {
		v := binary.LittleEndian.Uint32(body[off:])
		off += 4
		return v
	}
	r.LSN = u64()
	r.PrevLSN = u64()
	r.TxnID = u64()
	r.Type = RecordType(body[off])
	off++
	r.PageID = u32()
	r.Offset = u32()
	r.UndoNextLSN = u64()
	oldLen := int(u32())
	newLen := int(u32())
	if oldLen < 0 || newLen < 0 || off+oldLen+newLen > len(body) {
		return nil, 0, ErrCorrupt
	}
	if oldLen > 0 {
		r.OldData = append([]byte(nil), body[off:off+oldLen]...)
		off += oldLen
	}
	if newLen > 0 {
		r.NewData = append([]byte(nil), body[off:off+newLen]...)
		off += newLen
	}
	if r.Type == TypeCheckpoint {
		if off+4 > len(body) {
			return nil, 0, ErrCorrupt
		}
		n := int(u32())
		r.DirtyPages = make(map[uint32]uint64, n)
		for i := 0; i < n; i++ {
			if off+12 > len(body) {
				return nil, 0, ErrCorrupt
			}
			pid := u32()
			r.DirtyPages[pid] = u64()
		}
		if off+4 > len(body) {
			return nil, 0, ErrCorrupt
		}
		m := int(u32())
		r.Txns = make(map[uint64]uint64, m)
		for i := 0; i < m; i++ {
			if off+16 > len(body) {
				return nil, 0, ErrCorrupt
			}
			tid := u64()
			r.Txns[tid] = u64()
		}
	}
	return r, 4 + bodyLen + 4, nil
}
