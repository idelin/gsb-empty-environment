package waldb

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// RecType is the kind of a WAL record.
type RecType byte

const (
	RecBegin      RecType = 1 // transaction begin
	RecUpdate     RecType = 2 // page update, carries before/after images
	RecCommit     RecType = 3 // commit; durable commit == committed txn
	RecAbort      RecType = 4 // abort starts
	RecCLR        RecType = 5 // compensation log record (redo-only)
	RecEnd        RecType = 6 // transaction fully finished
	RecCheckpoint RecType = 7 // fuzzy checkpoint: txn table + dirty page table
)

func (t RecType) String() string {
	switch t {
	case RecBegin:
		return "begin"
	case RecUpdate:
		return "update"
	case RecCommit:
		return "commit"
	case RecAbort:
		return "abort"
	case RecCLR:
		return "clr"
	case RecEnd:
		return "end"
	case RecCheckpoint:
		return "checkpoint"
	}
	return "unknown"
}

// TxnStatus is the analysis-phase transaction state.
type TxnStatus byte

const (
	StatusRunning   TxnStatus = 1
	StatusCommitted TxnStatus = 2
	StatusAborting  TxnStatus = 3
)

// TxnEntry is one row of the transaction table.
type TxnEntry struct {
	Status  TxnStatus
	LastLSN uint64
}

// CheckpointData is the payload of a checkpoint record: a snapshot of
// the transaction table and the dirty page table (pageID -> recLSN).
type CheckpointData struct {
	Txns map[uint64]TxnEntry
	DPT  map[uint32]uint64
}

// LogRecord is the in-memory form of every WAL record. Fields are
// reused across record types to keep encoding simple:
//   - Update:    PageID, Before, After
//   - CLR:       PageID, After (the value restored by the undo), UndoNextLSN
//   - Checkpoint: Checkpoint
type LogRecord struct {
	LSN         uint64
	PrevLSN     uint64
	TxnID       uint64
	Type        RecType
	PageID      uint32
	Before      int64
	After       int64
	UndoNextLSN uint64
	Checkpoint  *CheckpointData
}

var errCorrupt = errors.New("wal: corrupt or incomplete record")

// On-disk layout (all integers little-endian):
//
//	Len(4) Body CRC32(4)
//
// Len counts Body+CRC32. CRC32 (IEEE) covers Body only.
// Body:
//
//	LSN(8) PrevLSN(8) TxnID(8) Type(1) Payload
//
// Payload:
//
//	begin/commit/abort/end: (empty)
//	update:    PageID(4) Before(8) After(8)
//	clr:       PageID(4) After(8) UndoNextLSN(8)
//	checkpoint: NTxns(4) [TxnID(8) Status(1) LastLSN(8)]* NDPT(4) [PageID(4) RecLSN(8)]*
func encodeRecord(r *LogRecord) []byte {
	body := make([]byte, 0, 64)
	body = binary.LittleEndian.AppendUint64(body, r.LSN)
	body = binary.LittleEndian.AppendUint64(body, r.PrevLSN)
	body = binary.LittleEndian.AppendUint64(body, r.TxnID)
	body = append(body, byte(r.Type))
	switch r.Type {
	case RecUpdate:
		body = binary.LittleEndian.AppendUint32(body, r.PageID)
		body = binary.LittleEndian.AppendUint64(body, uint64(r.Before))
		body = binary.LittleEndian.AppendUint64(body, uint64(r.After))
	case RecCLR:
		body = binary.LittleEndian.AppendUint32(body, r.PageID)
		body = binary.LittleEndian.AppendUint64(body, uint64(r.After))
		body = binary.LittleEndian.AppendUint64(body, r.UndoNextLSN)
	case RecCheckpoint:
		cp := r.Checkpoint
		body = binary.LittleEndian.AppendUint32(body, uint32(len(cp.Txns)))
		for id, e := range cp.Txns {
			body = binary.LittleEndian.AppendUint64(body, id)
			body = append(body, byte(e.Status))
			body = binary.LittleEndian.AppendUint64(body, e.LastLSN)
		}
		body = binary.LittleEndian.AppendUint32(body, uint32(len(cp.DPT)))
		for pg, lsn := range cp.DPT {
			body = binary.LittleEndian.AppendUint32(body, pg)
			body = binary.LittleEndian.AppendUint64(body, lsn)
		}
	}
	out := make([]byte, 0, 4+len(body)+4)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(body)+4))
	out = append(out, body...)
	out = binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(body))
	return out
}

// decodeRecord decodes the record starting at buf[off]. It returns the
// record and the offset just past it. Any truncation or CRC mismatch
// yields errCorrupt; callers treat that as the end of the valid log.
func decodeRecord(buf []byte, off int) (*LogRecord, int, error) {
	if off+4 > len(buf) {
		return nil, off, errCorrupt
	}
	l := int(binary.LittleEndian.Uint32(buf[off : off+4]))
	// minimum body: LSN+PrevLSN+TxnID+Type = 25 bytes
	if l < 4+25 || off+4+l > len(buf) {
		return nil, off, errCorrupt
	}
	body := buf[off+4 : off+4+l-4]
	crc := binary.LittleEndian.Uint32(buf[off+4+l-4 : off+4+l])
	if crc32.ChecksumIEEE(body) != crc {
		return nil, off, errCorrupt
	}
	r := &LogRecord{}
	pos := 0
	take := func(n int) ([]byte, bool) {
		if pos+n > len(body) {
			return nil, false
		}
		b := body[pos : pos+n]
		pos += n
		return b, true
	}
	u64 := func() (uint64, bool) {
		b, ok := take(8)
		if !ok {
			return 0, false
		}
		return binary.LittleEndian.Uint64(b), true
	}
	u32 := func() (uint32, bool) {
		b, ok := take(4)
		if !ok {
			return 0, false
		}
		return binary.LittleEndian.Uint32(b), true
	}
	var ok bool
	if r.LSN, ok = u64(); !ok {
		return nil, off, errCorrupt
	}
	if r.PrevLSN, ok = u64(); !ok {
		return nil, off, errCorrupt
	}
	if r.TxnID, ok = u64(); !ok {
		return nil, off, errCorrupt
	}
	tb, ok := take(1)
	if !ok {
		return nil, off, errCorrupt
	}
	r.Type = RecType(tb[0])
	switch r.Type {
	case RecBegin, RecCommit, RecAbort, RecEnd:
	case RecUpdate:
		var v uint64
		if r.PageID, ok = u32(); !ok {
			return nil, off, errCorrupt
		}
		if v, ok = u64(); !ok {
			return nil, off, errCorrupt
		}
		r.Before = int64(v)
		if v, ok = u64(); !ok {
			return nil, off, errCorrupt
		}
		r.After = int64(v)
	case RecCLR:
		var v uint64
		if r.PageID, ok = u32(); !ok {
			return nil, off, errCorrupt
		}
		if v, ok = u64(); !ok {
			return nil, off, errCorrupt
		}
		r.After = int64(v)
		if r.UndoNextLSN, ok = u64(); !ok {
			return nil, off, errCorrupt
		}
	case RecCheckpoint:
		cp := &CheckpointData{Txns: map[uint64]TxnEntry{}, DPT: map[uint32]uint64{}}
		n, ok := u32()
		if !ok {
			return nil, off, errCorrupt
		}
		for i := uint32(0); i < n; i++ {
			id, ok1 := u64()
			st, ok2 := take(1)
			last, ok3 := u64()
			if !ok1 || !ok2 || !ok3 {
				return nil, off, errCorrupt
			}
			cp.Txns[id] = TxnEntry{Status: TxnStatus(st[0]), LastLSN: last}
		}
		m, ok := u32()
		if !ok {
			return nil, off, errCorrupt
		}
		for i := uint32(0); i < m; i++ {
			pg, ok1 := u32()
			lsn, ok2 := u64()
			if !ok1 || !ok2 {
				return nil, off, errCorrupt
			}
			cp.DPT[pg] = lsn
		}
		r.Checkpoint = cp
	default:
		return nil, off, errCorrupt
	}
	if pos != len(body) {
		return nil, off, errCorrupt
	}
	return r, off + 4 + l, nil
}

// scanLog decodes records until the first incomplete/corrupt one and
// returns the valid records plus the number of valid bytes consumed.
// A torn tail (crash mid-flush) simply ends the scan.
func scanLog(data []byte) ([]*LogRecord, int) {
	var recs []*LogRecord
	off := 0
	for off < len(data) {
		r, next, err := decodeRecord(data, off)
		if err != nil {
			break
		}
		recs = append(recs, r)
		off = next
	}
	return recs, off
}
