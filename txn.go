package aries

import (
	"fmt"
	"sync"
)

// TxnManager drives normal (pre-crash) operation: it writes WAL records and
// keeps a small buffer pool whose dirty pages may or may not reach disk
// before a crash. It is safe for concurrent use.
type TxnManager struct {
	WAL  *WAL
	Disk *PageStore

	mu     sync.Mutex
	buf    map[uint64]*Page // buffer pool
	dirty  map[uint64]LSN   // pageID -> recLSN (first dirtying record since flush)
	txns   map[uint64]LSN   // txnID -> lastLSN
	status map[uint64]TxnStatus
}

// NewTxnManager creates a transaction manager over wal and disk.
func NewTxnManager(wal *WAL, disk *PageStore) *TxnManager {
	return &TxnManager{
		WAL:    wal,
		Disk:   disk,
		buf:    make(map[uint64]*Page),
		dirty:  make(map[uint64]LSN),
		txns:   make(map[uint64]LSN),
		status: make(map[uint64]TxnStatus),
	}
}

// Begin starts a transaction.
func (tm *TxnManager) Begin(txnID uint64) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if _, ok := tm.txns[txnID]; ok {
		return fmt.Errorf("aries: txn %d already active", txnID)
	}
	lsn, err := tm.WAL.Append(&LogRecord{Type: RecBegin, TxnID: txnID})
	if err != nil {
		return err
	}
	tm.txns[txnID] = lsn
	tm.status[txnID] = TxnRunning
	return nil
}

// Update logs and applies a forward update of pageID to data.
func (tm *TxnManager) Update(txnID, pageID uint64, data []byte) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	page, err := tm.bufferLocked(pageID)
	if err != nil {
		return err
	}
	before := append([]byte(nil), page.Data...)
	after := append([]byte(nil), page.Data...)
	copy(after, data)
	rec := &LogRecord{
		Type:    RecUpdate,
		TxnID:   txnID,
		PrevLSN: tm.txns[txnID],
		PageID:  pageID,
		Before:  before,
		After:   after,
	}
	lsn, err := tm.WAL.Append(rec)
	if err != nil {
		return err
	}
	copy(page.Data, data)
	page.LSN = lsn
	if _, ok := tm.dirty[pageID]; !ok {
		tm.dirty[pageID] = lsn
	}
	tm.txns[txnID] = lsn
	return nil
}

// Commit records the commit decision. If writeEnd is false the END record is
// left unwritten, modelling a transaction that committed but had not fully
// finished when the crash hit.
func (tm *TxnManager) Commit(txnID uint64, writeEnd bool) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	lsn, err := tm.WAL.Append(&LogRecord{
		Type: RecCommit, TxnID: txnID, PrevLSN: tm.txns[txnID],
	})
	if err != nil {
		return err
	}
	tm.txns[txnID] = lsn
	tm.status[txnID] = TxnCommitted
	if writeEnd {
		return tm.endLocked(txnID)
	}
	return nil
}

// Abort writes the ABORT record and undoes up to maxSteps updates
// (maxSteps < 0 undoes everything and ends the transaction). A crash can be
// simulated by passing a small maxSteps and never finishing the abort.
func (tm *TxnManager) Abort(txnID uint64, maxSteps int) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	lsn, err := tm.WAL.Append(&LogRecord{
		Type: RecAbort, TxnID: txnID, PrevLSN: tm.txns[txnID],
	})
	if err != nil {
		return err
	}
	tm.txns[txnID] = lsn
	tm.status[txnID] = TxnAborting
	// The ABORT record itself is not an undoable action; skip it so the
	// undo steps below operate on updates/CLRs only.
	last, err := tm.WAL.ReadAt(lsn)
	if err != nil {
		return err
	}
	tm.txns[txnID] = last.PrevLSN
	steps := 0
	for maxSteps < 0 || steps < maxSteps {
		done, err := tm.undoStepLocked(txnID)
		if err != nil {
			return err
		}
		steps++
		if done {
			return tm.endLocked(txnID)
		}
	}
	return nil
}

// undoStepLocked undoes exactly one record of txnID. It reports true when
// the transaction is fully undone.
func (tm *TxnManager) undoStepLocked(txnID uint64) (bool, error) {
	rec, err := tm.WAL.ReadAt(tm.txns[txnID])
	if err != nil {
		return false, err
	}
	switch rec.Type {
	case RecAbort:
		// The ABORT record itself is skipped; undo continues at the last
		// update/CLR of the transaction.
		tm.txns[txnID] = rec.PrevLSN
		return false, nil
	case RecUpdate:
		clr := &LogRecord{
			Type:     RecCLR,
			TxnID:    txnID,
			PrevLSN:  tm.txns[txnID],
			PageID:   rec.PageID,
			UndoNext: rec.PrevLSN,
			After:    rec.Before,
		}
		clrLSN, err := tm.WAL.Append(clr)
		if err != nil {
			return false, err
		}
		page, err := tm.bufferLocked(rec.PageID)
		if err != nil {
			return false, err
		}
		copy(page.Data, rec.Before)
		page.LSN = clrLSN
		tm.txns[txnID] = clrLSN
		return false, nil
	case RecCLR:
		tm.txns[txnID] = rec.UndoNext
		if rec.UndoNext == NilLSN {
			return true, nil
		}
		return false, nil
	case RecBegin:
		return true, nil
	}
	return false, fmt.Errorf("aries: unexpected %s in undo chain of txn %d", rec.Type, txnID)
}

func (tm *TxnManager) endLocked(txnID uint64) error {
	if _, err := tm.WAL.Append(&LogRecord{
		Type: RecEnd, TxnID: txnID, PrevLSN: tm.txns[txnID],
	}); err != nil {
		return err
	}
	delete(tm.txns, txnID)
	delete(tm.status, txnID)
	return nil
}

// FlushPage writes a buffered page to disk (steal policy: uncommitted data
// may be flushed).
func (tm *TxnManager) FlushPage(pageID uint64) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	page, ok := tm.buf[pageID]
	if !ok {
		return nil
	}
	if err := tm.Disk.WritePage(page); err != nil {
		return err
	}
	delete(tm.dirty, pageID)
	return nil
}

// Checkpoint writes a fuzzy checkpoint with the current transaction table
// and dirty page table.
func (tm *TxnManager) Checkpoint() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	rec := &LogRecord{
		Type: RecCheckpoint,
		Txns: make(map[uint64]TxnSnapshot, len(tm.txns)),
		DPT:  make(map[uint64]LSN, len(tm.dirty)),
	}
	for id, last := range tm.txns {
		rec.Txns[id] = TxnSnapshot{LastLSN: last, Status: tm.status[id]}
	}
	for pageID, recLSN := range tm.dirty {
		rec.DPT[pageID] = recLSN
	}
	_, err := tm.WAL.Append(rec)
	return err
}

func (tm *TxnManager) bufferLocked(pageID uint64) (*Page, error) {
	page, ok := tm.buf[pageID]
	if !ok {
		var err error
		page, err = tm.Disk.ReadPage(pageID)
		if err != nil {
			return nil, err
		}
		tm.buf[pageID] = page
	}
	return page, nil
}
