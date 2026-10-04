// Package aries implements a minimal ARIES-style write-ahead log,
// crash recovery (Analysis / Redo / Undo with CLRs) and an offline
// formal-constraint inspector that asserts structural invariants.
package aries

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
)

// LogType enumerates WAL record kinds.
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
	return fmt.Sprintf("UNKNOWN(%d)", uint8(t))
}

// TxnStatus is the lifecycle state tracked in the transaction table.
type TxnStatus uint8

const (
	TxnRunning TxnStatus = iota
	TxnCommitted
	TxnAborted
)

func (s TxnStatus) String() string {
	switch s {
	case TxnRunning:
		return "RUNNING"
	case TxnCommitted:
		return "COMMITTED"
	case TxnAborted:
		return "ABORTED"
	}
	return fmt.Sprintf("UNKNOWN(%d)", uint8(s))
}

// TxnRecord is one entry of the transaction table (ATT).
type TxnRecord struct {
	TxnID   uint64
	Status  TxnStatus
	LastLSN uint64
}

// CheckpointPayload is the ATT + DPT snapshot carried by a checkpoint record.
type CheckpointPayload struct {
	Transactions map[uint64]TxnRecord // txnID -> ATT entry
	DirtyPages   map[uint64]uint64    // pageID -> recLSN
}

// Clone deep-copies the payload so snapshots are immune to later mutation.
func (c *CheckpointPayload) Clone() *CheckpointPayload {
	if c == nil {
		return nil
	}
	out := &CheckpointPayload{
		Transactions: make(map[uint64]TxnRecord, len(c.Transactions)),
		DirtyPages:   make(map[uint64]uint64, len(c.DirtyPages)),
	}
	for k, v := range c.Transactions {
		out.Transactions[k] = v
	}
	for k, v := range c.DirtyPages {
		out.DirtyPages[k] = v
	}
	return out
}

// LogRecord is a single WAL entry. LSN is the byte offset of the record
// inside the log file, which makes LSNs globally ordered and seekable.
type LogRecord struct {
	LSN         uint64
	PrevLSN     uint64 // previous record of the same transaction (0 = none)
	TxnID       uint64
	Type        LogType
	PageID      uint64 // UPDATE / CLR only
	UndoNextLSN uint64 // CLR only: next record to undo (0 = txn fully undone)
	Before      []byte // UPDATE: before image; CLR: before image of undone update
	After       []byte // UPDATE / CLR: redo image
	Checkpoint  *CheckpointPayload
}

func (r *LogRecord) String() string {
	base := fmt.Sprintf("LSN=%d %-10s txn=%d prev=%d", r.LSN, r.Type, r.TxnID, r.PrevLSN)
	switch r.Type {
	case LogUpdate, LogCLR:
		base += fmt.Sprintf(" page=%d", r.PageID)
		if r.Type == LogCLR {
			base += fmt.Sprintf(" undoNext=%d", r.UndoNextLSN)
		}
	case LogCheckpoint:
		base += fmt.Sprintf(" att=%d dpt=%d", len(r.Checkpoint.Transactions), len(r.Checkpoint.DirtyPages))
	}
	return base
}

// ---- serialization ----

func writeU64(buf *bytes.Buffer, v uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	buf.Write(b[:])
}

func writeU32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func (r *LogRecord) encode() []byte {
	var body bytes.Buffer
	writeU64(&body, r.LSN)
	writeU64(&body, r.PrevLSN)
	writeU64(&body, r.TxnID)
	body.WriteByte(byte(r.Type))
	writeU64(&body, r.PageID)
	writeU64(&body, r.UndoNextLSN)
	writeU32(&body, uint32(len(r.Before)))
	body.Write(r.Before)
	writeU32(&body, uint32(len(r.After)))
	body.Write(r.After)
	if r.Checkpoint == nil {
		body.WriteByte(0)
	} else {
		body.WriteByte(1)
		cp := r.Checkpoint
		txnIDs := make([]uint64, 0, len(cp.Transactions))
		for id := range cp.Transactions {
			txnIDs = append(txnIDs, id)
		}
		sort.Slice(txnIDs, func(i, j int) bool { return txnIDs[i] < txnIDs[j] })
		writeU32(&body, uint32(len(txnIDs)))
		for _, id := range txnIDs {
			tr := cp.Transactions[id]
			writeU64(&body, tr.TxnID)
			body.WriteByte(byte(tr.Status))
			writeU64(&body, tr.LastLSN)
		}
		pageIDs := make([]uint64, 0, len(cp.DirtyPages))
		for id := range cp.DirtyPages {
			pageIDs = append(pageIDs, id)
		}
		sort.Slice(pageIDs, func(i, j int) bool { return pageIDs[i] < pageIDs[j] })
		writeU32(&body, uint32(len(pageIDs)))
		for _, id := range pageIDs {
			writeU64(&body, id)
			writeU64(&body, cp.DirtyPages[id])
		}
	}
	var frame bytes.Buffer
	writeU32(&frame, uint32(body.Len()))
	frame.Write(body.Bytes())
	return frame.Bytes()
}

type byteReader struct {
	b   []byte
	off int
}

func (r *byteReader) u64() (uint64, error) {
	if r.off+8 > len(r.b) {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint64(r.b[r.off:])
	r.off += 8
	return v, nil
}

func (r *byteReader) u32() (uint32, error) {
	if r.off+4 > len(r.b) {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint32(r.b[r.off:])
	r.off += 4
	return v, nil
}

func (r *byteReader) u8() (uint8, error) {
	if r.off+1 > len(r.b) {
		return 0, io.ErrUnexpectedEOF
	}
	v := r.b[r.off]
	r.off++
	return v, nil
}

func (r *byteReader) bytes(n int) ([]byte, error) {
	if n < 0 || r.off+n > len(r.b) {
		return nil, io.ErrUnexpectedEOF
	}
	v := make([]byte, n)
	copy(v, r.b[r.off:r.off+n])
	r.off += n
	return v, nil
}

func decodeRecord(lsn uint64, body []byte) (*LogRecord, error) {
	r := &byteReader{b: body}
	rec := &LogRecord{}
	var err error
	if rec.LSN, err = r.u64(); err != nil {
		return nil, err
	}
	if rec.LSN != lsn {
		return nil, fmt.Errorf("wal: record at offset %d claims LSN %d", lsn, rec.LSN)
	}
	if rec.PrevLSN, err = r.u64(); err != nil {
		return nil, err
	}
	if rec.TxnID, err = r.u64(); err != nil {
		return nil, err
	}
	typ, err := r.u8()
	if err != nil {
		return nil, err
	}
	rec.Type = LogType(typ)
	if rec.PageID, err = r.u64(); err != nil {
		return nil, err
	}
	if rec.UndoNextLSN, err = r.u64(); err != nil {
		return nil, err
	}
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	if rec.Before, err = r.bytes(int(n)); err != nil {
		return nil, err
	}
	if n, err = r.u32(); err != nil {
		return nil, err
	}
	if rec.After, err = r.bytes(int(n)); err != nil {
		return nil, err
	}
	hasCP, err := r.u8()
	if err != nil {
		return nil, err
	}
	if hasCP == 1 {
		cp := &CheckpointPayload{
			Transactions: make(map[uint64]TxnRecord),
			DirtyPages:   make(map[uint64]uint64),
		}
		cnt, err := r.u32()
		if err != nil {
			return nil, err
		}
		for i := uint32(0); i < cnt; i++ {
			var tr TxnRecord
			if tr.TxnID, err = r.u64(); err != nil {
				return nil, err
			}
			st, err := r.u8()
			if err != nil {
				return nil, err
			}
			tr.Status = TxnStatus(st)
			if tr.LastLSN, err = r.u64(); err != nil {
				return nil, err
			}
			cp.Transactions[tr.TxnID] = tr
		}
		if cnt, err = r.u32(); err != nil {
			return nil, err
		}
		for i := uint32(0); i < cnt; i++ {
			pageID, err := r.u64()
			if err != nil {
				return nil, err
			}
			recLSN, err := r.u64()
			if err != nil {
				return nil, err
			}
			cp.DirtyPages[pageID] = recLSN
		}
		rec.Checkpoint = cp
	}
	if r.off != len(body) {
		return nil, fmt.Errorf("wal: %d trailing bytes in record at LSN %d", len(body)-r.off, lsn)
	}
	return rec, nil
}

// WAL is an append-only, file-backed write-ahead log.
// Append and Records are safe for concurrent use.
type WAL struct {
	mu   sync.Mutex
	path string
}

// walMagic is the 8-byte file header. It reserves offset 0 so that LSN 0
// can never be a real record, keeping 0 usable as the "no predecessor"
// sentinel in PrevLSN / UndoNextLSN chains.
var walMagic = []byte("ARIESLG1")

// OpenWAL opens (or creates) the log file at path.
func OpenWAL(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return &WAL{path: path}, nil
}

// Append assigns the record its LSN (current end-of-file offset),
// persists it and fsyncs. The record is mutated in place with its LSN.
func (w *WAL) Append(rec *LogRecord) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	f, err := os.OpenFile(w.path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if st.Size() == 0 {
		if _, err := f.Write(walMagic); err != nil {
			return 0, err
		}
		st, err = f.Stat()
		if err != nil {
			return 0, err
		}
	}
	rec.LSN = uint64(st.Size())
	if _, err := f.Write(rec.encode()); err != nil {
		return 0, err
	}
	if err := f.Sync(); err != nil {
		return 0, err
	}
	return rec.LSN, nil
}

// Records reads the whole log into memory, in LSN order.
func (w *WAL) Records() ([]*LogRecord, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return ReadWALFile(w.path)
}

// ReadWALFile parses every record in the file at path.
func ReadWALFile(path string) ([]*LogRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []*LogRecord
	hdr8 := make([]byte, len(walMagic))
	if _, err := io.ReadFull(f, hdr8); err != nil {
		if err == io.EOF {
			return out, nil // brand-new empty log
		}
		return nil, fmt.Errorf("wal: truncated header: %v", err)
	}
	if !bytes.Equal(hdr8, walMagic) {
		return nil, fmt.Errorf("wal: bad magic %q, not an ARIES log", hdr8)
	}
	off := uint64(len(walMagic))
	var hdr [4]byte
	for {
		_, err := io.ReadFull(f, hdr[:])
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("wal: truncated header at offset %d", off)
		}
		n := binary.LittleEndian.Uint32(hdr[:])
		body := make([]byte, n)
		if _, err := io.ReadFull(f, body); err != nil {
			return nil, fmt.Errorf("wal: truncated record at offset %d", off)
		}
		rec, err := decodeRecord(off, body)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
		off += 4 + uint64(n)
	}
}
