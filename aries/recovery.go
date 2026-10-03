package aries

import (
	"container/heap"
	"fmt"
	"sort"
)

// RecoveryStats summarizes one recovery pass; handy for observability and
// for tests asserting exactly what the engine did.
type RecoveryStats struct {
	AnalyzedRecords int
	CheckpointLSN   uint64   // END_CHKPT record analysis started from (0 = none)
	RedoFromLSN     uint64   // smallest recLSN in the DPT after analysis
	RedoneOps       int      // update/CLR records re-applied
	SkippedOps      int      // records skipped by DPT/pageLSN checks
	UndoneOps       int      // CLRs written during undo
	Winners         []uint64 // committed txns (sorted)
	Losers          []uint64 // rolled-back txns (sorted)
}

// Recover runs the full ARIES protocol against wal and pool:
//
//	Analysis: rebuild the transaction table and dirty page table, starting
//	          from the most recent complete checkpoint if one exists.
//	Redo:     repeat history from the smallest recLSN, re-applying every
//	          logged update whose effects are not already on disk.
//	Undo:     roll back every loser transaction in reverse LSN order,
//	          logging a CLR for each undone update.
//
// On success the database is in the transactionally consistent state in
// which every committed transaction's effects are durable and every
// uncommitted transaction's effects are gone.
func Recover(wal *WAL, pool *BufferPool) (*RecoveryStats, error) {
	stats := &RecoveryStats{}
	if err := wal.Flush(); err != nil {
		return nil, err
	}

	txnTable, dpt, err := analysis(wal, stats)
	if err != nil {
		return nil, fmt.Errorf("analysis: %v", err)
	}
	if err := redo(wal, pool, dpt, stats); err != nil {
		return nil, fmt.Errorf("redo: %v", err)
	}
	if err := undo(wal, pool, txnTable, stats); err != nil {
		return nil, fmt.Errorf("undo: %v", err)
	}

	if err := pool.FlushAll(); err != nil {
		return nil, err
	}
	if err := wal.Flush(); err != nil {
		return nil, err
	}
	sort.Slice(stats.Winners, func(i, j int) bool { return stats.Winners[i] < stats.Winners[j] })
	sort.Slice(stats.Losers, func(i, j int) bool { return stats.Losers[i] < stats.Losers[j] })
	return stats, nil
}

// analysis rebuilds the transaction table and dirty page table.
func analysis(wal *WAL, stats *RecoveryStats) (map[uint64]TxnTableEntry, map[uint32]uint64, error) {
	// Pass 1: locate the most recent *complete* checkpoint.
	var lastChk *LogRecord
	if err := wal.Scan(func(r *LogRecord) bool {
		if r.Type == LogEndCheckpoint {
			cp := *r
			lastChk = &cp
		}
		return true
	}); err != nil {
		return nil, nil, err
	}

	txnTable := make(map[uint64]TxnTableEntry)
	dpt := make(map[uint32]uint64)
	startLSN := uint64(1)
	if lastChk != nil {
		for id, e := range lastChk.TxnTable {
			txnTable[id] = e
			if e.Status == StatusCommitted {
				stats.Winners = append(stats.Winners, id)
			}
		}
		for id, recLSN := range lastChk.DirtyPageTab {
			dpt[id] = recLSN
		}
		startLSN = lastChk.LSN
		stats.CheckpointLSN = lastChk.LSN
	}

	// Pass 2: scan forward from the checkpoint, maintaining both tables.
	err := wal.ScanFrom(startLSN, func(r *LogRecord) bool {
		stats.AnalyzedRecords++
		switch r.Type {
		case LogUpdate, LogCLR:
			e := txnTable[r.TxnID]
			e.LastLSN = r.LSN
			txnTable[r.TxnID] = e
			if _, ok := dpt[r.PageID]; !ok {
				dpt[r.PageID] = r.LSN
			}
		case LogCommit:
			e := txnTable[r.TxnID]
			e.LastLSN = r.LSN
			e.Status = StatusCommitted
			txnTable[r.TxnID] = e
			stats.Winners = append(stats.Winners, r.TxnID)
		case LogAbort:
			e := txnTable[r.TxnID]
			e.LastLSN = r.LSN
			e.Status = StatusAborting
			txnTable[r.TxnID] = e
		case LogEnd:
			delete(txnTable, r.TxnID)
		}
		return true
	})
	if err != nil {
		return nil, nil, err
	}

	// Losers are the transactions still open at the end of the log.
	for id, e := range txnTable {
		if e.Status != StatusCommitted {
			stats.Losers = append(stats.Losers, id)
		}
	}
	return txnTable, dpt, nil
}

// redo repeats history: every update/CLR whose effects may be missing
// from disk is re-applied. The pageLSN check makes redo idempotent.
func redo(wal *WAL, pool *BufferPool, dpt map[uint32]uint64, stats *RecoveryStats) error {
	if len(dpt) == 0 {
		return nil
	}
	redoFrom := uint64(^uint64(0)) // max uint64
	for _, recLSN := range dpt {
		if recLSN < redoFrom {
			redoFrom = recLSN
		}
	}
	stats.RedoFromLSN = redoFrom

	return wal.ScanFrom(redoFrom, func(r *LogRecord) bool {
		if r.Type != LogUpdate && r.Type != LogCLR {
			return true
		}
		recLSN, dirty := dpt[r.PageID]
		if !dirty {
			// Page was clean at crash time; the update is on disk already.
			stats.SkippedOps++
			return true
		}
		if r.LSN < recLSN {
			// Older than the update that first dirtied the page: on disk.
			stats.SkippedOps++
			return true
		}
		pageLSN, err := pool.PageLSN(r.PageID)
		if err != nil {
			return false
		}
		if pageLSN >= r.LSN {
			// Page already reflects this (or a later) record.
			stats.SkippedOps++
			return true
		}
		if err := pool.ApplyUpdate(r.PageID, r.LSN, int(r.Offset), r.NewData); err != nil {
			return false
		}
		stats.RedoneOps++
		return true
	})
}

// undo rolls back all loser transactions in global reverse LSN order,
// writing a compensation log record for every undone update.
func undo(wal *WAL, pool *BufferPool, txnTable map[uint64]TxnTableEntry, stats *RecoveryStats) error {
	// toUndo tracks, per loser, the LSN of the next record to process.
	toUndo := make(map[uint64]uint64)
	pq := &lsnHeap{}
	for id, e := range txnTable {
		if e.Status == StatusCommitted {
			continue
		}
		if e.LastLSN != 0 {
			toUndo[id] = e.LastLSN
			heap.Push(pq, lsnItem{lsn: e.LastLSN, txn: id})
		}
	}

	for pq.Len() > 0 {
		item := heap.Pop(pq).(lsnItem)
		if toUndo[item.txn] != item.lsn {
			continue // stale heap entry
		}
		rec := wal.Record(item.lsn)
		if rec == nil {
			return fmt.Errorf("undo: missing log record LSN %d (txn %d)", item.lsn, item.txn)
		}
		var next uint64
		switch rec.Type {
		case LogUpdate:
			clr := &LogRecord{
				PrevLSN:     item.lsn,
				TxnID:       rec.TxnID,
				Type:        LogCLR,
				PageID:      rec.PageID,
				Offset:      rec.Offset,
				OldData:     append([]byte(nil), rec.NewData...),
				NewData:     append([]byte(nil), rec.OldData...),
				UndoNextLSN: rec.PrevLSN,
			}
			clrLSN, err := wal.Append(clr)
			if err != nil {
				return err
			}
			if err := pool.ApplyUpdate(rec.PageID, clrLSN, int(rec.Offset), rec.OldData); err != nil {
				return err
			}
			stats.UndoneOps++
			next = rec.PrevLSN
		case LogCLR:
			// Never undo an undo: follow undoNext to skip compensated ops.
			next = rec.UndoNextLSN
		default:
			// ABORT and other non-data records: walk the prev chain.
			next = rec.PrevLSN
		}
		if next != 0 {
			toUndo[item.txn] = next
			heap.Push(pq, lsnItem{lsn: next, txn: item.txn})
		} else {
			delete(toUndo, item.txn)
			if _, err := wal.Append(&LogRecord{
				PrevLSN: item.lsn,
				TxnID:   item.txn,
				Type:    LogEnd,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// lsnItem is a (candidate undo record) heap entry.
type lsnItem struct {
	lsn uint64
	txn uint64
}

// lsnHeap is a max-heap of log records by LSN, so undo always processes
// the globally newest not-yet-processed record first.
type lsnHeap []lsnItem

func (h lsnHeap) Len() int            { return len(h) }
func (h lsnHeap) Less(i, j int) bool  { return h[i].lsn < h[j].lsn }
func (h lsnHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *lsnHeap) Push(x interface{}) { *h = append(*h, x.(lsnItem)) }
func (h *lsnHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
