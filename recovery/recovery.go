// Package recovery implements the classic ARIES crash-recovery
// protocol: Analysis (rebuild dirty page table + transaction table),
// Redo (repeat history), and Undo (roll back losers writing CLRs).
package recovery

import (
	"container/heap"
	"sort"

	"aries/storage"
	"aries/wal"
)

// TxnEntry is a transaction-table row built during analysis.
type TxnEntry struct {
	LastLSN wal.LSN
	Status  wal.TxnStatus
}

// Stats summarizes a recovery pass for observability and tests.
type Stats struct {
	CheckpointLSN wal.LSN
	RedoStartLSN  wal.LSN
	TxnsAnalyzed  int
	Losers        []uint32
	Redone        int
	Undone        int
	CLRsWritten   int
}

// Engine runs ARIES recovery over a WAL and a disk.
type Engine struct {
	WAL  *wal.WAL
	Disk *storage.Disk
}

func NewEngine(w *wal.WAL, d *storage.Disk) *Engine {
	return &Engine{WAL: w, Disk: d}
}

// Recover executes analysis, redo, and undo in order.
func (e *Engine) Recover() *Stats {
	records := e.WAL.Records()
	st := &Stats{}
	tt, dpt := e.analysis(records, st)
	e.redo(records, dpt, st)
	e.undo(tt, st)
	return st
}

// ------------------------------------------------------------------
// Phase 1: Analysis. Scan forward from the last checkpoint, restoring
// the dirty page table and transaction table at the moment of crash.
// ------------------------------------------------------------------
func (e *Engine) analysis(records []*wal.Record, st *Stats) (map[uint32]*TxnEntry, map[uint32]wal.LSN) {
	tt := make(map[uint32]*TxnEntry)
	dpt := make(map[uint32]wal.LSN)

	start := 0
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].Type == wal.TypeCheckpoint {
			start = i
			st.CheckpointLSN = records[i].LSN
			for p, l := range records[i].DPT {
				dpt[p] = l
			}
			for t, e := range records[i].TT {
				tt[t] = &TxnEntry{LastLSN: e.LastLSN, Status: e.Status}
			}
			break
		}
	}

	for _, rec := range records[start:] {
		switch rec.Type {
		case wal.TypeUpdate, wal.TypeCLR:
			ent, ok := tt[rec.TxnID]
			if !ok {
				ent = &TxnEntry{Status: wal.StatusRunning}
				tt[rec.TxnID] = ent
			}
			ent.LastLSN = rec.LSN
			ent.Status = wal.StatusRunning
			if _, ok := dpt[rec.PageID]; !ok {
				dpt[rec.PageID] = rec.LSN
			}
		case wal.TypeCommit:
			ent, ok := tt[rec.TxnID]
			if !ok {
				ent = &TxnEntry{}
				tt[rec.TxnID] = ent
			}
			ent.LastLSN = rec.LSN
			ent.Status = wal.StatusCommitted
		case wal.TypeAbort:
			ent, ok := tt[rec.TxnID]
			if !ok {
				ent = &TxnEntry{}
				tt[rec.TxnID] = ent
			}
			ent.LastLSN = rec.LSN
			ent.Status = wal.StatusRunning
		case wal.TypeEnd:
			delete(tt, rec.TxnID)
		case wal.TypeCheckpoint:
			// A later checkpoint supersedes: merge conservatively.
			for p, l := range rec.DPT {
				if _, ok := dpt[p]; !ok {
					dpt[p] = l
				}
			}
			for t, e := range rec.TT {
				if _, ok := tt[t]; !ok {
					tt[t] = &TxnEntry{LastLSN: e.LastLSN, Status: e.Status}
				}
			}
		}
	}
	st.TxnsAnalyzed = len(tt)
	return tt, dpt
}

// ------------------------------------------------------------------
// Phase 2: Redo. Repeat history from the oldest recLSN, reapplying
// every logged update/CLR whose effect is not yet on disk.
// ------------------------------------------------------------------
func (e *Engine) redo(records []*wal.Record, dpt map[uint32]wal.LSN, st *Stats) {
	if len(dpt) == 0 {
		return
	}
	redoFrom := wal.LSN(^uint64(0))
	for _, l := range dpt {
		if l < redoFrom {
			redoFrom = l
		}
	}
	st.RedoStartLSN = redoFrom
	for _, rec := range records {
		if rec.LSN < redoFrom {
			continue
		}
		if rec.Type != wal.TypeUpdate && rec.Type != wal.TypeCLR {
			continue
		}
		recLSN, ok := dpt[rec.PageID]
		if !ok || recLSN > rec.LSN {
			continue // page was not dirty when this record was written
		}
		if e.Disk.PageLSN(rec.PageID) >= rec.LSN {
			continue // already persisted on disk
		}
		e.Disk.Write(rec.PageID, rec.After, rec.LSN)
		st.Redone++
	}
}

// ------------------------------------------------------------------
// Phase 3: Undo. Roll back every loser transaction in reverse LSN
// order, writing compensation log records (CLRs) for each undo.
// ------------------------------------------------------------------

type undoItem struct {
	lsn wal.LSN
	txn uint32
}

// undoHeap pops the largest LSN first; ties broken by txn id for
// determinism.
type undoHeap []undoItem

func (h undoHeap) Len() int { return len(h) }
func (h undoHeap) Less(i, j int) bool {
	if h[i].lsn != h[j].lsn {
		return h[i].lsn > h[j].lsn
	}
	return h[i].txn > h[j].txn
}
func (h undoHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *undoHeap) Push(x interface{}) { *h = append(*h, x.(undoItem)) }
func (h *undoHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

func (e *Engine) undo(tt map[uint32]*TxnEntry, st *Stats) {
	h := &undoHeap{}
	lastWritten := make(map[uint32]wal.LSN)
	for txn, ent := range tt {
		if ent.Status == wal.StatusCommitted {
			continue
		}
		st.Losers = append(st.Losers, txn)
		heap.Push(h, undoItem{lsn: ent.LastLSN, txn: txn})
		lastWritten[txn] = ent.LastLSN
	}
	sort.Slice(st.Losers, func(i, j int) bool { return st.Losers[i] < st.Losers[j] })

	for h.Len() > 0 {
		item := heap.Pop(h).(undoItem)
		rec := e.WAL.Get(item.lsn)
		if rec == nil {
			continue
		}
		switch rec.Type {
		case wal.TypeUpdate:
			clr := &wal.Record{
				Type:        wal.TypeCLR,
				TxnID:       rec.TxnID,
				PageID:      rec.PageID,
				PrevLSN:     lastWritten[rec.TxnID],
				UndoNextLSN: rec.PrevLSN,
				Before:      rec.After,
				After:       rec.Before,
			}
			clrLSN := e.WAL.Append(clr)
			st.CLRsWritten++
			st.Undone++
			lastWritten[rec.TxnID] = clrLSN
			if e.Disk.PageLSN(rec.PageID) < clrLSN {
				e.Disk.Write(rec.PageID, rec.Before, clrLSN)
			}
			e.followChain(h, rec.TxnID, rec.PrevLSN, lastWritten)
		case wal.TypeCLR:
			// Already compensated (e.g. crash during a previous undo):
			// skip to undoNext, never undo a CLR.
			e.followChain(h, rec.TxnID, rec.UndoNextLSN, lastWritten)
		case wal.TypeAbort:
			e.followChain(h, rec.TxnID, rec.PrevLSN, lastWritten)
		default:
			e.followChain(h, rec.TxnID, rec.PrevLSN, lastWritten)
		}
	}
	e.WAL.Sync()
}

// followChain advances a transaction's undo chain, writing END when
// the transaction is fully rolled back.
func (e *Engine) followChain(h *undoHeap, txn uint32, next wal.LSN, lastWritten map[uint32]wal.LSN) {
	if next != wal.NilLSN {
		heap.Push(h, undoItem{lsn: next, txn: txn})
		return
	}
	end := &wal.Record{Type: wal.TypeEnd, TxnID: txn, PrevLSN: lastWritten[txn]}
	e.WAL.Append(end)
	lastWritten[txn] = end.LSN
}
