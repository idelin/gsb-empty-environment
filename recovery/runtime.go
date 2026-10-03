package recovery

import (
	"fmt"
	"sort"
	"sync"

	"aries/store"
	"aries/wal"
)

// Runtime is a minimal transaction runtime that generates WAL
// traffic: it appends log records for transaction operations and
// applies them to the buffer pool, tracking the transaction table
// and dirty page table so checkpoints can be written. It is safe
// for concurrent use.
type Runtime struct {
	WAL   *wal.WAL
	Store *store.Store

	mu      sync.Mutex
	lastLSN map[uint64]uint64 // txnID -> last log record (running txns)
	dpt     map[uint64]uint64 // pageID -> recLSN
}

// NewRuntime creates a Runtime over the given WAL and store.
func NewRuntime(w *wal.WAL, s *store.Store) *Runtime {
	return &Runtime{
		WAL:     w,
		Store:   s,
		lastLSN: make(map[uint64]uint64),
		dpt:     make(map[uint64]uint64),
	}
}

// Begin registers a transaction.
func (rt *Runtime) Begin(txnID uint64) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.lastLSN[txnID] = 0
}

// Update logs and applies a page update on behalf of a transaction.
func (rt *Runtime) Update(txnID, pageID uint64, off int, before, after []byte) (uint64, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rec := &wal.Record{
		Type:    wal.RecUpdate,
		TxnID:   txnID,
		PrevLSN: rt.lastLSN[txnID],
		PageID:  pageID,
		Offset:  uint32(off),
		Before:  before,
		After:   after,
	}
	lsn, err := rt.WAL.Append(rec)
	if err != nil {
		return 0, err
	}
	rt.lastLSN[txnID] = lsn
	if err := rt.Store.Write(pageID, off, after, lsn); err != nil {
		return 0, err
	}
	if _, ok := rt.dpt[pageID]; !ok {
		rt.dpt[pageID] = lsn
	}
	return lsn, nil
}

// Commit appends a COMMIT record and forces the log to disk.
func (rt *Runtime) Commit(txnID uint64) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if _, err := rt.WAL.Append(&wal.Record{
		Type:    wal.RecCommit,
		TxnID:   txnID,
		PrevLSN: rt.lastLSN[txnID],
	}); err != nil {
		return err
	}
	return rt.WAL.Flush()
}

// Abort appends an ABORT record.
func (rt *Runtime) Abort(txnID uint64) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	_, err := rt.WAL.Append(&wal.Record{
		Type:    wal.RecAbort,
		TxnID:   txnID,
		PrevLSN: rt.lastLSN[txnID],
	})
	return err
}

// End appends an END record and drops the transaction from the
// runtime transaction table.
func (rt *Runtime) End(txnID uint64) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if _, err := rt.WAL.Append(&wal.Record{
		Type:    wal.RecEnd,
		TxnID:   txnID,
		PrevLSN: rt.lastLSN[txnID],
	}); err != nil {
		return err
	}
	delete(rt.lastLSN, txnID)
	return nil
}

// FlushPage flushes a page to disk and removes it from the runtime
// dirty page table.
func (rt *Runtime) FlushPage(pageID uint64) error {
	if err := rt.Store.FlushPage(pageID); err != nil {
		return err
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	delete(rt.dpt, pageID)
	return nil
}

// Checkpoint appends a fuzzy checkpoint record carrying the current
// transaction table and dirty page table snapshots.
func (rt *Runtime) Checkpoint() (uint64, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rec := &wal.Record{Type: wal.RecCheckpoint}
	txns := make([]uint64, 0, len(rt.lastLSN))
	for id := range rt.lastLSN {
		txns = append(txns, id)
	}
	sort.Slice(txns, func(i, j int) bool { return txns[i] < txns[j] })
	for _, id := range txns {
		rec.Txns = append(rec.Txns, wal.TxnEntry{
			TxnID:   id,
			LastLSN: rt.lastLSN[id],
			Status:  wal.StatusRunning,
		})
	}
	pages := make([]uint64, 0, len(rt.dpt))
	for id := range rt.dpt {
		pages = append(pages, id)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i] < pages[j] })
	for _, id := range pages {
		rec.Pages = append(rec.Pages, wal.PageEntry{PageID: id, RecLSN: rt.dpt[id]})
	}
	lsn, err := rt.WAL.Append(rec)
	if err != nil {
		return 0, err
	}
	return lsn, rt.WAL.Flush()
}

// ActiveTxns reports the transactions currently in the runtime table.
func (rt *Runtime) ActiveTxns() []uint64 {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := make([]uint64, 0, len(rt.lastLSN))
	for id := range rt.lastLSN {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// String renders the runtime tables (debugging aid).
func (rt *Runtime) String() string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return fmt.Sprintf("txns=%v dpt=%v", rt.lastLSN, rt.dpt)
}
