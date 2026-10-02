// Package wal implements the write-ahead log used by the ARIES recovery
// protocol: typed log records, binary framing with CRC32 checksums, an
// append/flush log manager, and sequential/random-access iterators.
package wal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"
)

// LSN is a log sequence number: the byte offset of a record frame in the WAL.
type LSN int64

// LSNInvalid is the "nil" LSN sentinel (LSN 0 is a valid offset).
const LSNInvalid LSN = -1

// RecordType identifies the kind of a log record.
type RecordType uint8

const (
	TypeUpdate          RecordType = 1 + iota // regular update, carries before/after images
	TypeCommit                                // transaction commit
	TypeAbort                                 // transaction abort began
	TypeEnd                                   // transaction finished (after commit/abort cleanup)
	TypeCLR                                   // compensation log record (undo of an update)
	TypeBeginCheckpoint                       // checkpoint started
	TypeEndCheckpoint                         // checkpoint completed; carries TT + DPT snapshot
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
	case TypeBeginCheckpoint:
		return "BEGIN_CHECKPOINT"
	case TypeEndCheckpoint:
		return "END_CHECKPOINT"
	default:
		return "UNKNOWN"
	}
}

// TxnStatus is the transaction state tracked in the transaction table.
type TxnStatus uint8

const (
	StatusRunning   TxnStatus = 1 + iota // active at analysis time
	StatusCommitted                      // committed but END not yet seen
	StatusAborting                       // abort in progress
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
		return "UNKNOWN"
	}
}

// TxnInfo is one transaction-table entry.
type TxnInfo struct {
	Status  TxnStatus
	LastLSN LSN
}

// LogRecord is a single WAL record. DPT/TT are only populated for
// TypeEndCheckpoint records. For TypeEndCheckpoint, PrevLSN carries the LSN
// of the matching TypeBeginCheckpoint record.
type LogRecord struct {
	LSN         LSN
	TxnID       int64
	Type        RecordType
	PrevLSN     LSN
	PageID      int32
	UndoNextLSN LSN // CLR only: LSN of the next record to undo
	Offset      uint32
	Before      []byte
	After       []byte
	DPT         map[int32]LSN
	TT          map[int64]TxnInfo
}

// ErrMalformed is returned when a record payload cannot be decoded.
var ErrMalformed = errors.New("wal: malformed record payload")

func (r *LogRecord) encode() []byte {
	buf := new(bytes.Buffer)
	buf.WriteByte(byte(r.Type))
	writeInt64(buf, r.TxnID)
	writeInt64(buf, int64(r.PrevLSN))
	writeInt32(buf, r.PageID)
	writeInt64(buf, int64(r.UndoNextLSN))
	writeUint32(buf, r.Offset)
	writeBytes(buf, r.Before)
	writeBytes(buf, r.After)
	// DPT, sorted by page id for determinism.
	writeUint32(buf, uint32(len(r.DPT)))
	dptKeys := make([]int, 0, len(r.DPT))
	for k := range r.DPT {
		dptKeys = append(dptKeys, int(k))
	}
	sort.Ints(dptKeys)
	for _, k := range dptKeys {
		writeInt32(buf, int32(k))
		writeInt64(buf, int64(r.DPT[int32(k)]))
	}
	// TT, sorted by txn id.
	writeUint32(buf, uint32(len(r.TT)))
	ttKeys := make([]int64, 0, len(r.TT))
	for k := range r.TT {
		ttKeys = append(ttKeys, k)
	}
	sort.Slice(ttKeys, func(i, j int) bool { return ttKeys[i] < ttKeys[j] })
	for _, k := range ttKeys {
		writeInt64(buf, k)
		buf.WriteByte(byte(r.TT[k].Status))
		writeInt64(buf, int64(r.TT[k].LastLSN))
	}
	return buf.Bytes()
}

func decodeRecord(lsn LSN, payload []byte) (*LogRecord, error) {
	r := &LogRecord{LSN: lsn}
	rd := &reader{buf: payload}
	typ, err := rd.byte_()
	if err != nil {
		return nil, err
	}
	r.Type = RecordType(typ)
	if r.TxnID, err = rd.int64(); err != nil {
		return nil, err
	}
	prev, err := rd.int64()
	if err != nil {
		return nil, err
	}
	r.PrevLSN = LSN(prev)
	if r.PageID, err = rd.int32(); err != nil {
		return nil, err
	}
	undoNext, err := rd.int64()
	if err != nil {
		return nil, err
	}
	r.UndoNextLSN = LSN(undoNext)
	if r.Offset, err = rd.uint32(); err != nil {
		return nil, err
	}
	if r.Before, err = rd.bytes(); err != nil {
		return nil, err
	}
	if r.After, err = rd.bytes(); err != nil {
		return nil, err
	}
	dptLen, err := rd.uint32()
	if err != nil {
		return nil, err
	}
	if dptLen > 0 {
		r.DPT = make(map[int32]LSN, dptLen)
	}
	for i := uint32(0); i < dptLen; i++ {
		pageID, err := rd.int32()
		if err != nil {
			return nil, err
		}
		recLSN, err := rd.int64()
		if err != nil {
			return nil, err
		}
		r.DPT[pageID] = LSN(recLSN)
	}
	ttLen, err := rd.uint32()
	if err != nil {
		return nil, err
	}
	if ttLen > 0 {
		r.TT = make(map[int64]TxnInfo, ttLen)
	}
	for i := uint32(0); i < ttLen; i++ {
		txnID, err := rd.int64()
		if err != nil {
			return nil, err
		}
		status, err := rd.byte_()
		if err != nil {
			return nil, err
		}
		lastLSN, err := rd.int64()
		if err != nil {
			return nil, err
		}
		r.TT[txnID] = TxnInfo{Status: TxnStatus(status), LastLSN: LSN(lastLSN)}
	}
	if rd.off != len(rd.buf) {
		return nil, ErrMalformed
	}
	return r, nil
}

func writeInt64(buf *bytes.Buffer, v int64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v))
	buf.Write(b[:])
}

func writeInt32(buf *bytes.Buffer, v int32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(v))
	buf.Write(b[:])
}

func writeUint32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func writeBytes(buf *bytes.Buffer, b []byte) {
	writeUint32(buf, uint32(len(b)))
	buf.Write(b)
}

// reader is a cursor over a payload that fails sticky-free per read.
type reader struct {
	buf []byte
	off int
}

func (r *reader) byte_() (byte, error) {
	if r.off+1 > len(r.buf) {
		return 0, ErrMalformed
	}
	b := r.buf[r.off]
	r.off++
	return b, nil
}

func (r *reader) int64() (int64, error) {
	if r.off+8 > len(r.buf) {
		return 0, ErrMalformed
	}
	v := binary.BigEndian.Uint64(r.buf[r.off:])
	r.off += 8
	return int64(v), nil
}

func (r *reader) int32() (int32, error) {
	if r.off+4 > len(r.buf) {
		return 0, ErrMalformed
	}
	v := binary.BigEndian.Uint32(r.buf[r.off:])
	r.off += 4
	return int32(v), nil
}

func (r *reader) uint32() (uint32, error) {
	if r.off+4 > len(r.buf) {
		return 0, ErrMalformed
	}
	v := binary.BigEndian.Uint32(r.buf[r.off:])
	r.off += 4
	return v, nil
}

func (r *reader) bytes() ([]byte, error) {
	n, err := r.uint32()
	if err != nil {
		return nil, err
	}
	if uint64(r.off)+uint64(n) > uint64(len(r.buf)) {
		return nil, ErrMalformed
	}
	out := make([]byte, n)
	copy(out, r.buf[r.off:r.off+int(n)])
	r.off += int(n)
	return out, nil
}
