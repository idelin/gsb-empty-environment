package aries

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sync"
)

// LogType identifies the kind of a log record.
type LogType uint8

const (
	LogUpdate LogType = iota + 1
	LogCLR
	LogBegin
	LogCommit
	LogAbort
	LogEnd
	LogCheckpoint
)

func (t LogType) String() string {
	switch t {
	case LogUpdate:
		return "UPDATE"
	case LogCLR:
		return "CLR"
	case LogBegin:
		return "BEGIN"
	case LogCommit:
		return "COMMIT"
	case LogAbort:
		return "ABORT"
	case LogEnd:
		return "END"
	case LogCheckpoint:
		return "CHECKPOINT"
	}
	return "UNKNOWN"
}

// TxnStatus is the state of a transaction in the transaction table.
type TxnStatus uint8

const (
	StatusRunning TxnStatus = iota
	StatusCommitted
)

// TxnEntry is one row of the transaction table.
type TxnEntry struct {
	ID      uint64
	LastLSN uint64
	Status  TxnStatus
}

// LogRecord is a single WAL entry. LSNs are dense sequence numbers
// assigned by the LogManager at append time.
type LogRecord struct {
	LSN         uint64
	PrevLSN     uint64 // previous LSN of the same transaction, 0 = none
	TxnID       uint64
	Type        LogType
	PageID      int64  // -1 when not page-related
	Offset      uint32 // byte offset inside the page
	UndoNextLSN uint64 // CLR only: next record to undo
	OldData     []byte // before image (UPDATE)
	NewData     []byte // after image (UPDATE) / undo image (CLR)

	// CHECKPOINT payload: snapshot of the transaction table and DPT.
	Txns []TxnEntry
	DPT  map[uint64]uint64 // pageID -> recLSN
}

func (r *LogRecord) String() string {
	base := fmt.Sprintf("LSN=%d %-10s txn=%d prev=%d", r.LSN, r.Type, r.TxnID, r.PrevLSN)
	if r.PageID >= 0 {
		base += fmt.Sprintf(" page=%d off=%d", r.PageID, r.Offset)
	}
	if r.Type == LogCLR {
		base += fmt.Sprintf(" undoNext=%d", r.UndoNextLSN)
	}
	return base
}

func appendU64(b []byte, v uint64) []byte {
	var t [8]byte
	binary.BigEndian.PutUint64(t[:], v)
	return append(b, t[:]...)
}

func appendU32(b []byte, v uint32) []byte {
	var t [4]byte
	binary.BigEndian.PutUint32(t[:], v)
	return append(b, t[:]...)
}

func (r *LogRecord) encode() []byte {
	buf := make([]byte, 0, 64+len(r.OldData)+len(r.NewData))
	buf = appendU64(buf, r.LSN)
	buf = appendU64(buf, r.PrevLSN)
	buf = appendU64(buf, r.TxnID)
	buf = append(buf, byte(r.Type))
	buf = appendU64(buf, uint64(r.PageID))
	buf = appendU32(buf, r.Offset)
	buf = appendU64(buf, r.UndoNextLSN)
	buf = appendU32(buf, uint32(len(r.OldData)))
	buf = append(buf, r.OldData...)
	buf = appendU32(buf, uint32(len(r.NewData)))
	buf = append(buf, r.NewData...)
	if r.Type == LogCheckpoint {
		buf = appendU32(buf, uint32(len(r.Txns)))
		for _, te := range r.Txns {
			buf = appendU64(buf, te.ID)
			buf = appendU64(buf, te.LastLSN)
			buf = append(buf, byte(te.Status))
		}
		buf = appendU32(buf, uint32(len(r.DPT)))
		for pageID, recLSN := range r.DPT {
			buf = appendU64(buf, pageID)
			buf = appendU64(buf, recLSN)
		}
	}
	return buf
}

type decoder struct {
	b   []byte
	off int
	err error
}

func (d *decoder) take(n int) []byte {
	if d.err != nil {
		return nil
	}
	if d.off+n > len(d.b) {
		d.err = errors.New("aries: truncated log record")
		return nil
	}
	out := d.b[d.off : d.off+n]
	d.off += n
	return out
}

func (d *decoder) u64() uint64 {
	p := d.take(8)
	if p == nil {
		return 0
	}
	return binary.BigEndian.Uint64(p)
}

func (d *decoder) u32() uint32 {
	p := d.take(4)
	if p == nil {
		return 0
	}
	return binary.BigEndian.Uint32(p)
}

func (d *decoder) u8() uint8 {
	p := d.take(1)
	if p == nil {
		return 0
	}
	return p[0]
}

func (d *decoder) bytes(n uint32) []byte {
	p := d.take(int(n))
	if p == nil {
		return nil
	}
	out := make([]byte, len(p))
	copy(out, p)
	return out
}

func decodeRecord(b []byte) (*LogRecord, error) {
	d := &decoder{b: b}
	r := &LogRecord{}
	r.LSN = d.u64()
	r.PrevLSN = d.u64()
	r.TxnID = d.u64()
	r.Type = LogType(d.u8())
	r.PageID = int64(d.u64())
	r.Offset = d.u32()
	r.UndoNextLSN = d.u64()
	r.OldData = d.bytes(d.u32())
	r.NewData = d.bytes(d.u32())
	if r.Type == LogCheckpoint {
		nTxns := d.u32()
		for i := uint32(0); i < nTxns; i++ {
			id := d.u64()
			last := d.u64()
			st := TxnStatus(d.u8())
			r.Txns = append(r.Txns, TxnEntry{ID: id, LastLSN: last, Status: st})
		}
		nDPT := d.u32()
		r.DPT = make(map[uint64]uint64, nDPT)
		for i := uint32(0); i < nDPT; i++ {
			pageID := d.u64()
			recLSN := d.u64()
			r.DPT[pageID] = recLSN
		}
	}
	if d.err != nil {
		return nil, d.err
	}
	return r, nil
}

// LogManager appends records to the WAL file. Every record is framed as
// [payloadLen u32][crc32 u32][payload] and fsynced before Append returns,
// so a committed transaction's log is always durable. A torn or corrupt
// tail (from a crash mid-write) is truncated on open.
type LogManager struct {
	mu      sync.Mutex
	f       *os.File
	nextLSN uint64
	writeAt int64
	log     []*LogRecord // in-memory copy, indexed by LSN
}

// OpenLogManager opens (or creates) the WAL file, scans all valid records
// and truncates any torn tail left by a crash.
func OpenLogManager(path string) (*LogManager, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	lm := &LogManager{f: f}
	if err := lm.load(); err != nil {
		f.Close()
		return nil, err
	}
	return lm, nil
}

func (lm *LogManager) load() error {
	var validEnd int64
	header := make([]byte, 8)
	for {
		off, err := lm.f.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
		if _, err := io.ReadFull(lm.f, header); err != nil {
			break // clean EOF or torn header
		}
		length := binary.BigEndian.Uint32(header[0:4])
		crc := binary.BigEndian.Uint32(header[4:8])
		if length > 1<<30 {
			break // corrupt length
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(lm.f, payload); err != nil {
			break // torn payload
		}
		if crc32.ChecksumIEEE(payload) != crc {
			break // corrupt payload
		}
		rec, err := decodeRecord(payload)
		if err != nil {
			break
		}
		lm.log = append(lm.log, rec)
		lm.nextLSN = rec.LSN + 1
		validEnd = off + 8 + int64(length)
	}
	if err := lm.f.Truncate(validEnd); err != nil {
		return err
	}
	lm.writeAt = validEnd
	return nil
}

// Append assigns the next LSN, persists the record and fsyncs the WAL.
func (lm *LogManager) Append(r *LogRecord) (uint64, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	r.LSN = lm.nextLSN
	if r.Type != LogUpdate && r.Type != LogCLR {
		r.PageID = -1
	}
	payload := r.encode()
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(hdr[4:8], crc32.ChecksumIEEE(payload))
	frame := append(hdr[:], payload...)
	if _, err := lm.f.WriteAt(frame, lm.writeAt); err != nil {
		return 0, err
	}
	if err := lm.f.Sync(); err != nil {
		return 0, err
	}
	lm.writeAt += int64(len(frame))
	lm.log = append(lm.log, r)
	lm.nextLSN++
	return r.LSN, nil
}

// Get returns the record with the given LSN, or nil.
func (lm *LogManager) Get(lsn uint64) *LogRecord {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if int(lsn) >= len(lm.log) {
		return nil
	}
	return lm.log[lsn]
}

// All returns all records in LSN order.
func (lm *LogManager) All() []*LogRecord {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	out := make([]*LogRecord, len(lm.log))
	copy(out, lm.log)
	return out
}

// NextLSN returns the LSN that will be assigned to the next append.
func (lm *LogManager) NextLSN() uint64 {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.nextLSN
}

func (lm *LogManager) Close() error {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.f.Close()
}
