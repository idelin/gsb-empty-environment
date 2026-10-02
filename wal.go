// Package aries implements a compact ARIES-style write-ahead log and
// crash-recovery engine (Analysis / Redo / Undo) together with a
// deadlock-free multi-node conflict arbiter (wound-wait lock manager).
package aries

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"sort"
	"sync"
)

// LSN is a logical log sequence number. It equals the 1-based byte offset of
// the record inside the WAL file, so NilLSN (0) can never be a real record.
type LSN uint64

// NilLSN terminates PrevLSN / UndoNextLSN chains.
const NilLSN LSN = 0

// Log record types.
const (
	RecBegin byte = iota + 1
	RecUpdate
	RecCommit
	RecAbort
	RecCLR
	RecEnd
	RecCheckpoint
)

// RecTypeName renders a record type for logging and demos.
func RecTypeName(t byte) string {
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
	default:
		return fmt.Sprintf("UNKNOWN(%d)", t)
	}
}

// LogRecord is a single WAL entry. DPT/TT are only populated for
// RecCheckpoint records; UndoNextLSN is only meaningful for RecCLR.
type LogRecord struct {
	LSN         LSN
	Type        byte
	TxnID       int64
	PrevLSN     LSN
	PageID      int64
	Offset      uint32
	OldData     []byte
	NewData     []byte
	UndoNextLSN LSN
	DPT         map[int64]LSN // checkpoint: pageID -> recLSN
	TT          map[int64]LSN // checkpoint: txnID -> lastLSN
}

func (r *LogRecord) String() string {
	base := fmt.Sprintf("LSN=%d %-10s txn=%d prev=%d", r.LSN, RecTypeName(r.Type), r.TxnID, r.PrevLSN)
	switch r.Type {
	case RecUpdate, RecCLR:
		base += fmt.Sprintf(" page=%d off=%d old=%q new=%q", r.PageID, r.Offset, r.OldData, r.NewData)
		if r.Type == RecCLR {
			base += fmt.Sprintf(" undoNext=%d", r.UndoNextLSN)
		}
	case RecCheckpoint:
		base += fmt.Sprintf(" dpt=%v tt=%v", r.DPT, r.TT)
	}
	return base
}

// ErrCorruptWAL is returned when a record fails its CRC check. Readers treat
// the first corrupt frame as the (torn) tail of the log.
var ErrCorruptWAL = errors.New("aries: corrupt WAL record")

const frameHeaderSize = 8 // crc(4) + payloadLen(4)

func appendU32(b []byte, v uint32) []byte {
	var t [4]byte
	binary.BigEndian.PutUint32(t[:], v)
	return append(b, t[:]...)
}

func appendU64(b []byte, v uint64) []byte {
	var t [8]byte
	binary.BigEndian.PutUint64(t[:], v)
	return append(b, t[:]...)
}

func (r *LogRecord) marshal() []byte {
	p := make([]byte, 0, 64)
	p = append(p, r.Type)
	p = appendU64(p, uint64(r.TxnID))
	p = appendU64(p, uint64(r.PrevLSN))
	p = appendU64(p, uint64(r.PageID))
	p = appendU32(p, r.Offset)
	p = appendU64(p, uint64(r.UndoNextLSN))
	p = appendU32(p, uint32(len(r.OldData)))
	p = append(p, r.OldData...)
	p = appendU32(p, uint32(len(r.NewData)))
	p = append(p, r.NewData...)
	if r.Type == RecCheckpoint {
		pageIDs := make([]int64, 0, len(r.DPT))
		for id := range r.DPT {
			pageIDs = append(pageIDs, id)
		}
		sort.Slice(pageIDs, func(i, j int) bool { return pageIDs[i] < pageIDs[j] })
		p = appendU32(p, uint32(len(pageIDs)))
		for _, id := range pageIDs {
			p = appendU64(p, uint64(id))
			p = appendU64(p, uint64(r.DPT[id]))
		}
		txnIDs := make([]int64, 0, len(r.TT))
		for id := range r.TT {
			txnIDs = append(txnIDs, id)
		}
		sort.Slice(txnIDs, func(i, j int) bool { return txnIDs[i] < txnIDs[j] })
		p = appendU32(p, uint32(len(txnIDs)))
		for _, id := range txnIDs {
			p = appendU64(p, uint64(id))
			p = appendU64(p, uint64(r.TT[id]))
		}
	}
	frame := make([]byte, 0, frameHeaderSize+len(p))
	frame = appendU32(frame, crc32.ChecksumIEEE(p))
	frame = appendU32(frame, uint32(len(p)))
	return append(frame, p...)
}

type decoder struct {
	buf []byte
	pos int
}

func (d *decoder) u8() byte { v := d.buf[d.pos]; d.pos++; return v }
func (d *decoder) u32() uint32 {
	v := binary.BigEndian.Uint32(d.buf[d.pos:])
	d.pos += 4
	return v
}
func (d *decoder) u64() uint64 {
	v := binary.BigEndian.Uint64(d.buf[d.pos:])
	d.pos += 8
	return v
}
func (d *decoder) bytes(n int) []byte {
	v := make([]byte, n)
	copy(v, d.buf[d.pos:d.pos+n])
	d.pos += n
	return v
}

func unmarshalPayload(p []byte) *LogRecord {
	d := &decoder{buf: p}
	r := &LogRecord{}
	r.Type = d.u8()
	r.TxnID = int64(d.u64())
	r.PrevLSN = LSN(d.u64())
	r.PageID = int64(d.u64())
	r.Offset = d.u32()
	r.UndoNextLSN = LSN(d.u64())
	r.OldData = d.bytes(int(d.u32()))
	r.NewData = d.bytes(int(d.u32()))
	if r.Type == RecCheckpoint {
		r.DPT = make(map[int64]LSN)
		for n := int(d.u32()); n > 0; n-- {
			r.DPT[int64(d.u64())] = LSN(d.u64())
		}
		r.TT = make(map[int64]LSN)
		for n := int(d.u32()); n > 0; n-- {
			r.TT[int64(d.u64())] = LSN(d.u64())
		}
	}
	return r
}

// WAL is an append-only, CRC-framed log file. All appends are serialized and
// synchronously flushed, so a process crash can only lose nothing that was
// ever acknowledged (the OS may still reorder, which Sync covers).
type WAL struct {
	mu   sync.Mutex
	f    *os.File
	path string
	size int64
}

// CreateWAL opens (truncating) a WAL file at path.
func CreateWAL(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &WAL{f: f, path: path}, nil
}

// OpenWAL opens an existing WAL for appending during recovery (CLRs and END
// records are written while rolling back losers).
func OpenWAL(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, 2); err != nil {
		f.Close()
		return nil, err
	}
	return &WAL{f: f, path: path, size: st.Size()}, nil
}

// Append serializes rec, assigns its LSN and durably writes it.
func (w *WAL) Append(rec *LogRecord) LSN {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec.LSN = LSN(w.size + 1)
	frame := rec.marshal()
	// Positioned write: readers may have moved the shared file offset.
	if _, err := w.f.WriteAt(frame, w.size); err != nil {
		panic(fmt.Sprintf("aries: WAL append failed: %v", err))
	}
	w.size += int64(len(frame))
	return rec.LSN
}

// Sync flushes the log to stable storage (called on commit and checkpoint).
func (w *WAL) Sync() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.f.Sync(); err != nil {
		panic(fmt.Sprintf("aries: WAL sync failed: %v", err))
	}
}

// Close flushes and closes the underlying file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.f.Sync(); err != nil {
		return err
	}
	return w.f.Close()
}

// ReadAll scans the WAL from the beginning and returns every intact record in
// LSN order. A CRC mismatch or truncated frame is treated as the torn tail of
// the log (normal after a crash mid-append) and terminates the scan without
// an error.
func (w *WAL) ReadAll() ([]*LogRecord, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	raw, err := os.ReadFile(w.path)
	if err != nil {
		return nil, err
	}
	var records []*LogRecord
	off := 0
	for {
		if len(raw)-off < frameHeaderSize {
			break
		}
		payloadLen := int(binary.BigEndian.Uint32(raw[off+4:]))
		if payloadLen <= 0 || len(raw)-off-frameHeaderSize < payloadLen {
			break // torn tail
		}
		payload := raw[off+frameHeaderSize : off+frameHeaderSize+payloadLen]
		if crc32.ChecksumIEEE(payload) != binary.BigEndian.Uint32(raw[off:]) {
			break // corrupt tail
		}
		rec := unmarshalPayload(payload)
		rec.LSN = LSN(off + 1)
		records = append(records, rec)
		off += frameHeaderSize + payloadLen
	}
	return records, nil
}
