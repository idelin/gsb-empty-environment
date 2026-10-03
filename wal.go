package aries

import (
	"encoding/binary"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"sync"
)

// LogType identifies the kind of a WAL record.
type LogType uint8

const (
	LogUpdate LogType = 1 + iota
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

// TxnStatus is the recovery-time state of a transaction.
type TxnStatus uint8

const (
	StatusRunning TxnStatus = iota
	StatusCommitting
	StatusAborting
)

func (s TxnStatus) String() string {
	switch s {
	case StatusRunning:
		return "RUNNING"
	case StatusCommitting:
		return "COMMITTING"
	case StatusAborting:
		return "ABORTING"
	}
	return "UNKNOWN"
}

// CheckpointTxn is the transaction-table snapshot stored in a checkpoint.
type CheckpointTxn struct {
	LastLSN uint64
	Status  TxnStatus
}

// CheckpointData is a fuzzy checkpoint snapshot of the DPT and TT.
type CheckpointData struct {
	DPT map[uint64]uint64        // pageID -> recLSN
	TT  map[uint64]CheckpointTxn // txnID -> {lastLSN, status}
}

// LogRecord is a single WAL record. LSN is the byte offset of the record
// in the log file and is assigned by WAL.Append.
type LogRecord struct {
	LSN        uint64
	PrevLSN    uint64
	TxnID      uint64
	Type       LogType
	PageID     uint64 // UPDATE / CLR
	Offset     uint32 // cell offset within the page
	OldValue   uint32 // UPDATE: before-image; CLR: value being removed
	NewValue   uint32 // UPDATE: after-image;  CLR: value being restored
	UndoNext   uint64 // CLR only: next LSN to undo for this txn
	Checkpoint *CheckpointData
}

func (r *LogRecord) encode() []byte {
	buf := make([]byte, 0, 64)
	buf = append(buf, byte(r.Type))
	buf = binary.AppendUvarint(buf, r.PrevLSN)
	buf = binary.AppendUvarint(buf, r.TxnID)
	switch r.Type {
	case LogUpdate, LogCLR:
		buf = binary.AppendUvarint(buf, r.PageID)
		buf = binary.AppendUvarint(buf, uint64(r.Offset))
		buf = binary.AppendUvarint(buf, uint64(r.OldValue))
		buf = binary.AppendUvarint(buf, uint64(r.NewValue))
		buf = binary.AppendUvarint(buf, r.UndoNext)
	case LogCheckpoint:
		c := r.Checkpoint
		buf = binary.AppendUvarint(buf, uint64(len(c.DPT)))
		for _, pid := range slices.Sorted(maps.Keys(c.DPT)) {
			buf = binary.AppendUvarint(buf, pid)
			buf = binary.AppendUvarint(buf, c.DPT[pid])
		}
		buf = binary.AppendUvarint(buf, uint64(len(c.TT)))
		for _, tid := range slices.Sorted(maps.Keys(c.TT)) {
			buf = binary.AppendUvarint(buf, tid)
			buf = binary.AppendUvarint(buf, c.TT[tid].LastLSN)
			buf = append(buf, byte(c.TT[tid].Status))
		}
	}
	out := binary.AppendUvarint(nil, uint64(len(buf)))
	return append(out, buf...)
}

func decodeRecord(lsn uint64, data []byte) (*LogRecord, error) {
	r := &LogRecord{LSN: lsn}
	if len(data) == 0 {
		return nil, fmt.Errorf("wal: empty record at LSN %d", lsn)
	}
	r.Type = LogType(data[0])
	data = data[1:]
	read := func(dst *uint64) error {
		v, n := binary.Uvarint(data)
		if n <= 0 {
			return fmt.Errorf("wal: truncated record at LSN %d", lsn)
		}
		data = data[n:]
		*dst = v
		return nil
	}
	if err := read(&r.PrevLSN); err != nil {
		return nil, err
	}
	if err := read(&r.TxnID); err != nil {
		return nil, err
	}
	switch r.Type {
	case LogUpdate, LogCLR:
		var off, oldV, newV uint64
		for _, dst := range []*uint64{&r.PageID, &off, &oldV, &newV, &r.UndoNext} {
			if err := read(dst); err != nil {
				return nil, err
			}
		}
		r.Offset = uint32(off)
		r.OldValue = uint32(oldV)
		r.NewValue = uint32(newV)
	case LogCheckpoint:
		c := &CheckpointData{DPT: map[uint64]uint64{}, TT: map[uint64]CheckpointTxn{}}
		var nDPT uint64
		if err := read(&nDPT); err != nil {
			return nil, err
		}
		for i := uint64(0); i < nDPT; i++ {
			var pid, recLSN uint64
			if err := read(&pid); err != nil {
				return nil, err
			}
			if err := read(&recLSN); err != nil {
				return nil, err
			}
			c.DPT[pid] = recLSN
		}
		var nTT uint64
		if err := read(&nTT); err != nil {
			return nil, err
		}
		for i := uint64(0); i < nTT; i++ {
			var tid, lastLSN uint64
			if err := read(&tid); err != nil {
				return nil, err
			}
			if err := read(&lastLSN); err != nil {
				return nil, err
			}
			if len(data) == 0 {
				return nil, fmt.Errorf("wal: truncated checkpoint at LSN %d", lsn)
			}
			c.TT[tid] = CheckpointTxn{LastLSN: lastLSN, Status: TxnStatus(data[0])}
			data = data[1:]
		}
		r.Checkpoint = c
	case LogCommit, LogAbort, LogEnd:
	default:
		return nil, fmt.Errorf("wal: unknown record type %d at LSN %d", r.Type, lsn)
	}
	return r, nil
}

// walMagic is the 8-byte file header. It reserves LSN 0 as the "no LSN"
// sentinel so the first real record always has LSN > 0.
var walMagic = []byte("ARIESW1\n")

// WAL is an append-only write-ahead log backed by a file. Every append is
// fsynced before it returns, satisfying the WAL durability rule.
type WAL struct {
	mu  sync.Mutex
	f   *os.File
	off uint64
}

// OpenWAL opens (or creates) the log file at path for appending.
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
	if st.Size() == 0 {
		if _, err := f.WriteAt(walMagic, 0); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return nil, err
		}
		return &WAL{f: f, off: uint64(len(walMagic))}, nil
	}
	return &WAL{f: f, off: uint64(st.Size())}, nil
}

// Append encodes rec, writes it at the current end of the log, fsyncs,
// assigns rec.LSN and returns it. Safe for concurrent use.
func (w *WAL) Append(rec *LogRecord) uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec.LSN = w.off
	data := rec.encode()
	if _, err := w.f.WriteAt(data, int64(w.off)); err != nil {
		panic(fmt.Errorf("wal: append failed: %w", err))
	}
	if err := w.f.Sync(); err != nil {
		panic(fmt.Errorf("wal: sync failed: %w", err))
	}
	w.off += uint64(len(data))
	return rec.LSN
}

// Records reads and decodes every record in the log in LSN order.
func (w *WAL) Records() ([]*LogRecord, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(w.f)
	if err != nil {
		return nil, err
	}
	if len(data) < len(walMagic) || string(data[:len(walMagic)]) != string(walMagic) {
		return nil, fmt.Errorf("wal: bad file header")
	}
	var records []*LogRecord
	off := uint64(len(walMagic))
	for off < uint64(len(data)) {
		length, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return nil, fmt.Errorf("wal: bad length prefix at offset %d", off)
		}
		start := off + uint64(n)
		end := start + length
		if end > uint64(len(data)) {
			return nil, fmt.Errorf("wal: truncated record at offset %d", off)
		}
		rec, err := decodeRecord(off, data[start:end])
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
		off = end
	}
	return records, nil
}

// LastCheckpoint returns the LSN and payload of the most recent checkpoint
// record, or (0, nil) if the log contains none.
func (w *WAL) LastCheckpoint() (uint64, *CheckpointData, error) {
	records, err := w.Records()
	if err != nil {
		return 0, nil, err
	}
	var lsn uint64
	var ckpt *CheckpointData
	for _, r := range records {
		if r.Type == LogCheckpoint {
			lsn, ckpt = r.LSN, r.Checkpoint
		}
	}
	return lsn, ckpt, nil
}

// Close closes the underlying file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}
