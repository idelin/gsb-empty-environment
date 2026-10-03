// Package wal implements the write-ahead log used by the ARIES
// recovery protocol: record types, binary encoding and stores.
package wal

import (
	"encoding/binary"
	"fmt"
)

// RecType identifies the kind of a log record.
type RecType uint8

const (
	RecInvalid RecType = iota
	RecBegin
	RecUpdate
	RecCommit
	RecAbort
	RecEnd
	RecCLR
	RecCheckpoint
)

func (t RecType) String() string {
	switch t {
	case RecBegin:
		return "BEGIN"
	case RecUpdate:
		return "UPDATE"
	case RecCommit:
		return "COMMIT"
	case RecAbort:
		return "ABORT"
	case RecEnd:
		return "END"
	case RecCLR:
		return "CLR"
	case RecCheckpoint:
		return "CHECKPOINT"
	}
	return "INVALID"
}

// TxnStatus is the transaction state tracked by the transaction table.
type TxnStatus uint8

const (
	TxnRunning TxnStatus = iota
	TxnCommitted
	TxnAborting
)

func (s TxnStatus) String() string {
	switch s {
	case TxnRunning:
		return "RUNNING"
	case TxnCommitted:
		return "COMMITTED"
	case TxnAborting:
		return "ABORTING"
	}
	return "UNKNOWN"
}

// TxnEntry is one row of the transaction table snapshot stored in a
// checkpoint record.
type TxnEntry struct {
	TxnID   uint64
	Status  TxnStatus
	LastLSN uint64
}

// DirtyEntry is one row of the dirty page table snapshot stored in a
// checkpoint record.
type DirtyEntry struct {
	PageID uint64
	RecLSN uint64
}

// Record is a single WAL record. Txns/Dirty are only populated for
// checkpoint records; PageID/UndoNext/Before/After only for UPDATE/CLR.
type Record struct {
	LSN      uint64
	Type     RecType
	TxnID    uint64
	PrevLSN  uint64
	PageID   uint64
	UndoNext uint64
	Before   int64
	After    int64
	Txns     []TxnEntry
	Dirty    []DirtyEntry
}

func (r *Record) String() string {
	base := fmt.Sprintf("LSN=%d %-10s txn=%d prev=%d", r.LSN, r.Type, r.TxnID, r.PrevLSN)
	switch r.Type {
	case RecUpdate, RecCLR:
		return fmt.Sprintf("%s page=%d before=%d after=%d undoNext=%d", base, r.PageID, r.Before, r.After, r.UndoNext)
	case RecCheckpoint:
		return fmt.Sprintf("%s txns=%d dirtyPages=%d", base, len(r.Txns), len(r.Dirty))
	}
	return base
}

// Encode serializes the record (without framing) into bytes. It is
// hand-rolled (no reflection) and allocation-light: the simulator
// encodes millions of records.
func (r *Record) Encode() []byte {
	buf := make([]byte, 57+4+len(r.Txns)*17+4+len(r.Dirty)*16)
	o := 0
	put64 := func(v uint64) { binary.BigEndian.PutUint64(buf[o:], v); o += 8 }
	put32 := func(v uint32) { binary.BigEndian.PutUint32(buf[o:], v); o += 4 }
	put64(r.LSN)
	buf[o] = byte(r.Type)
	o++
	put64(r.TxnID)
	put64(r.PrevLSN)
	put64(r.PageID)
	put64(r.UndoNext)
	put64(uint64(r.Before))
	put64(uint64(r.After))
	put32(uint32(len(r.Txns)))
	for _, t := range r.Txns {
		put64(t.TxnID)
		buf[o] = byte(t.Status)
		o++
		put64(t.LastLSN)
	}
	put32(uint32(len(r.Dirty)))
	for _, d := range r.Dirty {
		put64(d.PageID)
		put64(d.RecLSN)
	}
	return buf
}

// DecodeRecord parses bytes produced by Encode.
func DecodeRecord(data []byte) (*Record, error) {
	if len(data) < 57 {
		return nil, fmt.Errorf("wal: record too short (%d bytes)", len(data))
	}
	r := &Record{}
	o := 0
	get64 := func() uint64 { v := binary.BigEndian.Uint64(data[o:]); o += 8; return v }
	get32 := func() uint32 { v := binary.BigEndian.Uint32(data[o:]); o += 4; return v }
	r.LSN = get64()
	r.Type = RecType(data[o])
	o++
	r.TxnID = get64()
	r.PrevLSN = get64()
	r.PageID = get64()
	r.UndoNext = get64()
	r.Before = int64(get64())
	r.After = int64(get64())
	nTxns := get32()
	if nTxns > 1<<20 {
		return nil, fmt.Errorf("wal: implausible txn count %d", nTxns)
	}
	if len(data) < o+int(nTxns)*17+4 {
		return nil, fmt.Errorf("wal: truncated txn entries")
	}
	if nTxns > 0 {
		r.Txns = make([]TxnEntry, 0, nTxns)
	}
	for i := uint32(0); i < nTxns; i++ {
		var e TxnEntry
		e.TxnID = get64()
		e.Status = TxnStatus(data[o])
		o++
		e.LastLSN = get64()
		r.Txns = append(r.Txns, e)
	}
	nDirty := get32()
	if nDirty > 1<<20 {
		return nil, fmt.Errorf("wal: implausible dirty count %d", nDirty)
	}
	if len(data) < o+int(nDirty)*16 {
		return nil, fmt.Errorf("wal: truncated dirty entries")
	}
	if nDirty > 0 {
		r.Dirty = make([]DirtyEntry, 0, nDirty)
	}
	for i := uint32(0); i < nDirty; i++ {
		r.Dirty = append(r.Dirty, DirtyEntry{PageID: get64(), RecLSN: get64()})
	}
	if o != len(data) {
		return nil, fmt.Errorf("wal: %d trailing bytes in record", len(data)-o)
	}
	return r, nil
}

// DecodeAll decodes a slice of payloads into records.
func DecodeAll(payloads [][]byte) ([]*Record, error) {
	recs := make([]*Record, 0, len(payloads))
	for i, p := range payloads {
		r, err := DecodeRecord(p)
		if err != nil {
			return nil, fmt.Errorf("wal: record %d: %v", i, err)
		}
		recs = append(recs, r)
	}
	return recs, nil
}
