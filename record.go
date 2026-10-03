// Package aries implements the classic ARIES crash-recovery protocol:
// a write-ahead log (WAL) with a compact binary encoding, zero-copy
// in-memory parsing, and the three recovery phases (Analysis, Redo, Undo).
package aries

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Log record types.
const (
	TypeInvalid         byte = 0
	TypeUpdate          byte = 1
	TypeCLR             byte = 2
	TypeCommit          byte = 3
	TypeAbort           byte = 4
	TypeEnd             byte = 5
	TypeBeginCheckpoint byte = 6
	TypeEndCheckpoint   byte = 7
)

const (
	// FileMagic identifies an ARIES WAL file ("ARIS").
	FileMagic uint32 = 0x41524953
	// Version1 is the original format: CLR records carry no undoNextLSN.
	Version1 uint16 = 1
	// Version2 adds undoNextLSN to CLR records.
	Version2       uint16 = 2
	CurrentVersion        = Version2

	// FileHeaderSize is magic(4) + version(2) + flags(2).
	FileHeaderSize = 8
	// recordHeaderSize is type(1) + payloadLen(4).
	recordHeaderSize = 5
)

var (
	ErrShortBuffer        = errors.New("aries: buffer too short for record header")
	ErrPayloadOverflow    = errors.New("aries: record payload length exceeds buffer")
	ErrMalformed          = errors.New("aries: malformed record payload")
	ErrBadMagic           = errors.New("aries: bad file magic")
	ErrUnsupportedVersion = errors.New("aries: unsupported wal version")
	ErrClosed             = errors.New("aries: resource is closed")
)

// TxnSnapshot is a transaction-table entry stored inside an end-checkpoint.
type TxnSnapshot struct {
	TxnID   uint64
	LastLSN uint64
	Status  byte
}

// DirtyPageSnapshot is a dirty-page-table entry stored inside an end-checkpoint.
type DirtyPageSnapshot struct {
	PageID uint32
	RecLSN uint64
}

// Record is a decoded WAL record. OldData/NewData are zero-copy views into
// the source buffer passed to DecodeRecord; they must not be retained beyond
// the lifetime of that buffer (copy them if persistence is needed).
type Record struct {
	Type    byte
	LSN     uint64 // file offset, assigned by the WAL on append / by the scanner
	TxnID   uint64
	PrevLSN uint64

	// UPDATE / CLR payload.
	PageID  uint32
	Offset  uint32
	OldData []byte
	NewData []byte

	// CLR only: LSN of the next record to undo (v2+).
	UndoNextLSN uint64

	// End-checkpoint payload.
	BeginLSN   uint64
	Txns       []TxnSnapshot
	DirtyPages []DirtyPageSnapshot
}

func (r *Record) payloadSize() int {
	switch r.Type {
	case TypeUpdate:
		return 32 + len(r.OldData) + len(r.NewData)
	case TypeCLR:
		return 40 + len(r.OldData) + len(r.NewData)
	case TypeCommit, TypeAbort, TypeEnd:
		return 16
	case TypeBeginCheckpoint:
		return 0
	case TypeEndCheckpoint:
		return 16 + len(r.Txns)*17 + len(r.DirtyPages)*12
	default:
		return 0
	}
}

// Encode serializes the record (header + payload) into a fresh buffer.
// The LSN field is not part of the encoding: it is the record's file offset.
func (r *Record) Encode() []byte {
	size := r.payloadSize()
	buf := make([]byte, recordHeaderSize+size)
	buf[0] = r.Type
	binary.BigEndian.PutUint32(buf[1:5], uint32(size))
	p := buf[recordHeaderSize:]
	switch r.Type {
	case TypeUpdate, TypeCLR:
		binary.BigEndian.PutUint64(p[0:8], r.TxnID)
		binary.BigEndian.PutUint64(p[8:16], r.PrevLSN)
		binary.BigEndian.PutUint32(p[16:20], r.PageID)
		binary.BigEndian.PutUint32(p[20:24], r.Offset)
		binary.BigEndian.PutUint32(p[24:28], uint32(len(r.OldData)))
		binary.BigEndian.PutUint32(p[28:32], uint32(len(r.NewData)))
		n := copy(p[32:], r.OldData)
		n += copy(p[32+n:], r.NewData)
		if r.Type == TypeCLR {
			binary.BigEndian.PutUint64(p[32+n:40+n], r.UndoNextLSN)
		}
	case TypeCommit, TypeAbort, TypeEnd:
		binary.BigEndian.PutUint64(p[0:8], r.TxnID)
		binary.BigEndian.PutUint64(p[8:16], r.PrevLSN)
	case TypeEndCheckpoint:
		binary.BigEndian.PutUint64(p[0:8], r.BeginLSN)
		binary.BigEndian.PutUint32(p[8:12], uint32(len(r.Txns)))
		o := 12
		for _, t := range r.Txns {
			binary.BigEndian.PutUint64(p[o:o+8], t.TxnID)
			binary.BigEndian.PutUint64(p[o+8:o+16], t.LastLSN)
			p[o+16] = t.Status
			o += 17
		}
		binary.BigEndian.PutUint32(p[o:o+4], uint32(len(r.DirtyPages)))
		o += 4
		for _, d := range r.DirtyPages {
			binary.BigEndian.PutUint32(p[o:o+4], d.PageID)
			binary.BigEndian.PutUint64(p[o+4:o+12], d.RecLSN)
			o += 12
		}
	}
	return buf
}

// DecodeRecord decodes one record from the head of buf. It returns the
// record and the total number of bytes consumed (header + payload).
//
// Zero-copy: OldData/NewData sub-slice buf directly; no payload bytes are
// copied or allocated.
//
// Compatibility:
//   - forward: payloads with trailing unknown bytes (written by a newer
//     version) are accepted; the extra bytes are skipped via the length
//     prefix. Unknown record types are returned with only Type decoded, so
//     scanners can skip them by length.
//   - backward: v1 CLR records (no undoNextLSN) default UndoNextLSN to
//     PrevLSN, matching the pre-v2 undo-chain semantics.
//
// All length fields are validated against the buffer before slicing, so a
// corrupt or adversarial log cannot cause an out-of-bounds access.
func DecodeRecord(buf []byte, version uint16) (*Record, int, error) {
	if len(buf) < recordHeaderSize {
		return nil, 0, ErrShortBuffer
	}
	typ := buf[0]
	payloadLen := binary.BigEndian.Uint32(buf[1:5])
	if uint64(payloadLen) > uint64(len(buf))-recordHeaderSize {
		return nil, 0, ErrPayloadOverflow
	}
	payload := buf[recordHeaderSize : recordHeaderSize+int(payloadLen)]
	rec := &Record{Type: typ}
	var err error
	switch typ {
	case TypeUpdate, TypeCLR:
		err = decodeUpdatePayload(rec, payload, version)
	case TypeCommit, TypeAbort, TypeEnd:
		err = decodeTxnPayload(rec, payload)
	case TypeBeginCheckpoint:
		// empty payload
	case TypeEndCheckpoint:
		err = decodeEndCheckpointPayload(rec, payload)
	default:
		// Unknown record type from a newer version: skip by length.
	}
	if err != nil {
		return nil, 0, err
	}
	return rec, recordHeaderSize + int(payloadLen), nil
}

func decodeUpdatePayload(rec *Record, p []byte, version uint16) error {
	if len(p) < 32 {
		return fmt.Errorf("%w: update payload %d bytes, need >= 32", ErrMalformed, len(p))
	}
	rec.TxnID = binary.BigEndian.Uint64(p[0:8])
	rec.PrevLSN = binary.BigEndian.Uint64(p[8:16])
	rec.PageID = binary.BigEndian.Uint32(p[16:20])
	rec.Offset = binary.BigEndian.Uint32(p[20:24])
	oldLen := binary.BigEndian.Uint32(p[24:28])
	newLen := binary.BigEndian.Uint32(p[28:32])
	if uint64(oldLen)+uint64(newLen) > uint64(len(p))-32 {
		return fmt.Errorf("%w: data lengths %d+%d exceed payload %d", ErrMalformed, oldLen, newLen, len(p)-32)
	}
	rec.OldData = p[32 : 32+oldLen]
	rec.NewData = p[32+oldLen : 32+oldLen+newLen]
	if rec.Type == TypeCLR {
		rest := len(p) - 32 - int(oldLen) - int(newLen)
		switch {
		case rest >= 8:
			rec.UndoNextLSN = binary.BigEndian.Uint64(p[32+oldLen+newLen : 40+oldLen+newLen])
		case rest == 0:
			// v1 CLR: no undoNextLSN field; fall back to the prevLSN chain.
			_ = version
			rec.UndoNextLSN = rec.PrevLSN
		default:
			return fmt.Errorf("%w: truncated undoNextLSN (%d bytes)", ErrMalformed, rest)
		}
	}
	return nil
}

func decodeTxnPayload(rec *Record, p []byte) error {
	if len(p) < 16 {
		return fmt.Errorf("%w: txn payload %d bytes, need >= 16", ErrMalformed, len(p))
	}
	rec.TxnID = binary.BigEndian.Uint64(p[0:8])
	rec.PrevLSN = binary.BigEndian.Uint64(p[8:16])
	return nil
}

func decodeEndCheckpointPayload(rec *Record, p []byte) error {
	if len(p) < 16 {
		return fmt.Errorf("%w: end-checkpoint payload %d bytes, need >= 16", ErrMalformed, len(p))
	}
	rec.BeginLSN = binary.BigEndian.Uint64(p[0:8])
	txnCount := binary.BigEndian.Uint32(p[8:12])
	if uint64(txnCount)*17 > uint64(len(p))-12 {
		return fmt.Errorf("%w: txn count %d exceeds payload", ErrMalformed, txnCount)
	}
	o := 12
	rec.Txns = make([]TxnSnapshot, 0, txnCount)
	for i := uint32(0); i < txnCount; i++ {
		rec.Txns = append(rec.Txns, TxnSnapshot{
			TxnID:   binary.BigEndian.Uint64(p[o : o+8]),
			LastLSN: binary.BigEndian.Uint64(p[o+8 : o+16]),
			Status:  p[o+16],
		})
		o += 17
	}
	if len(p)-o < 4 {
		return fmt.Errorf("%w: missing dirty-page count", ErrMalformed)
	}
	dptCount := binary.BigEndian.Uint32(p[o : o+4])
	o += 4
	if uint64(dptCount)*12 > uint64(len(p))-uint64(o) {
		return fmt.Errorf("%w: dirty-page count %d exceeds payload", ErrMalformed, dptCount)
	}
	rec.DirtyPages = make([]DirtyPageSnapshot, 0, dptCount)
	for i := uint32(0); i < dptCount; i++ {
		rec.DirtyPages = append(rec.DirtyPages, DirtyPageSnapshot{
			PageID: binary.BigEndian.Uint32(p[o : o+4]),
			RecLSN: binary.BigEndian.Uint64(p[o+4 : o+12]),
		})
		o += 12
	}
	return nil
}
