// Package wal implements the write-ahead log used by the ARIES
// recovery engine. Records are framed as [len uint32][body] and the
// LSN of a record is its byte offset inside the log file.
package wal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// RecordType identifies the kind of a log record.
type RecordType byte

const (
	RecUpdate RecordType = iota + 1
	RecCLR
	RecCommit
	RecAbort
	RecEnd
	RecCheckpoint
)

func (t RecordType) String() string {
	switch t {
	case RecUpdate:
		return "UPDATE"
	case RecCLR:
		return "CLR"
	case RecCommit:
		return "COMMIT"
	case RecAbort:
		return "ABORT"
	case RecEnd:
		return "END"
	case RecCheckpoint:
		return "CHECKPOINT"
	}
	return fmt.Sprintf("UNKNOWN(%d)", byte(t))
}

// TxnStatus is the transaction state tracked in the transaction table.
type TxnStatus byte

const (
	StatusRunning TxnStatus = iota + 1
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
	}
	return fmt.Sprintf("UNKNOWN(%d)", byte(s))
}

// TxnEntry is one row of a checkpoint's transaction table snapshot.
type TxnEntry struct {
	TxnID   uint64
	LastLSN uint64
	Status  TxnStatus
}

// PageEntry is one row of a checkpoint's dirty page table snapshot.
type PageEntry struct {
	PageID uint64
	RecLSN uint64
}

// Record is a single WAL record. Only the fields relevant to the
// record type are populated.
type Record struct {
	Type     RecordType
	LSN      uint64 // assigned by WAL.Append
	TxnID    uint64
	PrevLSN  uint64
	PageID   uint64 // update/CLR only
	Offset   uint32 // update/CLR only: byte offset inside the page
	UndoNext uint64 // CLR only
	Before   []byte // update/CLR only
	After    []byte // update/CLR only
	Txns     []TxnEntry
	Pages    []PageEntry
}

// ErrCorrupt is returned when a record cannot be decoded.
var ErrCorrupt = errors.New("wal: corrupt record")

func writeU64(buf *bytes.Buffer, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	buf.Write(b[:])
}

func writeU32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func writeBytes(buf *bytes.Buffer, data []byte) {
	writeU32(buf, uint32(len(data)))
	buf.Write(data)
}

// encode serializes the record body (everything after the frame
// length; the LSN is implied by the file offset).
func (r *Record) encode() []byte {
	var buf bytes.Buffer
	buf.WriteByte(byte(r.Type))
	writeU64(&buf, r.TxnID)
	writeU64(&buf, r.PrevLSN)
	switch r.Type {
	case RecUpdate, RecCLR:
		writeU64(&buf, r.PageID)
		writeU32(&buf, r.Offset)
		if r.Type == RecCLR {
			writeU64(&buf, r.UndoNext)
		}
		writeBytes(&buf, r.Before)
		writeBytes(&buf, r.After)
	case RecCheckpoint:
		writeU32(&buf, uint32(len(r.Txns)))
		for _, te := range r.Txns {
			writeU64(&buf, te.TxnID)
			writeU64(&buf, te.LastLSN)
			buf.WriteByte(byte(te.Status))
		}
		writeU32(&buf, uint32(len(r.Pages)))
		for _, pe := range r.Pages {
			writeU64(&buf, pe.PageID)
			writeU64(&buf, pe.RecLSN)
		}
	case RecCommit, RecAbort, RecEnd:
		// header only
	}
	return buf.Bytes()
}

type decoder struct {
	data []byte
	off  int
}

func (d *decoder) u8() (byte, error) {
	if d.off+1 > len(d.data) {
		return 0, ErrCorrupt
	}
	v := d.data[d.off]
	d.off++
	return v, nil
}

func (d *decoder) u32() (uint32, error) {
	if d.off+4 > len(d.data) {
		return 0, ErrCorrupt
	}
	v := binary.BigEndian.Uint32(d.data[d.off:])
	d.off += 4
	return v, nil
}

func (d *decoder) u64() (uint64, error) {
	if d.off+8 > len(d.data) {
		return 0, ErrCorrupt
	}
	v := binary.BigEndian.Uint64(d.data[d.off:])
	d.off += 8
	return v, nil
}

func (d *decoder) bytes() ([]byte, error) {
	n, err := d.u32()
	if err != nil {
		return nil, err
	}
	if d.off+int(n) > len(d.data) {
		return nil, ErrCorrupt
	}
	out := make([]byte, n)
	copy(out, d.data[d.off:d.off+int(n)])
	d.off += int(n)
	return out, nil
}

// decodeBody parses a record body. lsn is supplied by the caller
// (it is the file offset of the record frame).
func decodeBody(lsn uint64, body []byte) (*Record, error) {
	d := &decoder{data: body}
	typ, err := d.u8()
	if err != nil {
		return nil, err
	}
	rec := &Record{Type: RecordType(typ), LSN: lsn}
	if rec.TxnID, err = d.u64(); err != nil {
		return nil, err
	}
	if rec.PrevLSN, err = d.u64(); err != nil {
		return nil, err
	}
	switch rec.Type {
	case RecUpdate, RecCLR:
		if rec.PageID, err = d.u64(); err != nil {
			return nil, err
		}
		var off uint32
		if off, err = d.u32(); err != nil {
			return nil, err
		}
		rec.Offset = off
		if rec.Type == RecCLR {
			if rec.UndoNext, err = d.u64(); err != nil {
				return nil, err
			}
		}
		if rec.Before, err = d.bytes(); err != nil {
			return nil, err
		}
		if rec.After, err = d.bytes(); err != nil {
			return nil, err
		}
	case RecCheckpoint:
		var n uint32
		if n, err = d.u32(); err != nil {
			return nil, err
		}
		for i := uint32(0); i < n; i++ {
			var te TxnEntry
			if te.TxnID, err = d.u64(); err != nil {
				return nil, err
			}
			if te.LastLSN, err = d.u64(); err != nil {
				return nil, err
			}
			var s byte
			if s, err = d.u8(); err != nil {
				return nil, err
			}
			te.Status = TxnStatus(s)
			rec.Txns = append(rec.Txns, te)
		}
		if n, err = d.u32(); err != nil {
			return nil, err
		}
		for i := uint32(0); i < n; i++ {
			var pe PageEntry
			if pe.PageID, err = d.u64(); err != nil {
				return nil, err
			}
			if pe.RecLSN, err = d.u64(); err != nil {
				return nil, err
			}
			rec.Pages = append(rec.Pages, pe)
		}
	case RecCommit, RecAbort, RecEnd:
		// header only
	default:
		return nil, fmt.Errorf("wal: unknown record type %d", typ)
	}
	if d.off != len(body) {
		return nil, ErrCorrupt
	}
	return rec, nil
}
