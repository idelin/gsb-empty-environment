// Package recovery implements the ARIES crash-recovery protocol:
// Analysis (rebuild DirtyPageTable and TransactionTable), Redo (repeat
// history) and Undo (rollback losers writing compensation log records),
// plus a small transaction manager used to drive the WAL before a crash.
package recovery

import (
	"errors"
	"sync"

	"aries/storage"
	"aries/wal"
)

// TxnManager runs transactions against a WAL + page store. It is safe for
// concurrent use by multiple goroutines.
type TxnManager struct {
	Wal   *wal.WAL
	Store *storage.PageStore

	mu      sync.Mutex
	prevLSN map[uint64]uint64 // txnID -> last LSN written by that txn
	nextID  uint64
}

// NewTxnManager wires a transaction manager to a WAL and a page store.
func NewTxnManager(w *wal.WAL, s *storage.PageStore) *TxnManager {
	return &TxnManager{
		Wal:     w,
		Store:   s,
		prevLSN: make(map[uint64]uint64),
		nextID:  1,
	}
}

// Begin starts a transaction and returns its ID.
func (tm *TxnManager) Begin() uint64 {
	tm.mu.Lock()
	id := tm.nextID
	tm.nextID++
	tm.mu.Unlock()
	lsn := tm.Wal.Append(&wal.Record{Type: wal.TypeBegin, TxnID: id})
	tm.mu.Lock()
	tm.prevLSN[id] = lsn
	tm.mu.Unlock()
	return id
}

// Update logs and applies a byte-range update to a page.
func (tm *TxnManager) Update(txnID uint64, pageID uint32, offset int, data []byte) error {
	if offset < 0 || offset+len(data) > storage.PageSize {
		return errors.New("recovery: update out of page bounds")
	}
	tm.mu.Lock()
	prev := tm.prevLSN[txnID]
	tm.mu.Unlock()

	page := tm.Store.Fetch(pageID)
	old := append([]byte(nil), page.Data[offset:offset+len(data)]...)
	lsn := tm.Wal.Append(&wal.Record{
		Type:    wal.TypeUpdate,
		TxnID:   txnID,
		PrevLSN: prev,
		PageID:  pageID,
		Offset:  uint32(offset),
		OldData: old,
		NewData: append([]byte(nil), data...),
	})
	copy(page.Data[offset:], data)
	page.LSN = lsn
	tm.Store.UnpinDirty(page)

	tm.mu.Lock()
	tm.prevLSN[txnID] = lsn
	tm.mu.Unlock()
	return nil
}

// Commit makes a transaction durable: the COMMIT record is appended and
// flushed atomically. If the flush fails (e.g. disk full), the commit is
// rolled back out of the log buffer and the transaction stays active so
// the caller can Abort it.
func (tm *TxnManager) Commit(txnID uint64) error {
	tm.mu.Lock()
	prev := tm.prevLSN[txnID]
	tm.mu.Unlock()
	commit := &wal.Record{Type: wal.TypeCommit, TxnID: txnID, PrevLSN: prev}
	if err := tm.Wal.AppendAndFlush(commit); err != nil {
		return err
	}
	endLSN := tm.Wal.Append(&wal.Record{Type: wal.TypeEnd, TxnID: txnID, PrevLSN: commit.LSN})
	_ = endLSN
	if err := tm.Wal.Flush(); err != nil {
		return err
	}
	tm.mu.Lock()
	delete(tm.prevLSN, txnID)
	tm.mu.Unlock()
	return nil
}

// Abort rolls a transaction back, walking its PrevLSN chain backwards and
// writing a CLR for every update it undoes (the same routine ARIES uses
// during the undo pass of recovery).
func (tm *TxnManager) Abort(txnID uint64) error {
	tm.mu.Lock()
	prev := tm.prevLSN[txnID]
	tm.mu.Unlock()
	abortLSN := tm.Wal.Append(&wal.Record{Type: wal.TypeAbort, TxnID: txnID, PrevLSN: prev})
	tm.mu.Lock()
	tm.prevLSN[txnID] = abortLSN
	tm.mu.Unlock()

	for lsn := abortLSN; lsn != 0; {
		rec := tm.Wal.Record(lsn)
		if rec == nil {
			break
		}
		next := rec.PrevLSN
		switch rec.Type {
		case wal.TypeUpdate:
			page := tm.Store.Fetch(rec.PageID)
			copy(page.Data[rec.Offset:], rec.OldData)
			tm.mu.Lock()
			pl := tm.prevLSN[txnID]
			tm.mu.Unlock()
			clrLSN := tm.Wal.Append(&wal.Record{
				Type:        wal.TypeCLR,
				TxnID:       txnID,
				PrevLSN:     pl,
				PageID:      rec.PageID,
				Offset:      rec.Offset,
				OldData:     rec.NewData,
				NewData:     rec.OldData,
				UndoNextLSN: rec.PrevLSN,
			})
			page.LSN = clrLSN
			tm.Store.UnpinDirty(page)
			tm.mu.Lock()
			tm.prevLSN[txnID] = clrLSN
			tm.mu.Unlock()
		case wal.TypeCLR:
			next = rec.UndoNextLSN
		}
		lsn = next
	}
	tm.mu.Lock()
	pl := tm.prevLSN[txnID]
	tm.mu.Unlock()
	tm.Wal.Append(&wal.Record{Type: wal.TypeEnd, TxnID: txnID, PrevLSN: pl})
	tm.mu.Lock()
	delete(tm.prevLSN, txnID)
	tm.mu.Unlock()
	return tm.Wal.Flush()
}

// Checkpoint writes a checkpoint record capturing the current dirty page
// table and transaction table, then forces both the buffer pool and the
// log to disk.
func (tm *TxnManager) Checkpoint() (uint64, error) {
	tm.mu.Lock()
	txns := make(map[uint64]uint64, len(tm.prevLSN))
	for k, v := range tm.prevLSN {
		txns[k] = v
	}
	tm.mu.Unlock()
	lsn := tm.Wal.Append(&wal.Record{
		Type:       wal.TypeCheckpoint,
		DirtyPages: tm.Store.DirtyPages(),
		Txns:       txns,
	})
	tm.Store.FlushAll()
	if err := tm.Wal.Flush(); err != nil {
		return 0, err
	}
	return lsn, nil
}
