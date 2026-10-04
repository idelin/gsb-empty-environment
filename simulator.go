package aries

import (
	"fmt"
	"sync"
)

// Simulator drives a "live" database: it runs transactions against a
// buffer pool, emits WAL records and can take checkpoints. Dropping the
// Simulator (and its buffer pool) without flushing simulates a crash.
type Simulator struct {
	mu      sync.Mutex
	wal     *WAL
	bp      *BufferPool
	nextTxn uint64
	prevLSN map[uint64]uint64 // txnID -> last LSN (per-txn chain tail)
	status  map[uint64]TxnStatus
}

// NewSimulator creates a simulator over the given WAL and buffer pool.
func NewSimulator(wal *WAL, bp *BufferPool) *Simulator {
	return &Simulator{
		wal:     wal,
		bp:      bp,
		nextTxn: 1,
		prevLSN: make(map[uint64]uint64),
		status:  make(map[uint64]TxnStatus),
	}
}

// Begin starts a transaction and returns its ID.
func (s *Simulator) Begin() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextTxn
	s.nextTxn++
	s.status[id] = TxnRunning
	return id
}

// Update logs an UPDATE record and applies the after-image to the buffer pool.
func (s *Simulator) Update(txnID, pageID uint64, payload []byte) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status[txnID] != TxnRunning {
		return 0, fmt.Errorf("sim: txn %d not running", txnID)
	}
	if len(payload) > PayloadSize {
		return 0, fmt.Errorf("sim: payload of %d bytes exceeds %d", len(payload), PayloadSize)
	}
	page, err := s.bp.Fetch(pageID)
	if err != nil {
		return 0, err
	}
	before := make([]byte, PayloadSize)
	copy(before, page.Payload)
	after := make([]byte, PayloadSize)
	copy(after, payload)
	rec := &LogRecord{
		PrevLSN: s.prevLSN[txnID],
		TxnID:   txnID,
		Type:    LogUpdate,
		PageID:  pageID,
		Before:  before,
		After:   after,
	}
	lsn, err := s.wal.Append(rec)
	if err != nil {
		return 0, err
	}
	if err := s.bp.Apply(pageID, lsn, after); err != nil {
		return 0, err
	}
	s.prevLSN[txnID] = lsn
	return lsn, nil
}

// Commit logs COMMIT and END for a transaction.
func (s *Simulator) Commit(txnID uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status[txnID] != TxnRunning {
		return fmt.Errorf("sim: txn %d not running", txnID)
	}
	c := &LogRecord{PrevLSN: s.prevLSN[txnID], TxnID: txnID, Type: LogCommit}
	lsn, err := s.wal.Append(c)
	if err != nil {
		return err
	}
	e := &LogRecord{PrevLSN: lsn, TxnID: txnID, Type: LogEnd}
	if _, err := s.wal.Append(e); err != nil {
		return err
	}
	s.status[txnID] = TxnCommitted
	delete(s.prevLSN, txnID)
	return nil
}

// Abort logs ABORT for a transaction (the actual undo happens at recovery).
func (s *Simulator) Abort(txnID uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status[txnID] != TxnRunning {
		return fmt.Errorf("sim: txn %d not running", txnID)
	}
	a := &LogRecord{PrevLSN: s.prevLSN[txnID], TxnID: txnID, Type: LogAbort}
	if _, err := s.wal.Append(a); err != nil {
		return err
	}
	s.status[txnID] = TxnAborted
	return nil
}

// Checkpoint writes a checkpoint record carrying the current ATT and DPT.
func (s *Simulator) Checkpoint() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	att := make(map[uint64]TxnRecord)
	for id, st := range s.status {
		if st == TxnRunning || st == TxnAborted {
			att[id] = TxnRecord{TxnID: id, Status: st, LastLSN: s.prevLSN[id]}
		}
	}
	rec := &LogRecord{
		Type:       LogCheckpoint,
		Checkpoint: &CheckpointPayload{Transactions: att, DirtyPages: s.bp.DirtyPageTable()},
	}
	return s.wal.Append(rec)
}

// FlushPage forces one buffered page to disk (simulates a background flush).
func (s *Simulator) FlushPage(pageID uint64) error { return s.bp.Flush(pageID) }

// BufferPool exposes the pool so tests can flush selectively.
func (s *Simulator) BufferPool() *BufferPool { return s.bp }
