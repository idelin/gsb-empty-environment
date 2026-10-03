package recovery

import (
	"fmt"
	"sync"

	"aries/storage"
	"aries/wal"
)

// System is a concurrency-safe facade for normal transaction
// processing: it appends WAL records and applies page updates through
// the buffer pool while tracking the in-memory ATT/DPT needed to
// write checkpoints.
type System struct {
	mu   sync.Mutex
	Log  *wal.LogManager
	Pool *storage.BufferPool
	att  map[int]uint64 // txn -> lastLSN
	dpt  map[int]uint64 // page -> recLSN
}

// NewSystem creates a transaction-processing facade.
func NewSystem(log *wal.LogManager, pool *storage.BufferPool) *System {
	return &System{
		Log:  log,
		Pool: pool,
		att:  make(map[int]uint64),
		dpt:  make(map[int]uint64),
	}
}

// Begin starts a transaction.
func (s *System) Begin(txnID int) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.att[txnID]; ok {
		return 0, fmt.Errorf("system: txn %d already active", txnID)
	}
	lsn := s.Log.Append(&wal.Record{Type: wal.TypeBegin, TxnID: txnID})
	s.att[txnID] = lsn
	return lsn, nil
}

// Update logs and applies a page update, capturing the before image
// from the buffer pool. It returns the update record's LSN.
func (s *System) Update(txnID, pageID int, after []byte) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.att[txnID]
	if !ok {
		return 0, fmt.Errorf("system: txn %d not active", txnID)
	}
	before, _, err := s.Pool.Fetch(pageID)
	if err != nil {
		return 0, err
	}
	lsn := s.Log.Append(&wal.Record{
		Type:    wal.TypeUpdate,
		TxnID:   txnID,
		PrevLSN: prev,
		PageID:  pageID,
		Before:  before,
		After:   after,
	})
	if err := s.Pool.Apply(pageID, after, lsn); err != nil {
		return 0, err
	}
	s.att[txnID] = lsn
	if _, ok := s.dpt[pageID]; !ok {
		s.dpt[pageID] = lsn
	}
	return lsn, nil
}

// Commit writes COMMIT and END records for a transaction.
func (s *System) Commit(txnID int) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.att[txnID]
	if !ok {
		return 0, fmt.Errorf("system: txn %d not active", txnID)
	}
	commitLSN := s.Log.Append(&wal.Record{Type: wal.TypeCommit, TxnID: txnID, PrevLSN: prev})
	s.Log.Append(&wal.Record{Type: wal.TypeEnd, TxnID: txnID, PrevLSN: commitLSN})
	delete(s.att, txnID)
	return commitLSN, nil
}

// Abort writes an ABORT record. The actual rollback is left to the
// recovery engine, mirroring a crash mid-abort.
func (s *System) Abort(txnID int) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.att[txnID]
	if !ok {
		return 0, fmt.Errorf("system: txn %d not active", txnID)
	}
	lsn := s.Log.Append(&wal.Record{Type: wal.TypeAbort, TxnID: txnID, PrevLSN: prev})
	s.att[txnID] = lsn
	return lsn, nil
}

// Checkpoint appends a checkpoint record carrying the current ATT and
// DPT, and returns its LSN.
func (s *System) Checkpoint() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	att := make(map[int]uint64, len(s.att))
	for txn, last := range s.att {
		att[txn] = last
	}
	dpt := make(map[int]uint64, len(s.dpt))
	for page, recLSN := range s.dpt {
		dpt[page] = recLSN
	}
	return s.Log.Append(&wal.Record{Type: wal.TypeCheckpoint, ATT: att, DPT: dpt}), nil
}
