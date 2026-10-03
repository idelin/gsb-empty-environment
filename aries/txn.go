package aries

import (
	"fmt"
	"sync"
)

// TxnMgr coordinates transactions: it writes WAL records ahead of page
// changes (the WAL rule) and maintains the in-memory transaction table.
// All methods are safe for concurrent use.
type TxnMgr struct {
	mu     sync.Mutex
	wal    *WAL
	pool   *BufferPool
	last   map[uint64]uint64    // txn -> last LSN
	status map[uint64]TxnStatus // txn -> status
	next   uint64
}

// NewTxnMgr creates a transaction manager. After a crash, create a fresh
// TxnMgr only *after* Recover has completed.
func NewTxnMgr(wal *WAL, pool *BufferPool) *TxnMgr {
	return &TxnMgr{
		wal:    wal,
		pool:   pool,
		last:   make(map[uint64]uint64),
		status: make(map[uint64]TxnStatus),
		next:   1,
	}
}

// Begin starts a new transaction and returns its id.
func (tm *TxnMgr) Begin() uint64 {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	id := tm.next
	tm.next++
	tm.last[id] = 0
	tm.status[id] = StatusRunning
	return id
}

// Write logs an UPDATE record and applies the new image to the buffer
// pool. oldData must be the current page contents at that offset (read it
// with ReadAt); it is what undo will restore.
func (tm *TxnMgr) Write(txn uint64, pageID uint32, offset uint32, oldData, newData []byte) (uint64, error) {
	tm.mu.Lock()
	if _, ok := tm.status[txn]; !ok {
		tm.mu.Unlock()
		return 0, fmt.Errorf("txn %d not active", txn)
	}
	rec := &LogRecord{
		PrevLSN: tm.last[txn],
		TxnID:   txn,
		Type:    LogUpdate,
		PageID:  pageID,
		Offset:  offset,
		OldData: append([]byte(nil), oldData...),
		NewData: append([]byte(nil), newData...),
	}
	lsn, err := tm.wal.Append(rec)
	if err != nil {
		tm.mu.Unlock()
		return 0, err
	}
	tm.last[txn] = lsn
	tm.mu.Unlock()
	// Page update happens after the log record is assigned (WAL rule: the
	// log record reaches stable storage no later than the page does,
	// enforced by FlushAll ordering in Checkpoint/Commit paths).
	if err := tm.pool.ApplyUpdate(pageID, lsn, int(offset), newData); err != nil {
		return 0, err
	}
	return lsn, nil
}

// ReadAt reads current page contents through the buffer pool.
func (tm *TxnMgr) ReadAt(pageID uint32, offset, n int) ([]byte, error) {
	return tm.pool.ReadAt(pageID, offset, n)
}

// Commit writes COMMIT, forces the log, and writes END.
func (tm *TxnMgr) Commit(txn uint64) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if _, ok := tm.status[txn]; !ok {
		return fmt.Errorf("txn %d not active", txn)
	}
	if _, err := tm.wal.Append(&LogRecord{
		PrevLSN: tm.last[txn],
		TxnID:   txn,
		Type:    LogCommit,
	}); err != nil {
		return err
	}
	tm.status[txn] = StatusCommitted
	// Commit rule: all of the transaction's log records must be durable
	// before the commit is acknowledged.
	if err := tm.wal.Flush(); err != nil {
		return err
	}
	endLSN, err := tm.wal.Append(&LogRecord{
		PrevLSN: tm.last[txn],
		TxnID:   txn,
		Type:    LogEnd,
	})
	if err != nil {
		return err
	}
	_ = endLSN
	delete(tm.last, txn)
	delete(tm.status, txn)
	return nil
}

// StartAbort writes the ABORT record; follow with RollbackStep calls (or
// Abort) to undo the transaction's effects.
func (tm *TxnMgr) StartAbort(txn uint64) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if _, ok := tm.status[txn]; !ok {
		return fmt.Errorf("txn %d not active", txn)
	}
	if _, err := tm.wal.Append(&LogRecord{
		PrevLSN: tm.last[txn],
		TxnID:   txn,
		Type:    LogAbort,
	}); err != nil {
		return err
	}
	tm.status[txn] = StatusAborting
	return nil
}

// RollbackStep undoes exactly one not-yet-undone update of txn, writing a
// CLR. It reports whether more undo work remains. Exported so tests (and
// crash simulators) can interleave a crash mid-abort.
func (tm *TxnMgr) RollbackStep(txn uint64) (bool, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	lsn, ok := tm.last[txn]
	if !ok {
		return false, fmt.Errorf("txn %d not active", txn)
	}
	if lsn == 0 {
		return false, nil
	}
	rec := tm.wal.Record(lsn)
	if rec == nil {
		return false, fmt.Errorf("txn %d: missing log record LSN %d", txn, lsn)
	}
	switch rec.Type {
	case LogUpdate:
		clr := &LogRecord{
			PrevLSN:     lsn,
			TxnID:       txn,
			Type:        LogCLR,
			PageID:      rec.PageID,
			Offset:      rec.Offset,
			OldData:     append([]byte(nil), rec.NewData...),
			NewData:     append([]byte(nil), rec.OldData...),
			UndoNextLSN: rec.PrevLSN,
		}
		clrLSN, err := tm.wal.Append(clr)
		if err != nil {
			return false, err
		}
		if err := tm.pool.ApplyUpdate(rec.PageID, clrLSN, int(rec.Offset), rec.OldData); err != nil {
			return false, err
		}
		tm.last[txn] = clrLSN
		return rec.PrevLSN != 0, nil
	case LogCLR:
		// Already undone; jump straight to the next record to undo.
		tm.last[txn] = rec.UndoNextLSN
		return rec.UndoNextLSN != 0, nil
	default:
		// ABORT record or similar: step over it via the prev chain.
		tm.last[txn] = rec.PrevLSN
		return rec.PrevLSN != 0, nil
	}
}

// Abort rolls the transaction fully back and writes END.
func (tm *TxnMgr) Abort(txn uint64) error {
	if err := tm.StartAbort(txn); err != nil {
		return err
	}
	for {
		more, err := tm.RollbackStep(txn)
		if err != nil {
			return err
		}
		if !more {
			break
		}
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if _, err := tm.wal.Append(&LogRecord{
		PrevLSN: tm.last[txn],
		TxnID:   txn,
		Type:    LogEnd,
	}); err != nil {
		return err
	}
	delete(tm.last, txn)
	delete(tm.status, txn)
	return nil
}

// Checkpoint performs a fuzzy checkpoint: it logs BEGIN_CHKPT, snapshots
// the transaction table and dirty page table into an END_CHKPT record,
// and forces the log. It does not quiesce transactions. It returns the
// LSN of the END_CHKPT record.
func (tm *TxnMgr) Checkpoint() (uint64, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if _, err := tm.wal.Append(&LogRecord{Type: LogBeginCheckpoint}); err != nil {
		return 0, err
	}
	txnTab := make(map[uint64]TxnTableEntry, len(tm.last))
	for id, lsn := range tm.last {
		txnTab[id] = TxnTableEntry{LastLSN: lsn, Status: tm.status[id]}
	}
	end := &LogRecord{
		Type:         LogEndCheckpoint,
		TxnTable:     txnTab,
		DirtyPageTab: tm.pool.DirtyPageTable(),
	}
	lsn, err := tm.wal.Append(end)
	if err != nil {
		return 0, err
	}
	if err := tm.wal.Flush(); err != nil {
		return 0, err
	}
	return lsn, nil
}

// ActiveTxns returns the ids of transactions that have not ended.
func (tm *TxnMgr) ActiveTxns() map[uint64]bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	out := make(map[uint64]bool, len(tm.status))
	for id := range tm.status {
		out[id] = true
	}
	return out
}
