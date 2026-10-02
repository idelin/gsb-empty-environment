// Package aries implements the classic ARIES crash-recovery protocol
// (Analysis / Redo / Undo with CLRs) on top of a write-ahead log,
// plus lock-free concurrency primitives used to accelerate logging.
package aries

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"os"
	"sync"
)

// LogType identifies the kind of a WAL record.
type LogType uint8

const (
	LogUpdate LogType = iota + 1
	LogCommit
	LogAbort
	LogEnd
	LogCLR
	LogCheckpoint
)

func (t LogType) String() string {
	switch t {
	case LogUpdate:
		return "UPDATE"
	case LogCommit:
		return "COMMIT"
	case LogAbort:
		return "ABORT"
	case LogEnd:
		return "END"
	case LogCLR:
		return "CLR"
	case LogCheckpoint:
		return "CHECKPOINT"
	}
	return "UNKNOWN"
}

// LogRecord is a single WAL entry.
//
// For UPDATE records Before holds the before-image and After the
// after-image. For CLR records After holds the redo image (the value
// the undo wrote back) and UndoNextLSN points at the next record to
// undo, skipping already-undone work on repeated crashes.
type LogRecord struct {
	LSN         uint64
	PrevLSN     uint64
	TxnID       uint64
	Type        LogType
	PageID      uint64
	UndoNextLSN uint64
	Before      []byte
	After       []byte
}

func (r *LogRecord) String() string {
	return fmt.Sprintf("LSN=%d type=%s txn=%d page=%d prev=%d undoNext=%d",
		r.LSN, r.Type, r.TxnID, r.PageID, r.PrevLSN, r.UndoNextLSN)
}

// CheckpointPayload is stored inside a CHECKPOINT record and snapshots
// the transaction table and dirty page table at checkpoint time.
type CheckpointPayload struct {
	// Txns maps txnID -> lastLSN for transactions active at checkpoint.
	Txns map[uint64]uint64
	// DirtyPages maps pageID -> recLSN.
	DirtyPages map[uint64]uint64
}

const recordHeaderSize = 1 + 8 + 8 + 8 + 8 + 8 + 4 + 4

// encode serializes a record: type(1) lsn(8) prev(8) txn(8) page(8)
// undoNext(8) beforeLen(4) afterLen(4) before after.
func (r *LogRecord) encode() []byte {
	buf := make([]byte, recordHeaderSize+len(r.Before)+len(r.After))
	buf[0] = byte(r.Type)
	binary.LittleEndian.PutUint64(buf[1:], r.LSN)
	binary.LittleEndian.PutUint64(buf[9:], r.PrevLSN)
	binary.LittleEndian.PutUint64(buf[17:], r.TxnID)
	binary.LittleEndian.PutUint64(buf[25:], r.PageID)
	binary.LittleEndian.PutUint64(buf[33:], r.UndoNextLSN)
	binary.LittleEndian.PutUint32(buf[41:], uint32(len(r.Before)))
	binary.LittleEndian.PutUint32(buf[45:], uint32(len(r.After)))
	copy(buf[recordHeaderSize:], r.Before)
	copy(buf[recordHeaderSize+len(r.Before):], r.After)
	return buf
}

func decodeRecord(buf []byte) (*LogRecord, error) {
	if len(buf) < recordHeaderSize {
		return nil, fmt.Errorf("record too short: %d bytes", len(buf))
	}
	r := &LogRecord{
		Type:        LogType(buf[0]),
		LSN:         binary.LittleEndian.Uint64(buf[1:]),
		PrevLSN:     binary.LittleEndian.Uint64(buf[9:]),
		TxnID:       binary.LittleEndian.Uint64(buf[17:]),
		PageID:      binary.LittleEndian.Uint64(buf[25:]),
		UndoNextLSN: binary.LittleEndian.Uint64(buf[33:]),
	}
	beforeLen := binary.LittleEndian.Uint32(buf[41:])
	afterLen := binary.LittleEndian.Uint32(buf[45:])
	if uint64(len(buf)) < recordHeaderSize+uint64(beforeLen)+uint64(afterLen) {
		return nil, fmt.Errorf("truncated record at LSN %d", r.LSN)
	}
	r.Before = append([]byte(nil), buf[recordHeaderSize:recordHeaderSize+beforeLen]...)
	r.After = append([]byte(nil), buf[recordHeaderSize+beforeLen:recordHeaderSize+beforeLen+afterLen]...)
	return r, nil
}

// EncodeCheckpointPayload serializes a checkpoint payload with gob.
func EncodeCheckpointPayload(p *CheckpointPayload) []byte {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(p); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// DecodeCheckpointPayload deserializes a checkpoint payload.
func DecodeCheckpointPayload(data []byte) (*CheckpointPayload, error) {
	var p CheckpointPayload
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

// WAL is an append-only write-ahead log file. Appends are serialized
// by a mutex, but LSNs are handed out by a lock-free CAS allocator so
// concurrent transactions never contend on a global log lock to
// reserve log sequence numbers.
type WAL struct {
	mu   sync.Mutex // serializes physical appends only
	file *os.File
	lsn  *LSNAllocator
	path string
}

// OpenWAL opens (or creates) a WAL file. LSN allocation resumes after
// the highest LSN already present in the file.
func OpenWAL(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	w := &WAL{file: f, lsn: NewLSNAllocator(0), path: path}
	recs, err := w.Scan()
	if err != nil {
		f.Close()
		return nil, err
	}
	var maxLSN uint64
	for _, r := range recs {
		if r.LSN > maxLSN {
			maxLSN = r.LSN
		}
	}
	w.lsn = NewLSNAllocator(maxLSN)
	return w, nil
}

// Close flushes and closes the underlying file.
func (w *WAL) Close() error { return w.file.Close() }

// Path returns the WAL file path.
func (w *WAL) Path() string { return w.path }

// Append reserves an LSN via the lock-free allocator and durably
// appends the record, returning the assigned LSN.
func (w *WAL) Append(rec *LogRecord) (uint64, error) {
	rec.LSN = w.lsn.Next()
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return 0, err
	}
	if _, err := w.file.Write(rec.encode()); err != nil {
		return 0, err
	}
	return rec.LSN, w.file.Sync()
}

// Scan reads every record in the WAL in LSN (append) order.
func (w *WAL) Scan() ([]*LogRecord, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(w.file)
	if err != nil {
		return nil, err
	}
	var recs []*LogRecord
	for off := 0; off < len(data); {
		if len(data)-off < recordHeaderSize {
			return nil, fmt.Errorf("corrupt WAL: dangling header at offset %d", off)
		}
		beforeLen := binary.LittleEndian.Uint32(data[off+41:])
		afterLen := binary.LittleEndian.Uint32(data[off+45:])
		total := recordHeaderSize + int(beforeLen) + int(afterLen)
		if len(data)-off < total {
			return nil, fmt.Errorf("corrupt WAL: truncated record at offset %d", off)
		}
		rec, err := decodeRecord(data[off : off+total])
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec)
		off += total
	}
	return recs, nil
}
