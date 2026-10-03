// Package wal implements a write-ahead log with ARIES-style records:
// UPDATE, COMMIT, ABORT, END, CLR (compensation log record) and CHECKPOINT.
// Records are length-prefixed binary frames appended to a single file.
package wal

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"os"
	"sync"
)

// LSN is a monotonically increasing log sequence number.
type LSN uint64

// RecordType identifies the kind of a log record.
type RecordType uint8

const (
	TypeUpdate RecordType = iota + 1
	TypeCommit
	TypeAbort
	TypeEnd
	TypeCLR
	TypeCheckpoint
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
	}
	return "UNKNOWN"
}

// TxnStatus is the recovery-time state of a transaction.
type TxnStatus uint8

const (
	TxnRunning TxnStatus = iota + 1
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

// TxnSnapshot is the checkpointed state of one transaction.
type TxnSnapshot struct {
	LastLSN LSN
	Status  TxnStatus
}

// CheckpointData is the payload of a CHECKPOINT record: the transaction
// table and the dirty page table at checkpoint time.
type CheckpointData struct {
	Txns       map[uint64]TxnSnapshot
	DirtyPages map[uint64]LSN
}

// Record is a single WAL entry. Before/After carry the page images for
// UPDATE and CLR records; UndoNextLSN is only meaningful for CLR records.
type Record struct {
	LSN         LSN
	Type        RecordType
	TxnID       uint64
	PrevLSN     LSN
	PageID      uint64
	UndoNextLSN LSN
	Before      []byte
	After       []byte
	Checkpoint  *CheckpointData
}

func (r *Record) String() string {
	return fmt.Sprintf("LSN=%d %s txn=%d prev=%d page=%d undoNext=%d before=%q after=%q",
		r.LSN, r.Type, r.TxnID, r.PrevLSN, r.PageID, r.UndoNextLSN, r.Before, r.After)
}

// Writer appends records to a WAL file. Safe for concurrent use.
type Writer struct {
	mu      sync.Mutex
	f       *os.File
	w       *bufio.Writer
	nextLSN LSN
}

// Open opens (or creates) the WAL at path. Existing records are scanned so
// that newly appended records continue the LSN sequence.
func Open(path string) (*Writer, error) {
	recs, err := ReadAll(path)
	if err != nil {
		return nil, err
	}
	var next LSN = 1
	for _, r := range recs {
		if r.LSN >= next {
			next = r.LSN + 1
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return &Writer{f: f, w: bufio.NewWriter(f), nextLSN: next}, nil
}

// Append assigns the next LSN to rec, writes it and returns the LSN.
func (w *Writer) Append(rec *Record) (LSN, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec.LSN = w.nextLSN
	w.nextLSN++
	if _, err := w.w.Write(encodeRecord(rec)); err != nil {
		return 0, err
	}
	return rec.LSN, nil
}

// Sync flushes buffered data and fsyncs the file.
func (w *Writer) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.w.Flush(); err != nil {
		return err
	}
	return w.f.Sync()
}

// Close flushes and closes the WAL.
func (w *Writer) Close() error {
	if err := w.Sync(); err != nil {
		return err
	}
	return w.f.Close()
}

func writeBytes(buf *bytes.Buffer, b []byte) {
	binary.Write(buf, binary.BigEndian, uint32(len(b)))
	buf.Write(b)
}

func encodeRecord(rec *Record) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, uint64(rec.LSN))
	buf.WriteByte(byte(rec.Type))
	binary.Write(buf, binary.BigEndian, rec.TxnID)
	binary.Write(buf, binary.BigEndian, uint64(rec.PrevLSN))
	binary.Write(buf, binary.BigEndian, rec.PageID)
	binary.Write(buf, binary.BigEndian, uint64(rec.UndoNextLSN))
	writeBytes(buf, rec.Before)
	writeBytes(buf, rec.After)
	var extra []byte
	if rec.Type == TypeCheckpoint && rec.Checkpoint != nil {
		eb := new(bytes.Buffer)
		if err := gob.NewEncoder(eb).Encode(rec.Checkpoint); err == nil {
			extra = eb.Bytes()
		}
	}
	writeBytes(buf, extra)
	payload := buf.Bytes()
	framed := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(framed, uint32(len(payload)))
	copy(framed[4:], payload)
	return framed
}

func readBytes(buf *bytes.Reader) ([]byte, error) {
	var n uint32
	if err := binary.Read(buf, binary.BigEndian, &n); err != nil {
		return nil, err
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(buf, b); err != nil {
		return nil, err
	}
	return b, nil
}

func decodeRecord(payload []byte) (*Record, error) {
	buf := bytes.NewReader(payload)
	rec := new(Record)
	var lsn, prev, undoNext uint64
	var typ byte
	if err := binary.Read(buf, binary.BigEndian, &lsn); err != nil {
		return nil, err
	}
	var err error
	if typ, err = buf.ReadByte(); err != nil {
		return nil, err
	}
	if err = binary.Read(buf, binary.BigEndian, &rec.TxnID); err != nil {
		return nil, err
	}
	if err = binary.Read(buf, binary.BigEndian, &prev); err != nil {
		return nil, err
	}
	if err = binary.Read(buf, binary.BigEndian, &rec.PageID); err != nil {
		return nil, err
	}
	if err = binary.Read(buf, binary.BigEndian, &undoNext); err != nil {
		return nil, err
	}
	rec.LSN = LSN(lsn)
	rec.Type = RecordType(typ)
	rec.PrevLSN = LSN(prev)
	rec.UndoNextLSN = LSN(undoNext)
	if rec.Before, err = readBytes(buf); err != nil {
		return nil, err
	}
	if rec.After, err = readBytes(buf); err != nil {
		return nil, err
	}
	extra, err := readBytes(buf)
	if err != nil {
		return nil, err
	}
	if rec.Type == TypeCheckpoint && len(extra) > 0 {
		cd := new(CheckpointData)
		if err := gob.NewDecoder(bytes.NewReader(extra)).Decode(cd); err != nil {
			return nil, err
		}
		rec.Checkpoint = cd
	}
	return rec, nil
}

// ReadAll reads every record in the WAL at path. A missing file yields an
// empty log; a truncated tail frame (crash mid-write) is ignored.
func ReadAll(path string) ([]*Record, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var recs []*Record
	for {
		var nbuf [4]byte
		if _, err := io.ReadFull(r, nbuf[:]); err != nil {
			break
		}
		n := binary.BigEndian.Uint32(nbuf[:])
		payload := make([]byte, n)
		if _, err := io.ReadFull(r, payload); err != nil {
			break
		}
		rec, err := decodeRecord(payload)
		if err != nil {
			return recs, fmt.Errorf("wal: corrupt record: %v", err)
		}
		recs = append(recs, rec)
	}
	return recs, nil
}
