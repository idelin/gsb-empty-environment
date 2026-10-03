package aries

import (
	"container/heap"
	"fmt"
	"sort"
)

// RecoveryReport summarizes one ARIES recovery run.
type RecoveryReport struct {
	CheckpointLSN uint64            // checkpoint the analysis phase started from (0 = none)
	RedoStartLSN  uint64            // smallest recLSN in the analyzed DPT
	DPT           map[uint64]uint64 // dirty page table after analysis
	Losers        []uint64          // transactions rolled back by undo
	Winners       []uint64          // committing transactions completed with END
	Redone        int               // update/CLR records re-applied by redo
	Undone        int               // update records rolled back by undo
	CLRsWritten   int               // compensation log records written
}

// lsnHeap is a max-heap of LSNs driving the undo phase.
type lsnHeap []uint64

func (h lsnHeap) Len() int            { return len(h) }
func (h lsnHeap) Less(i, j int) bool  { return h[i] > h[j] }
func (h lsnHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *lsnHeap) Push(x interface{}) { *h = append(*h, x.(uint64)) }
func (h *lsnHeap) Pop() interface{} {
	old := *h
	v := old[len(old)-1]
	*h = old[:len(old)-1]
	return v
}

// Recover performs the three ARIES phases against the given WAL and page
// store: analysis (rebuild DPT/TT from the last checkpoint), redo (repeat
// history from the smallest recLSN) and undo (roll back loser transactions
// writing CLRs). All redone/kept pages are flushed before returning.
func Recover(walPath, pagePath string) (*RecoveryReport, error) {
	wal, err := OpenWAL(walPath)
	if err != nil {
		return nil, err
	}
	defer wal.Close()
	store, err := OpenPageStore(pagePath)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	records, err := wal.Records()
	if err != nil {
		return nil, err
	}
	byLSN := make(map[uint64]*LogRecord, len(records))
	for _, r := range records {
		byLSN[r.LSN] = r
	}

	report := &RecoveryReport{DPT: map[uint64]uint64{}}

	// ---- Phase 1: Analysis ------------------------------------------------
	ckptLSN := uint64(0)
	var ckpt *CheckpointData
	for _, r := range records {
		if r.Type == LogCheckpoint {
			ckptLSN, ckpt = r.LSN, r.Checkpoint
		}
	}
	report.CheckpointLSN = ckptLSN
	dpt := map[uint64]uint64{}
	tt := map[uint64]*TxnEntry{}
	if ckpt != nil {
		for pid, recLSN := range ckpt.DPT {
			dpt[pid] = recLSN
		}
		for tid, e := range ckpt.TT {
			tt[tid] = &TxnEntry{ID: tid, LastLSN: e.LastLSN, Status: e.Status}
		}
	}
	txnEntry := func(id uint64) *TxnEntry {
		e := tt[id]
		if e == nil {
			e = &TxnEntry{ID: id}
			tt[id] = e
		}
		return e
	}
	for _, r := range records {
		if r.LSN < ckptLSN || r.Type == LogCheckpoint {
			continue
		}
		switch r.Type {
		case LogUpdate, LogCLR:
			e := txnEntry(r.TxnID)
			e.LastLSN = r.LSN
			e.Status = StatusRunning
			if _, ok := dpt[r.PageID]; !ok {
				dpt[r.PageID] = r.LSN
			}
		case LogCommit:
			e := txnEntry(r.TxnID)
			e.LastLSN = r.LSN
			e.Status = StatusCommitting
		case LogAbort:
			e := txnEntry(r.TxnID)
			e.LastLSN = r.LSN
			e.Status = StatusAborting
		case LogEnd:
			delete(tt, r.TxnID)
		}
	}
	report.DPT = dpt

	// ---- Phase 2: Redo (repeat history) -----------------------------------
	pages := map[uint64]*SparsePage{}
	getPage := func(id uint64) (*SparsePage, error) {
		p, ok := pages[id]
		if !ok {
			var err error
			p, err = store.ReadPage(id)
			if err != nil {
				return nil, err
			}
			pages[id] = p
		}
		return p, nil
	}
	redoStart := ckptLSN
	first := true
	for _, recLSN := range dpt {
		if first || recLSN < redoStart {
			redoStart = recLSN
			first = false
		}
	}
	report.RedoStartLSN = redoStart
	for _, r := range records {
		if r.LSN < redoStart || (r.Type != LogUpdate && r.Type != LogCLR) {
			continue
		}
		recLSN, dirty := dpt[r.PageID]
		if !dirty || r.LSN < recLSN {
			continue
		}
		page, err := getPage(r.PageID)
		if err != nil {
			return nil, err
		}
		if page.LSN < r.LSN {
			page.Set(r.Offset, r.NewValue)
			page.LSN = r.LSN
			report.Redone++
		}
	}

	// ---- Phase 3: Undo (reverse scan, CLRs) -------------------------------
	toUndo := &lsnHeap{}
	for _, e := range tt {
		if e.Status == StatusCommitting {
			// Committing txn whose END was lost in the crash: a winner.
			wal.Append(&LogRecord{Type: LogEnd, TxnID: e.ID, PrevLSN: e.LastLSN})
			report.Winners = append(report.Winners, e.ID)
			continue
		}
		report.Losers = append(report.Losers, e.ID)
		if e.LastLSN != 0 {
			heap.Push(toUndo, e.LastLSN)
		} else {
			wal.Append(&LogRecord{Type: LogEnd, TxnID: e.ID})
		}
	}
	sort.Slice(report.Losers, func(i, j int) bool { return report.Losers[i] < report.Losers[j] })
	sort.Slice(report.Winners, func(i, j int) bool { return report.Winners[i] < report.Winners[j] })

	for toUndo.Len() > 0 {
		lsn := heap.Pop(toUndo).(uint64)
		rec := byLSN[lsn]
		if rec == nil {
			return nil, fmt.Errorf("recovery: dangling LSN %d in undo chain", lsn)
		}
		switch rec.Type {
		case LogUpdate:
			page, err := getPage(rec.PageID)
			if err != nil {
				return nil, err
			}
			e := tt[rec.TxnID]
			clrLSN := wal.Append(&LogRecord{
				Type: LogCLR, TxnID: rec.TxnID, PrevLSN: e.LastLSN,
				PageID: rec.PageID, Offset: rec.Offset,
				OldValue: rec.NewValue, NewValue: rec.OldValue,
				UndoNext: rec.PrevLSN,
			})
			e.LastLSN = clrLSN
			page.Set(rec.Offset, rec.OldValue)
			page.LSN = clrLSN
			report.CLRsWritten++
			report.Undone++
			if rec.PrevLSN != 0 {
				heap.Push(toUndo, rec.PrevLSN)
			} else {
				wal.Append(&LogRecord{Type: LogEnd, TxnID: rec.TxnID, PrevLSN: clrLSN})
			}
		case LogCLR:
			if rec.UndoNext != 0 {
				heap.Push(toUndo, rec.UndoNext)
			} else {
				wal.Append(&LogRecord{Type: LogEnd, TxnID: rec.TxnID, PrevLSN: rec.LSN})
			}
		case LogAbort:
			if rec.PrevLSN != 0 {
				heap.Push(toUndo, rec.PrevLSN)
			} else {
				wal.Append(&LogRecord{Type: LogEnd, TxnID: rec.TxnID, PrevLSN: rec.LSN})
			}
		default:
			return nil, fmt.Errorf("recovery: unexpected %s record in undo chain", rec.Type)
		}
	}

	// ---- Flush all pages touched by recovery ------------------------------
	for id, page := range pages {
		if err := store.WritePage(id, page); err != nil {
			return nil, err
		}
	}
	return report, nil
}
