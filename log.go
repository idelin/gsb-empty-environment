package aries

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"sync"
)

// LSN is a monotonically increasing log sequence number.
type LSN uint64

// TxID identifies a transaction.
type TxID uint64

// PageID identifies a database page.
type PageID uint64

// NilLSN marks the absence of a previous log record.
const NilLSN LSN = 0

// RecordType enumerates the WAL record kinds.
type RecordType byte

const (
	RecUpdate RecordType = iota + 1
	RecCommit
	RecAbort
	RecEnd
	RecCLR
	RecCheckpoint
)

func (t RecordType) String() string {
	switch t {
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
	return "UNKNOWN"
}

// TxnStatus is the lifecycle state tracked in the transaction table.
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
	return "UNKNOWN"
}

// TxnInfo is one entry of the transaction table.
type TxnInfo struct {
	Status  TxnStatus
	LastLSN LSN
}

// LogRecord is a single WAL entry. Only the fields relevant to its
// Type are populated.
type LogRecord struct {
	LSN     LSN
	Type    RecordType
	TxID    TxID
	PrevLSN LSN

	// Update / CLR payload.
	PageID      PageID
	Offset      uint32
	Before      []byte // update only: undo image
	After       []byte // update: redo image; CLR: redo-only undo image
	UndoNextLSN LSN    // CLR only

	// Checkpoint payload.
	TxnTable map[TxID]TxnInfo
	DPT      map[PageID]LSN
}

func (r *LogRecord) String() string {
	base := fmt.Sprintf("LSN=%d %-10s txn=%d prev=%d", r.LSN, r.Type, r.TxID, r.PrevLSN)
	switch r.Type {
	case RecUpdate:
		return fmt.Sprintf("%s page=%d off=%d before=%q after=%q", base, r.PageID, r.Offset, r.Before, r.After)
	case RecCLR:
		return fmt.Sprintf("%s page=%d off=%d after=%q undoNext=%d", base, r.PageID, r.Offset, r.After, r.UndoNextLSN)
	}
	return base
}

func writeUint64(buf *bytes.Buffer, v uint64) { binary.Write(buf, binary.LittleEndian, v) }
func writeUint32(buf *bytes.Buffer, v uint32) { binary.Write(buf, binary.LittleEndian, v) }

func writeBytes(buf *bytes.Buffer, b []byte) {
	writeUint32(buf, uint32(len(b)))
	buf.Write(b)
}

func readUint64(r *bytes.Reader) uint64 {
	var v uint64
	binary.Read(r, binary.LittleEndian, &v)
	return v
}

func readUint32(r *bytes.Reader) uint32 {
	var v uint32
	binary.Read(r, binary.LittleEndian, &v)
	return v
}

func readBytes(r *bytes.Reader) []byte {
	n := readUint32(r)
	b := make([]byte, n)
	r.Read(b)
	return b
}

// encodeRecord serializes a record into a framed, checksummed blob:
// [payloadLen uint32][crc32 uint32][payload...]
func encodeRecord(r *LogRecord) []byte {
	buf := new(bytes.Buffer)
	buf.WriteByte(byte(r.Type))
	writeUint64(buf, uint64(r.LSN))
	writeUint64(buf, uint64(r.TxID))
	writeUint64(buf, uint64(r.PrevLSN))
	switch r.Type {
	case RecUpdate:
		writeUint64(buf, uint64(r.PageID))
		writeUint32(buf, r.Offset)
		writeBytes(buf, r.Before)
		writeBytes(buf, r.After)
	case RecCLR:
		writeUint64(buf, uint64(r.PageID))
		writeUint32(buf, r.Offset)
		writeUint64(buf, uint64(r.UndoNextLSN))
		writeBytes(buf, r.After)
	case RecCheckpoint:
		writeUint32(buf, uint32(len(r.TxnTable)))
		for id, info := range r.TxnTable {
			writeUint64(buf, uint64(id))
			buf.WriteByte(byte(info.Status))
			writeUint64(buf, uint64(info.LastLSN))
		}
		writeUint32(buf, uint32(len(r.DPT)))
		for pid, lsn := range r.DPT {
			writeUint64(buf, uint64(pid))
			writeUint64(buf, uint64(lsn))
		}
	}
	payload := buf.Bytes()
	frame := make([]byte, 8+len(payload))
	binary.LittleEndian.PutUint32(frame[0:4], uint32(len(payload)))
	binary.LittleEndian.PutUint32(frame[4:8], crc32.ChecksumIEEE(payload))
	copy(frame[8:], payload)
	return frame
}

func decodeRecord(payload []byte) *LogRecord {
	r := &LogRecord{}
	rd := bytes.NewReader(payload)
	t, _ := rd.ReadByte()
	r.Type = RecordType(t)
	r.LSN = LSN(readUint64(rd))
	r.TxID = TxID(readUint64(rd))
	r.PrevLSN = LSN(readUint64(rd))
	switch r.Type {
	case RecUpdate:
		r.PageID = PageID(readUint64(rd))
		r.Offset = readUint32(rd)
		r.Before = readBytes(rd)
		r.After = readBytes(rd)
	case RecCLR:
		r.PageID = PageID(readUint64(rd))
		r.Offset = readUint32(rd)
		r.UndoNextLSN = LSN(readUint64(rd))
		r.After = readBytes(rd)
	case RecCheckpoint:
		r.TxnTable = make(map[TxID]TxnInfo)
		r.DPT = make(map[PageID]LSN)
		for i, n := uint32(0), readUint32(rd); i < n; i++ {
			id := TxID(readUint64(rd))
			status, _ := rd.ReadByte()
			last := LSN(readUint64(rd))
			r.TxnTable[id] = TxnInfo{Status: TxnStatus(status), LastLSN: last}
		}
		for i, n := uint32(0), readUint32(rd); i < n; i++ {
			pid := PageID(readUint64(rd))
			r.DPT[pid] = LSN(readUint64(rd))
		}
	}
	return r
}

// maxFramePayload guards against garbage lengths in a torn tail write.
const maxFramePayload = 1 << 30

// readFrame reads one framed record at byte offset off. It returns an
// error on EOF, short read, bad length or CRC mismatch (torn write).
func readFrame(f *os.File, off int64) (*LogRecord, int, error) {
	hdr := make([]byte, 8)
	if _, err := f.ReadAt(hdr, off); err != nil {
		return nil, 0, err
	}
	n := binary.LittleEndian.Uint32(hdr[0:4])
	if n == 0 || n > maxFramePayload {
		return nil, 0, fmt.Errorf("bad frame length %d at offset %d", n, off)
	}
	crc := binary.LittleEndian.Uint32(hdr[4:8])
	payload := make([]byte, n)
	if _, err := f.ReadAt(payload, off+8); err != nil {
		return nil, 0, err
	}
	if crc32.ChecksumIEEE(payload) != crc {
		return nil, 0, fmt.Errorf("crc mismatch at offset %d", off)
	}
	return decodeRecord(payload), 8 + int(n), nil
}

// LogManager appends records to the WAL file, forces them to stable
// storage on demand, and supports sequential scans plus random access
// by LSN (needed by undo).
type LogManager struct {
	mu      sync.Mutex
	f       *os.File
	nextLSN LSN
	maxTxID TxID
	flushed LSN
	records []*LogRecord
	index   map[LSN]*LogRecord
}

func openLogManager(path string) (*LogManager, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	lm := &LogManager{
		f:       f,
		nextLSN: 1,
		index:   make(map[LSN]*LogRecord),
	}
	// Scan existing records, rebuild the in-memory index and stop at
	// the first torn/incomplete frame (crash tail).
	var validEnd int64
	var off int64
	for {
		rec, n, err := readFrame(f, off)
		if err != nil {
			break
		}
		lm.records = append(lm.records, rec)
		lm.index[rec.LSN] = rec
		if rec.LSN >= lm.nextLSN {
			lm.nextLSN = rec.LSN + 1
		}
		if rec.TxID > lm.maxTxID {
			lm.maxTxID = rec.TxID
		}
		off += int64(n)
		validEnd = off
	}
	if err := f.Truncate(validEnd); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(validEnd, 0); err != nil {
		f.Close()
		return nil, err
	}
	return lm, nil
}

// Append assigns the next LSN to rec and writes it through to the OS.
func (lm *LogManager) Append(rec *LogRecord) LSN {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	rec.LSN = lm.nextLSN
	lm.nextLSN++
	if rec.TxID > lm.maxTxID {
		lm.maxTxID = rec.TxID
	}
	lm.f.Write(encodeRecord(rec))
	lm.records = append(lm.records, rec)
	lm.index[rec.LSN] = rec
	return rec.LSN
}

// Flush forces all log records up to (and including) upto to stable
// storage. This is the WAL force rule: commit and page writes must
// flush the log first.
func (lm *LogManager) Flush(upto LSN) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if upto <= lm.flushed {
		return nil
	}
	if err := lm.f.Sync(); err != nil {
		return err
	}
	lm.flushed = upto
	return nil
}

// Sync forces everything written so far to stable storage.
func (lm *LogManager) Sync() error {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if err := lm.f.Sync(); err != nil {
		return err
	}
	lm.flushed = lm.nextLSN - 1
	return nil
}

// Get returns the record with the given LSN, or nil.
func (lm *LogManager) Get(lsn LSN) *LogRecord {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.index[lsn]
}

// Records returns all valid records in LSN order.
func (lm *LogManager) Records() []*LogRecord {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	out := make([]*LogRecord, len(lm.records))
	copy(out, lm.records)
	return out
}

func (lm *LogManager) Close() error {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.f.Close()
}
