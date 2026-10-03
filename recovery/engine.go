package recovery

import (
	"container/heap"
	"fmt"
	"math"
	"sort"

	"aries/storage"
	"aries/wal"
)

// Engine performs ARIES restart recovery over a log, buffer pool and
// disk store.
type Engine struct {
	Log  *wal.LogManager
	Pool *storage.BufferPool
	Disk *storage.DiskStore
}

// Report summarizes a restart pass.
type Report struct {
	Analysis *Analysis
	Redone   int      // number of UPDATE/CLR records re-applied
	CLRs     []uint64 // LSNs of compensation records written during undo
	Ends     []uint64 // LSNs of END records written during undo
	Losers   []int    // transactions rolled back
	Winners  []int    // transactions seen committed
}

// Restart executes the three ARIES phases: analysis, redo, undo, then
// forces log and dirty pages to durable storage.
func (e *Engine) Restart(checkpointLSN uint64) (*Report, error) {
	analysis, err := Analyze(e.Log, checkpointLSN)
	if err != nil {
		return nil, err
	}
	redone, err := Redo(e.Log, e.Pool, analysis)
	if err != nil {
		return nil, err
	}
	clrs, ends, err := Undo(e.Log, e.Pool, analysis)
	if err != nil {
		return nil, err
	}
	e.Log.Flush(e.Log.LastLSN())
	if err := e.Pool.FlushAll(); err != nil {
		return nil, err
	}
	rep := &Report{
		Analysis: analysis,
		Redone:   redone,
		CLRs:     clrs,
		Ends:     ends,
		Losers:   analysis.Losers(),
	}
	for txn := range analysis.Committed {
		rep.Winners = append(rep.Winners, txn)
	}
	sort.Ints(rep.Losers)
	sort.Ints(rep.Winners)
	return rep, nil
}

// Redo repeats history: starting from the smallest recLSN in the DPT,
// every UPDATE/CLR affecting a dirty page is re-applied unless the
// page on disk already reflects it (pageLSN >= record LSN).
func Redo(log *wal.LogManager, pool *storage.BufferPool, a *Analysis) (int, error) {
	if len(a.DPT) == 0 {
		return 0, nil
	}
	start := uint64(math.MaxUint64)
	for _, recLSN := range a.DPT {
		if recLSN < start {
			start = recLSN
		}
	}
	redone := 0
	err := log.ScanFrom(start, func(rec *wal.Record) error {
		if rec.Type != wal.TypeUpdate && rec.Type != wal.TypeCLR {
			return nil
		}
		recLSN, dirty := a.DPT[rec.PageID]
		if !dirty || recLSN > rec.LSN {
			return nil
		}
		_, pageLSN, err := pool.Fetch(rec.PageID)
		if err != nil {
			return err
		}
		if pageLSN >= rec.LSN {
			return nil // already on disk: nothing to repeat
		}
		if err := pool.Apply(rec.PageID, rec.After, rec.LSN); err != nil {
			return err
		}
		redone++
		return nil
	})
	if err != nil {
		return 0, err
	}
	return redone, nil
}

// Undo rolls back every loser transaction in reverse LSN order,
// writing a compensation log record (CLR) per undone update and an
// END record per finished transaction. Already-written CLRs are
// followed via undoNext, so undo is idempotent across crashes.
func Undo(log *wal.LogManager, pool *storage.BufferPool, a *Analysis) (clrs, ends []uint64, err error) {
	todo := &lsnHeap{}
	for _, e := range a.ATT {
		if e.Status == StatusCommitted {
			continue
		}
		heap.Push(todo, e.LastLSN)
	}
	heap.Init(todo)
	for todo.Len() > 0 {
		lsn := heap.Pop(todo).(uint64)
		rec, err := log.Read(lsn)
		if err != nil {
			return nil, nil, fmt.Errorf("undo: %w", err)
		}
		switch rec.Type {
		case wal.TypeUpdate:
			clr := &wal.Record{
				Type:     wal.TypeCLR,
				TxnID:    rec.TxnID,
				PrevLSN:  rec.LSN,
				PageID:   rec.PageID,
				UndoNext: rec.PrevLSN,
				Before:   rec.After,
				After:    rec.Before,
			}
			clrLSN := log.Append(clr)
			if err := pool.Apply(rec.PageID, rec.Before, clrLSN); err != nil {
				return nil, nil, err
			}
			clrs = append(clrs, clrLSN)
			if rec.PrevLSN != 0 {
				heap.Push(todo, rec.PrevLSN)
			} else {
				ends = append(ends, appendEnd(log, rec.TxnID, clrLSN))
			}
		case wal.TypeCLR:
			// Already compensated before the crash: skip to undoNext.
			if rec.UndoNext != 0 {
				heap.Push(todo, rec.UndoNext)
			} else {
				ends = append(ends, appendEnd(log, rec.TxnID, rec.LSN))
			}
		case wal.TypeAbort, wal.TypeCommit:
			if rec.PrevLSN != 0 {
				heap.Push(todo, rec.PrevLSN)
			} else {
				ends = append(ends, appendEnd(log, rec.TxnID, rec.LSN))
			}
		case wal.TypeBegin:
			ends = append(ends, appendEnd(log, rec.TxnID, rec.LSN))
		default:
			return nil, nil, fmt.Errorf("undo: unexpected %s record at LSN %d", rec.Type, rec.LSN)
		}
	}
	return clrs, ends, nil
}

func appendEnd(log *wal.LogManager, txnID int, prevLSN uint64) uint64 {
	return log.Append(&wal.Record{Type: wal.TypeEnd, TxnID: txnID, PrevLSN: prevLSN})
}

// lsnHeap is a max-heap of LSNs: undo always processes the largest
// outstanding LSN first.
type lsnHeap []uint64

func (h lsnHeap) Len() int            { return len(h) }
func (h lsnHeap) Less(i, j int) bool  { return h[i] > h[j] }
func (h lsnHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *lsnHeap) Push(x interface{}) { *h = append(*h, x.(uint64)) }
func (h *lsnHeap) Pop() interface{} {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}
