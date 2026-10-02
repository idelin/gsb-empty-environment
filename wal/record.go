// Package wal defines the ARIES write-ahead log record format and a
// lock-free, CAS-accelerated log appender.
package wal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// LSN is a log sequence number: the logical position of a record in the WAL.
type LSN = uint64

// TxnID identifies a transaction.
type TxnID = uint64

// PageID identifies a database page.
type PageID = uint64

// RecordType enumerates ARIES log record kinds.
type RecordType uint8

const (
	TypeInvalid RecordType = iota
	TypeUpdate             // regular update: carries before/after images
	TypeCommit
	TypeAbort
	TypeEnd        // transaction fully finished (after commit or undo)
	TypeCLR        // compensation log record written during undo
	TypeCheckpoint // end-checkpoint: snapshot of TT + DPT
)

func (t RecordType) String() string {
	switch t {
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
	default:
		return "INVALID"
	}
}

// TxnStatus is the analysis-phase state of a transaction.
type TxnStatus uint8

const (
	StatusRunning TxnStatus = iota
	StatusCommitted
	StatusAborting
)

func (s TxnStatus) String() string {
	switch s {
	case StatusRunning:
		return "RUNNING"
	case StatusCommitted:
		return "COMMITTED"
	case StatusAborting:
		return "ABORTING"
	default:
		return "?"
	}
}

// TxnEntry is one row of the transaction table.
type TxnEntry struct {
	Status  TxnStatus
	LastLSN LSN
}

// DirtyPageTable maps a page to the oldest LSN that dirtied it (recLSN).
type DirtyPageTable map[PageID]LSN

// TxnTable maps a live transaction to its analysis state.
type TxnTable map[TxnID]*TxnEntry

// Record is a single WAL record.
type Record struct {
	LSN      LSN
	TxnID    TxnID
	Type     RecordType
	PrevLSN  LSN // previous record of the same transaction (0 = none)
	PageID   PageID
	UndoNext LSN // CLR only: next record to undo (0 = undo complete)
	Before   []byte
	After    []byte
	// Checkpoint payload (TypeCheckpoint only).
	ChkTT  TxnTable
	ChkDPT DirtyPageTable
}

// fixed header: LSN, TxnID, Type, PrevLSN, PageID, UndoNext,
// beforeLen, afterLen
const headerSize = 8 + 8 + 1 + 8 + 8 + 8 + 4 + 4

// Marshal encodes the record to its binary form.
func (r *Record) Marshal() []byte {
	body := headerSize + len(r.Before) + len(r.After)
	if r.Type == TypeCheckpoint {
		body += checkpointPayloadSize(r.ChkTT, r.ChkDPT)
	}
	buf := make([]byte, 4+body) // 4-byte length prefix
	binary.LittleEndian.PutUint32(buf[0:4], uint32(body))
	off := 4
	binary.LittleEndian.PutUint64(buf[off:], r.LSN)
	off += 8
	binary.LittleEndian.PutUint64(buf[off:], r.TxnID)
	off += 8
	buf[off] = byte(r.Type)
	off++
	binary.LittleEndian.PutUint64(buf[off:], r.PrevLSN)
	off += 8
	binary.LittleEndian.PutUint64(buf[off:], r.PageID)
	off += 8
	binary.LittleEndian.PutUint64(buf[off:], r.UndoNext)
	off += 8
	binary.LittleEndian.PutUint32(buf[off:], uint32(len(r.Before)))
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(len(r.After)))
	off += 4
	off += copy(buf[off:], r.Before)
	off += copy(buf[off:], r.After)
	if r.Type == TypeCheckpoint {
		off += marshalCheckpoint(buf[off:], r.ChkTT, r.ChkDPT)
	}
	return buf
}

func checkpointPayloadSize(tt TxnTable, dpt DirtyPageTable) int {
	return 4 + len(tt)*(8+1+8) + 4 + len(dpt)*(8+8)
}

func marshalCheckpoint(dst []byte, tt TxnTable, dpt DirtyPageTable) int {
	off := 0
	binary.LittleEndian.PutUint32(dst[off:], uint32(len(tt)))
	off += 4
	for id, e := range tt {
		binary.LittleEndian.PutUint64(dst[off:], id)
		off += 8
		dst[off] = byte(e.Status)
		off++
		binary.LittleEndian.PutUint64(dst[off:], e.LastLSN)
		off += 8
	}
	binary.LittleEndian.PutUint32(dst[off:], uint32(len(dpt)))
	off += 4
	for pid, lsn := range dpt {
		binary.LittleEndian.PutUint64(dst[off:], pid)
		off += 8
		binary.LittleEndian.PutUint64(dst[off:], lsn)
		off += 8
	}
	return off
}

// UnmarshalRecord decodes one record from buf, which must start with the
// 4-byte length prefix. It returns the record and the total bytes consumed.
func UnmarshalRecord(buf []byte) (*Record, int, error) {
	if len(buf) < 4 {
		return nil, 0, io.ErrUnexpectedEOF
	}
	body := int(binary.LittleEndian.Uint32(buf[0:4]))
	if body < headerSize || len(buf) < 4+body {
		return nil, 0, io.ErrUnexpectedEOF
	}
	off := 4
	r := &Record{}
	r.LSN = binary.LittleEndian.Uint64(buf[off:])
	off += 8
	r.TxnID = binary.LittleEndian.Uint64(buf[off:])
	off += 8
	r.Type = RecordType(buf[off])
	off++
	r.PrevLSN = binary.LittleEndian.Uint64(buf[off:])
	off += 8
	r.PageID = binary.LittleEndian.Uint64(buf[off:])
	off += 8
	r.UndoNext = binary.LittleEndian.Uint64(buf[off:])
	off += 8
	blen := int(binary.LittleEndian.Uint32(buf[off:]))
	off += 4
	alen := int(binary.LittleEndian.Uint32(buf[off:]))
	off += 4
	if off+blen+alen > 4+body {
		return nil, 0, errors.New("wal: corrupt record payload lengths")
	}
	if blen > 0 {
		r.Before = append([]byte(nil), buf[off:off+blen]...)
	}
	off += blen
	if alen > 0 {
		r.After = append([]byte(nil), buf[off:off+alen]...)
	}
	off += alen
	if r.Type == TypeCheckpoint {
		tt, dpt, n, err := unmarshalCheckpoint(buf[off : 4+body])
		if err != nil {
			return nil, 0, err
		}
		r.ChkTT, r.ChkDPT = tt, dpt
		off += n
	}
	if off != 4+body {
		return nil, 0, fmt.Errorf("wal: record size mismatch: used %d, want %d", off, 4+body)
	}
	return r, 4 + body, nil
}

func unmarshalCheckpoint(buf []byte) (TxnTable, DirtyPageTable, int, error) {
	off := 0
	if len(buf) < 4 {
		return nil, nil, 0, errors.New("wal: corrupt checkpoint")
	}
	ntt := int(binary.LittleEndian.Uint32(buf[off:]))
	off += 4
	tt := make(TxnTable, ntt)
	for i := 0; i < ntt; i++ {
		if off+17 > len(buf) {
			return nil, nil, 0, errors.New("wal: corrupt checkpoint TT")
		}
		id := binary.LittleEndian.Uint64(buf[off:])
		off += 8
		st := TxnStatus(buf[off])
		off++
		last := binary.LittleEndian.Uint64(buf[off:])
		off += 8
		tt[id] = &TxnEntry{Status: st, LastLSN: last}
	}
	if off+4 > len(buf) {
		return nil, nil, 0, errors.New("wal: corrupt checkpoint DPT")
	}
	ndpt := int(binary.LittleEndian.Uint32(buf[off:]))
	off += 4
	dpt := make(DirtyPageTable, ndpt)
	for i := 0; i < ndpt; i++ {
		if off+16 > len(buf) {
			return nil, nil, 0, errors.New("wal: corrupt checkpoint DPT")
		}
		pid := binary.LittleEndian.Uint64(buf[off:])
		off += 8
		lsn := binary.LittleEndian.Uint64(buf[off:])
		off += 8
		dpt[pid] = lsn
	}
	return tt, dpt, off, nil
}
