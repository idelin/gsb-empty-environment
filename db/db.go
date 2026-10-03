// Package db implements the runtime database system that produces the
// WAL: transactions, buffer-pool updates, checkpoints and crashes.
package db

import (
	"fmt"
	"sort"
	"sync"

	"aries/storage"
	"aries/wal"
)

// Txn is a live transaction known to the runtime.
type Txn struct {
	ID      uint64
	Status  wal.TxnStatus
	LastLSN uint64
}

// DB ties together the WAL, buffer pool and transaction table.
// All methods are safe for concurrent use.
type DB struct {
	mu       sync.Mutex
	WAL      *wal.WAL
	Pool     *storage.BufferPool
	txns     map[uint64]*Txn
	nextTxn  uint64
	lsnIndex map[uint64]*wal.Record // records appended since (re)start
}

// New creates a DB. The WAL should be positioned past any existing log
// (use wal.WAL.Resume after a restart).
func New(w *wal.WAL, pool *storage.BufferPool) *DB {
	return NewAt(w, pool, 1)
}

// NewAt creates a DB whose transaction IDs start at nextTxn. After a
// restart, pass a value greater than any txn ID in the recovered log
// (see recovery.Stats.MaxTxnID) so IDs never collide across crashes.
func NewAt(w *wal.WAL, pool *storage.BufferPool, nextTxn uint64) *DB {
	return &DB{
		WAL:      w,
		Pool:     pool,
		txns:     make(map[uint64]*Txn),
		nextTxn:  nextTxn,
		lsnIndex: make(map[uint64]*wal.Record),
	}
}

func (d *DB) append(rec *wal.Record) uint64 {
	lsn := d.WAL.Append(rec)
	d.lsnIndex[lsn] = rec
	return lsn
}

// Begin starts a transaction and returns its ID.
func (d *DB) Begin() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := d.nextTxn
	d.nextTxn++
	txn := &Txn{ID: id, Status: wal.TxnRunning}
	txn.LastLSN = d.append(&wal.Record{Type: wal.RecBegin, TxnID: id})
	d.txns[id] = txn
	return id
}

// Update sets page pageID to newVal on behalf of txn txnID, logging an
// UPDATE record with the before image.
func (d *DB) Update(txnID, pageID uint64, newVal int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	txn, ok := d.txns[txnID]
	if !ok {
		return fmt.Errorf("db: unknown txn %d", txnID)
	}
	if txn.Status != wal.TxnRunning {
		return fmt.Errorf("db: txn %d not running (%s)", txnID, txn.Status)
	}
	pg := d.Pool.Fetch(pageID)
	lsn := d.append(&wal.Record{
		Type:    wal.RecUpdate,
		TxnID:   txnID,
		PrevLSN: txn.LastLSN,
		PageID:  pageID,
		Before:  pg.Value,
		After:   newVal,
	})
	pg.Value = newVal
	pg.PageLSN = lsn
	if !pg.Dirty {
		pg.Dirty = true
		pg.RecLSN = lsn
	}
	txn.LastLSN = lsn
	return nil
}

// Commit logs COMMIT, forces the log (write-ahead rule), logs END and
// forgets the transaction.
func (d *DB) Commit(txnID uint64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	txn, ok := d.txns[txnID]
	if !ok {
		return fmt.Errorf("db: unknown txn %d", txnID)
	}
	txn.Status = wal.TxnCommitted
	commitLSN := d.append(&wal.Record{Type: wal.RecCommit, TxnID: txnID, PrevLSN: txn.LastLSN})
	txn.LastLSN = commitLSN
	if err := d.WAL.Flush(); err != nil {
		return err
	}
	endLSN := d.append(&wal.Record{Type: wal.RecEnd, TxnID: txnID, PrevLSN: txn.LastLSN})
	delete(d.txns, txnID)
	d.prune(endLSN)
	return nil
}

// Abort rolls a running transaction back at runtime, writing CLRs, then
// logs END.
func (d *DB) Abort(txnID uint64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	txn, ok := d.txns[txnID]
	if !ok {
		return fmt.Errorf("db: unknown txn %d", txnID)
	}
	txn.Status = wal.TxnAborting
	d.append(&wal.Record{Type: wal.RecAbort, TxnID: txnID, PrevLSN: txn.LastLSN})
	lsn := txn.LastLSN
	for lsn != 0 {
		rec, ok := d.lsnIndex[lsn]
		if !ok {
			return fmt.Errorf("db: missing record at LSN %d during abort", lsn)
		}
		switch rec.Type {
		case wal.RecCLR:
			lsn = rec.UndoNext
		case wal.RecUpdate:
			clrLSN := d.append(&wal.Record{
				Type:     wal.RecCLR,
				TxnID:    txnID,
				PrevLSN:  txn.LastLSN,
				PageID:   rec.PageID,
				UndoNext: rec.PrevLSN,
				Before:   rec.After,
				After:    rec.Before,
			})
			pg := d.Pool.Fetch(rec.PageID)
			pg.Value = rec.Before
			pg.PageLSN = clrLSN
			if !pg.Dirty {
				pg.Dirty = true
				pg.RecLSN = clrLSN
			}
			txn.LastLSN = clrLSN
			lsn = rec.PrevLSN
		default: // BEGIN
			lsn = 0
		}
	}
	d.append(&wal.Record{Type: wal.RecEnd, TxnID: txnID, PrevLSN: txn.LastLSN})
	delete(d.txns, txnID)
	d.prune(txn.LastLSN)
	return nil
}

// prune drops the finished transaction's records from the in-memory
// index; only active transactions need runtime-undo lookup.
func (d *DB) prune(lastLSN uint64) {
	for lsn := lastLSN; lsn != 0; {
		rec, ok := d.lsnIndex[lsn]
		if !ok {
			return
		}
		delete(d.lsnIndex, lsn)
		lsn = rec.PrevLSN
	}
}

// Checkpoint writes a checkpoint record capturing the transaction table
// and dirty page table.
func (d *DB) Checkpoint() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	rec := &wal.Record{Type: wal.RecCheckpoint}
	ids := make([]uint64, 0, len(d.txns))
	for id := range d.txns {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		txn := d.txns[id]
		rec.Txns = append(rec.Txns, wal.TxnEntry{TxnID: id, Status: txn.Status, LastLSN: txn.LastLSN})
	}
	for _, de := range d.Pool.DirtyPages() {
		rec.Dirty = append(rec.Dirty, wal.DirtyEntry{PageID: de.PageID, RecLSN: de.RecLSN})
	}
	return d.append(rec)
}

// FlushPage forces the log then writes the page to disk (WAL rule: log
// up to PageLSN must be durable before the page is).
func (d *DB) FlushPage(pageID uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	_ = d.WAL.Flush()
	d.Pool.Flush(pageID)
}

// FlushLog forces the log buffer to stable storage.
func (d *DB) FlushLog() {
	d.mu.Lock()
	defer d.mu.Unlock()
	_ = d.WAL.Flush()
}

// Crash simulates a power failure: unflushed log records and all
// buffered (unflushed) pages are lost, the transaction table is gone.
func (d *DB) Crash() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.WAL.LoseVolatile()
	d.Pool.Reset()
	d.txns = make(map[uint64]*Txn)
	d.lsnIndex = make(map[uint64]*wal.Record)
}

// ActiveTxns reports the live transaction table (empty after a crash).
func (d *DB) ActiveTxns() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.txns)
}
