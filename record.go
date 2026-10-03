// Package aries implements the classic ARIES crash-recovery protocol:
// a write-ahead log (WAL), a three-phase recovery engine
// (Analysis / Redo / Undo with compensation log records), and a fault
// injector used to verify resilience under power loss, disk-full and
// packet-loss conditions.
package aries

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"sort"
)

// LSN is a log sequence number: the byte offset of a record inside the WAL
// file. LSN 0 is reserved as NilLSN ("no record") because the WAL file
// starts with an 8-byte magic header, so every real record has LSN >= 8.
type LSN = uint64

// NilLSN marks the absence of a previous / undo-next record.
const NilLSN LSN = 0

// RecType enumerates the WAL record kinds used by ARIES.
type RecType uint8

const (
	RecBegin      RecType = 1 + iota // transaction begin
	RecUpdate                        // forward update: before/after image
	RecCommit                        // transaction commit decision
	RecAbort                         // transaction abort decision
	RecCLR                           // compensation log record (undo of an update)
	RecEnd                           // transaction fully finished
	RecCheckpoint                    // fuzzy checkpoint: txn table + DPT snapshot
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
	case RecCLR:
		return "CLR"
	case RecEnd:
		return "END"
	case RecCheckpoint:
		return "CHECKPOINT"
	}
	return "UNKNOWN"
}

// TxnStatus is the transaction state tracked in the transaction table.
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

// TxnSnapshot is the per-transaction state persisted inside a checkpoint.
type TxnSnapshot struct {
	LastLSN LSN
	Status  TxnStatus
}

// LogRecord is a single WAL record.
type LogRecord struct {
	LSN      LSN
	Type     RecType
	TxnID    uint64
	PrevLSN  LSN // previous record of the same transaction (NilLSN if none)
	PageID   uint64
	UndoNext LSN // CLR only: next record to undo (usually PrevLSN of the undone update)
	Before   []byte
	After    []byte

	// Checkpoint payload.
	Txns map[uint64]TxnSnapshot
	DPT  map[uint64]LSN // pageID -> recLSN
}

// headerSize: crc(4) type(1) txn(8) prev(8) page(8) undonext(8)
// beforeLen(4) afterLen(4) nTxns(4) nDPT(4)
const headerSize = 4 + 1 + 8 + 8 + 8 + 8 + 4 + 4 + 4 + 4

const txnEntrySize = 8 + 8 + 1 // txnID + lastLSN + status
const dptEntrySize = 8 + 8     // pageID + recLSN

// errCorrupt is returned when a record fails structural or CRC validation.
var errCorrupt = errors.New("aries: corrupt or torn log record")

// encodeRecord serializes r. r.LSN is not stored in the payload: it is
// implied by the file offset and assigned by the reader.
func encodeRecord(r *LogRecord) []byte {
	body := headerSize + len(r.Before) + len(r.After) +
		len(r.Txns)*txnEntrySize + len(r.DPT)*dptEntrySize
	buf := make([]byte, body)
	buf[4] = byte(r.Type)
	binary.BigEndian.PutUint64(buf[5:], r.TxnID)
	binary.BigEndian.PutUint64(buf[13:], r.PrevLSN)
	binary.BigEndian.PutUint64(buf[21:], r.PageID)
	binary.BigEndian.PutUint64(buf[29:], r.UndoNext)
	binary.BigEndian.PutUint32(buf[37:], uint32(len(r.Before)))
	binary.BigEndian.PutUint32(buf[41:], uint32(len(r.After)))
	binary.BigEndian.PutUint32(buf[45:], uint32(len(r.Txns)))
	binary.BigEndian.PutUint32(buf[49:], uint32(len(r.DPT)))
	off := headerSize
	off += copy(buf[off:], r.Before)
	off += copy(buf[off:], r.After)
	for _, id := range sortedTxnKeys(r.Txns) {
		snap := r.Txns[id]
		binary.BigEndian.PutUint64(buf[off:], id)
		binary.BigEndian.PutUint64(buf[off+8:], snap.LastLSN)
		buf[off+16] = byte(snap.Status)
		off += txnEntrySize
	}
	for _, pageID := range sortedPageKeys(r.DPT) {
		binary.BigEndian.PutUint64(buf[off:], pageID)
		binary.BigEndian.PutUint64(buf[off+8:], r.DPT[pageID])
		off += dptEntrySize
	}
	binary.BigEndian.PutUint32(buf[0:], crc32.ChecksumIEEE(buf[4:]))
	return buf
}

// encodedLen returns the size r will occupy on disk.
func encodedLen(r *LogRecord) int {
	return headerSize + len(r.Before) + len(r.After) +
		len(r.Txns)*txnEntrySize + len(r.DPT)*dptEntrySize
}

// decodeRecord parses one record from the head of buf. It returns the record
// and the number of bytes consumed. A short buffer or CRC mismatch yields
// errCorrupt, which callers treat as the crash point (torn tail).
func decodeRecord(buf []byte) (*LogRecord, int, error) {
	if len(buf) < headerSize {
		return nil, 0, errCorrupt
	}
	want := binary.BigEndian.Uint32(buf[0:])
	beforeLen := int(binary.BigEndian.Uint32(buf[37:]))
	afterLen := int(binary.BigEndian.Uint32(buf[41:]))
	nTxns := int(binary.BigEndian.Uint32(buf[45:]))
	nDPT := int(binary.BigEndian.Uint32(buf[49:]))
	total := headerSize + beforeLen + afterLen + nTxns*txnEntrySize + nDPT*dptEntrySize
	if len(buf) < total {
		return nil, 0, errCorrupt
	}
	if crc32.ChecksumIEEE(buf[4:total]) != want {
		return nil, 0, errCorrupt
	}
	r := &LogRecord{
		Type:     RecType(buf[4]),
		TxnID:    binary.BigEndian.Uint64(buf[5:]),
		PrevLSN:  binary.BigEndian.Uint64(buf[13:]),
		PageID:   binary.BigEndian.Uint64(buf[21:]),
		UndoNext: binary.BigEndian.Uint64(buf[29:]),
	}
	off := headerSize
	if beforeLen > 0 {
		r.Before = append([]byte(nil), buf[off:off+beforeLen]...)
	}
	off += beforeLen
	if afterLen > 0 {
		r.After = append([]byte(nil), buf[off:off+afterLen]...)
	}
	off += afterLen
	if nTxns > 0 {
		r.Txns = make(map[uint64]TxnSnapshot, nTxns)
		for i := 0; i < nTxns; i++ {
			id := binary.BigEndian.Uint64(buf[off:])
			snap := TxnSnapshot{
				LastLSN: binary.BigEndian.Uint64(buf[off+8:]),
				Status:  TxnStatus(buf[off+16]),
			}
			r.Txns[id] = snap
			off += txnEntrySize
		}
	}
	if nDPT > 0 {
		r.DPT = make(map[uint64]LSN, nDPT)
		for i := 0; i < nDPT; i++ {
			id := binary.BigEndian.Uint64(buf[off:])
			r.DPT[id] = binary.BigEndian.Uint64(buf[off+8:])
			off += dptEntrySize
		}
	}
	return r, total, nil
}

func sortedTxnKeys(m map[uint64]TxnSnapshot) []uint64 {
	keys := make([]uint64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortedPageKeys(m map[uint64]LSN) []uint64 {
	keys := make([]uint64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func (r *LogRecord) String() string {
	base := fmt.Sprintf("LSN=%d %-10s txn=%d prev=%d", r.LSN, r.Type, r.TxnID, r.PrevLSN)
	switch r.Type {
	case RecUpdate, RecCLR:
		base += fmt.Sprintf(" page=%d undoNext=%d before=%q after=%q",
			r.PageID, r.UndoNext, trim(r.Before), trim(r.After))
	case RecCheckpoint:
		base += fmt.Sprintf(" txns=%d dpt=%d", len(r.Txns), len(r.DPT))
	}
	return base
}

func trim(b []byte) []byte {
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return b[:n]
}
