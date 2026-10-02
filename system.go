package aries

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// System is a single-machine transactional storage manager with
// write-ahead logging and ARIES recovery.
//
// Files in the data directory:
//
//	wal.log  - the write-ahead log
//	data.db  - page storage
//
// WAL protocol: every update is logged (and the log fsynced) before the
// in-memory page is modified. Commit is durable because the COMMIT record
// is fsynced before Commit returns; dirty pages may be flushed lazily.
type System struct {
	mu       sync.Mutex
	lm       *LogManager
	dm       *DiskManager
	bp       *BufferPool
	txnTable map[uint64]*TxnEntry // active transactions
	dpt      map[uint64]uint64    // dirty page table: pageID -> recLSN
	nextTxn  uint64
	closed   bool
}

// Open opens (or creates) a System rooted at dir. It does not run
// recovery; call Recover after a crash.
func Open(dir string) (*System, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	lm, err := OpenLogManager(filepath.Join(dir, "wal.log"))
	if err != nil {
		return nil, err
	}
	dm, err := OpenDiskManager(filepath.Join(dir, "data.db"))
	if err != nil {
		lm.Close()
		return nil, err
	}
	s := &System{
		lm:       lm,
		dm:       dm,
		bp:       NewBufferPool(dm),
		txnTable: make(map[uint64]*TxnEntry),
		dpt:      make(map[uint64]uint64),
		nextTxn:  1,
	}
	for _, r := range lm.All() {
		if r.TxnID >= s.nextTxn {
			s.nextTxn = r.TxnID + 1
		}
	}
	return s, nil
}

// Begin starts a new transaction and returns its ID.
func (s *System) Begin() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOpen(); err != nil {
		return 0, err
	}
	id := s.nextTxn
	s.nextTxn++
	lsn, err := s.lm.Append(&LogRecord{Type: LogBegin, TxnID: id})
	if err != nil {
		return 0, err
	}
	s.txnTable[id] = &TxnEntry{ID: id, LastLSN: lsn, Status: StatusRunning}
	return id, nil
}

// Write logs an update and then applies it to the in-memory page
// (log-first, i.e. the WAL rule). The page stays dirty in the buffer pool
// until a checkpoint or recovery flushes it.
func (s *System) Write(txnID, pageID uint64, offset int, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOpen(); err != nil {
		return err
	}
	te, ok := s.txnTable[txnID]
	if !ok {
		return fmt.Errorf("aries: unknown or finished transaction %d", txnID)
	}
	if offset < PageHeaderSize || offset+len(data) > PageSize {
		return fmt.Errorf("aries: write out of bounds: offset=%d len=%d", offset, len(data))
	}
	p, err := s.bp.Fetch(pageID)
	if err != nil {
		return err
	}
	old := make([]byte, len(data))
	copy(old, p.Data[offset:offset+len(data)])
	rec := &LogRecord{
		Type:    LogUpdate,
		TxnID:   txnID,
		PrevLSN: te.LastLSN,
		PageID:  int64(pageID),
		Offset:  uint32(offset),
		OldData: old,
		NewData: append([]byte(nil), data...),
	}
	// 1. log first (fsynced inside Append) ...
	lsn, err := s.lm.Append(rec)
	if err != nil {
		return err
	}
	// 2. ... then mutate memory.
	te.LastLSN = lsn
	copy(p.Data[offset:], data)
	p.PageLSN = lsn
	p.Dirty = true
	if _, ok := s.dpt[pageID]; !ok {
		s.dpt[pageID] = lsn
	}
	return nil
}

// Read returns n bytes of a page starting at offset.
func (s *System) Read(pageID uint64, offset, n int) ([]byte, error) {
	if offset < 0 || offset+n > PageSize {
		return nil, fmt.Errorf("aries: read out of bounds: offset=%d len=%d", offset, n)
	}
	p, err := s.bp.Fetch(pageID)
	if err != nil {
		return nil, err
	}
	out := make([]byte, n)
	copy(out, p.Data[offset:offset+n])
	return out, nil
}

// Commit makes a transaction durable: the COMMIT record is fsynced before
// Commit returns, even though dirty pages may still be only in memory.
func (s *System) Commit(txnID uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOpen(); err != nil {
		return err
	}
	te, ok := s.txnTable[txnID]
	if !ok {
		return fmt.Errorf("aries: unknown or finished transaction %d", txnID)
	}
	lsn, err := s.lm.Append(&LogRecord{Type: LogCommit, TxnID: txnID, PrevLSN: te.LastLSN})
	if err != nil {
		return err
	}
	te.LastLSN = lsn
	te.Status = StatusCommitted
	if _, err := s.lm.Append(&LogRecord{Type: LogEnd, TxnID: txnID, PrevLSN: lsn}); err != nil {
		return err
	}
	delete(s.txnTable, txnID)
	return nil
}

// Abort rolls a live transaction back in memory, writing compensation log
// records (CLRs) for every undone update.
func (s *System) Abort(txnID uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOpen(); err != nil {
		return err
	}
	te, ok := s.txnTable[txnID]
	if !ok {
		return fmt.Errorf("aries: unknown or finished transaction %d", txnID)
	}
	abortLSN, err := s.lm.Append(&LogRecord{Type: LogAbort, TxnID: txnID, PrevLSN: te.LastLSN})
	if err != nil {
		return err
	}
	last := abortLSN
	for lsn := te.LastLSN; lsn != 0; {
		r := s.lm.Get(lsn)
		if r == nil {
			return fmt.Errorf("aries: missing log record %d", lsn)
		}
		switch r.Type {
		case LogUpdate:
			clr := &LogRecord{
				Type:        LogCLR,
				TxnID:       txnID,
				PrevLSN:     last,
				PageID:      r.PageID,
				Offset:      r.Offset,
				NewData:     r.OldData,
				UndoNextLSN: r.PrevLSN,
			}
			clrLSN, err := s.lm.Append(clr)
			if err != nil {
				return err
			}
			p, err := s.bp.Fetch(uint64(r.PageID))
			if err != nil {
				return err
			}
			copy(p.Data[r.Offset:], r.OldData)
			p.PageLSN = clrLSN
			p.Dirty = true
			last = clrLSN
			lsn = r.PrevLSN
		case LogCLR:
			lsn = r.UndoNextLSN
		default: // BEGIN
			lsn = 0
		}
	}
	if _, err := s.lm.Append(&LogRecord{Type: LogEnd, TxnID: txnID, PrevLSN: last}); err != nil {
		return err
	}
	delete(s.txnTable, txnID)
	return nil
}

// Checkpoint writes a checkpoint record (snapshot of the transaction table
// and DPT) and flushes all dirty pages.
func (s *System) Checkpoint() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOpen(); err != nil {
		return err
	}
	txns := make([]TxnEntry, 0, len(s.txnTable))
	for _, te := range s.txnTable {
		txns = append(txns, *te)
	}
	dpt := make(map[uint64]uint64, len(s.dpt))
	for k, v := range s.dpt {
		dpt[k] = v
	}
	if _, err := s.lm.Append(&LogRecord{Type: LogCheckpoint, Txns: txns, DPT: dpt}); err != nil {
		return err
	}
	if _, err := s.bp.FlushAll(); err != nil {
		return err
	}
	s.dpt = make(map[uint64]uint64)
	return nil
}

// Crash simulates a sudden power loss: file handles are closed (the WAL is
// already fsynced per record, so it survives) but all dirty pages in the
// buffer pool and all in-memory tables are lost. The System must not be
// used afterwards; Open a fresh one and call Recover.
func (s *System) Crash() {
	s.lm.Close()
	s.dm.Close()
	s.closed = true
}

// Close performs a clean shutdown: checkpoint, then close files.
func (s *System) Close() error {
	if err := s.Checkpoint(); err != nil {
		return err
	}
	s.lm.Close()
	s.dm.Close()
	s.closed = true
	return nil
}

// LogRecords returns all WAL records (for inspection and tests).
func (s *System) LogRecords() []*LogRecord {
	return s.lm.All()
}

// DirtyPageTable returns a snapshot of the in-memory DPT.
func (s *System) DirtyPageTable() map[uint64]uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[uint64]uint64, len(s.dpt))
	for k, v := range s.dpt {
		out[k] = v
	}
	return out
}

// TxnTable returns a snapshot of the in-memory transaction table.
func (s *System) TxnTable() []TxnEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TxnEntry, 0, len(s.txnTable))
	for _, te := range s.txnTable {
		out = append(out, *te)
	}
	return out
}

func (s *System) checkOpen() error {
	if s.closed {
		return errors.New("aries: system is closed")
	}
	return nil
}
