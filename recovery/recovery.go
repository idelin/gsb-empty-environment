// Package recovery implements the ARIES crash-recovery protocol:
// Analysis, Redo (repeating history) and Undo (with CLRs).
package recovery

import (
	"container/heap"
	"errors"
	"fmt"
	"sort"

	"aries/storage"
	"aries/wal"
)

// ErrCrashInjected is returned when an injected fault aborts recovery
// mid-Undo (used by the fault-reproduction tooling).
var ErrCrashInjected = errors.New("recovery: injected crash during undo")

// Options carries fault-injection hooks. OnCLR, if non-nil, is invoked
// after each CLR written during Undo; returning true simulates a crash
// (volatile log lost, buffer pool dropped) and aborts recovery with
// ErrCrashInjected.
type Options struct {
	OnCLR func(w *wal.WAL, pool *storage.BufferPool, clrsWritten int) (crash bool)
}

// Stats summarizes one recovery pass.
type Stats struct {
	RecordsScanned int
	CheckpointLSN  uint64 // 0 if no checkpoint was found
	Redone         int    // UPDATE/CLR records re-applied
	RedoSkipped    int    // records skipped (page already current / not dirty)
	CLRsWritten    int
	MaxTxnID       uint64   // highest txn ID seen in the log
	Losers         []uint64 // txn IDs rolled back
	Winners        []uint64 // txn IDs committed before the crash
}

// TxnTableEntry is the analysis-phase transaction table row.
type TxnTableEntry struct {
	Status  wal.TxnStatus
	LastLSN uint64
}

// Result exposes the analysis-phase tables for inspection/testing.
type Result struct {
	Stats     *Stats
	TxnTable  map[uint64]TxnTableEntry
	DirtyPage map[uint64]uint64 // pageID -> recLSN
	Records   []*wal.Record     // the decoded durable log (pre-recovery)
}

// Recover runs the three ARIES phases against the durable log in store,
// applying changes through pool (backed by disk). It returns the
// analysis tables and statistics, plus a WAL positioned past the end of
// the log so the system can resume appending.
func Recover(store wal.LogStore, pool *storage.BufferPool, disk storage.Disk, opts *Options) (*Result, *wal.WAL, error) {
	w := wal.New(store, 1)
	records, err := w.ReadAll()
	if err != nil {
		return nil, nil, err
	}
	if err := w.Resume(); err != nil {
		return nil, nil, err
	}
	stats := &Stats{RecordsScanned: len(records)}
	for _, r := range records {
		if r.TxnID > stats.MaxTxnID && r.Type != wal.RecCheckpoint {
			stats.MaxTxnID = r.TxnID
		}
	}
	res := &Result{Stats: stats, TxnTable: make(map[uint64]TxnTableEntry), DirtyPage: make(map[uint64]uint64), Records: records}

	// LSNs assigned by one WAL are dense, so a slice gives O(1) lookup
	// without a million-entry hash table; fall back to a map if sparse.
	var maxLSN uint64
	for _, r := range records {
		if r.LSN > maxLSN {
			maxLSN = r.LSN
		}
	}
	var lsnIndex *lsnLookup
	if maxLSN <= 2*uint64(len(records))+16 {
		lsnIndex = newDenseLookup(maxLSN + 64)
	} else {
		lsnIndex = newSparseLookup()
	}
	for _, r := range records {
		lsnIndex.put(r.LSN, r)
	}

	// ---------------- Phase 1: Analysis ----------------
	start := 0
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].Type == wal.RecCheckpoint {
			start = i
			stats.CheckpointLSN = records[i].LSN
			for _, te := range records[i].Txns {
				res.TxnTable[te.TxnID] = TxnTableEntry{Status: te.Status, LastLSN: te.LastLSN}
			}
			for _, de := range records[i].Dirty {
				res.DirtyPage[de.PageID] = de.RecLSN
			}
			break
		}
	}
	winners := make(map[uint64]bool)
	for _, rec := range records {
		if rec.Type == wal.RecCommit {
			winners[rec.TxnID] = true
		}
	}
	for _, rec := range records[start:] {
		switch rec.Type {
		case wal.RecBegin, wal.RecUpdate, wal.RecCLR:
			e := res.TxnTable[rec.TxnID]
			e.LastLSN = rec.LSN
			res.TxnTable[rec.TxnID] = e
		case wal.RecCommit:
			e := res.TxnTable[rec.TxnID]
			e.LastLSN = rec.LSN
			e.Status = wal.TxnCommitted
			res.TxnTable[rec.TxnID] = e
		case wal.RecAbort:
			e := res.TxnTable[rec.TxnID]
			e.LastLSN = rec.LSN
			e.Status = wal.TxnAborting
			res.TxnTable[rec.TxnID] = e
		case wal.RecEnd:
			delete(res.TxnTable, rec.TxnID)
		}
		if rec.Type == wal.RecUpdate || rec.Type == wal.RecCLR {
			if _, ok := res.DirtyPage[rec.PageID]; !ok {
				res.DirtyPage[rec.PageID] = rec.LSN
			}
		}
	}
	for id := range winners {
		stats.Winners = append(stats.Winners, id)
	}
	sort.Slice(stats.Winners, func(i, j int) bool { return stats.Winners[i] < stats.Winners[j] })

	// ---------------- Phase 2: Redo (repeat history) ----------------
	var redoLSN uint64
	for _, recLSN := range res.DirtyPage {
		if redoLSN == 0 || recLSN < redoLSN {
			redoLSN = recLSN
		}
	}
	if redoLSN != 0 {
		for _, rec := range records {
			if rec.LSN < redoLSN {
				continue
			}
			if rec.Type != wal.RecUpdate && rec.Type != wal.RecCLR {
				continue
			}
			recLSN, dirty := res.DirtyPage[rec.PageID]
			if !dirty || recLSN > rec.LSN {
				stats.RedoSkipped++
				continue
			}
			pg := pool.Fetch(rec.PageID)
			if pg.PageLSN >= rec.LSN {
				stats.RedoSkipped++
				continue
			}
			pg.Value = rec.After
			pg.PageLSN = rec.LSN
			if !pg.Dirty {
				pg.Dirty = true
				pg.RecLSN = rec.LSN
			}
			stats.Redone++
		}
	}

	// ---------------- Phase 3: Undo (losers, with CLRs) ----------------
	toUndo := &lsnHeap{}
	heap.Init(toUndo)
	losers := make([]uint64, 0, len(res.TxnTable))
	for id, e := range res.TxnTable {
		if e.Status == wal.TxnCommitted {
			continue // winner that never got its END record: nothing to undo
		}
		losers = append(losers, id)
		heap.Push(toUndo, e.LastLSN)
	}
	sort.Slice(losers, func(i, j int) bool { return losers[i] < losers[j] })
	stats.Losers = losers

	lastLSN := make(map[uint64]uint64, len(res.TxnTable))
	for id, e := range res.TxnTable {
		lastLSN[id] = e.LastLSN
	}

	for toUndo.Len() > 0 {
		lsn := heap.Pop(toUndo).(uint64)
		rec, ok := lsnIndex.get(lsn)
		if !ok {
			return nil, nil, fmt.Errorf("recovery: undo chain references missing LSN %d", lsn)
		}
		var next uint64
		switch rec.Type {
		case wal.RecCLR:
			next = rec.UndoNext
		case wal.RecUpdate:
			clr := &wal.Record{
				Type:     wal.RecCLR,
				TxnID:    rec.TxnID,
				PrevLSN:  lastLSN[rec.TxnID],
				PageID:   rec.PageID,
				UndoNext: rec.PrevLSN,
				Before:   rec.After,
				After:    rec.Before,
			}
			clrLSN := w.Append(clr)
			lsnIndex.put(clrLSN, clr)
			lastLSN[rec.TxnID] = clrLSN
			pg := pool.Fetch(rec.PageID)
			pg.Value = rec.Before
			pg.PageLSN = clrLSN
			if !pg.Dirty {
				pg.Dirty = true
				pg.RecLSN = clrLSN
			}
			stats.CLRsWritten++
			if opts != nil && opts.OnCLR != nil && opts.OnCLR(w, pool, stats.CLRsWritten) {
				w.LoseVolatile()
				pool.Reset()
				return res, w, ErrCrashInjected
			}
			next = rec.PrevLSN
		default: // BEGIN or anything else: chain ends
			next = 0
		}
		if next == 0 {
			end := &wal.Record{Type: wal.RecEnd, TxnID: rec.TxnID, PrevLSN: lastLSN[rec.TxnID]}
			endLSN := w.Append(end)
			lsnIndex.put(endLSN, end)
			delete(res.TxnTable, rec.TxnID)
		} else {
			heap.Push(toUndo, next)
		}
	}
	if err := w.Flush(); err != nil {
		return nil, nil, err
	}
	return res, w, nil
}

// lsnLookup maps LSN -> record, dense-slice backed when possible.
type lsnLookup struct {
	dense  []*wal.Record
	sparse map[uint64]*wal.Record
}

func newDenseLookup(size uint64) *lsnLookup { return &lsnLookup{dense: make([]*wal.Record, size)} }
func newSparseLookup() *lsnLookup           { return &lsnLookup{sparse: make(map[uint64]*wal.Record)} }

func (l *lsnLookup) put(lsn uint64, r *wal.Record) {
	if l.dense != nil {
		for uint64(len(l.dense)) <= lsn {
			l.dense = append(l.dense, make([]*wal.Record, 1024)...)
		}
		l.dense[lsn] = r
		return
	}
	l.sparse[lsn] = r
}

func (l *lsnLookup) get(lsn uint64) (*wal.Record, bool) {
	if l.dense != nil {
		if lsn < uint64(len(l.dense)) {
			r := l.dense[lsn]
			return r, r != nil
		}
		return nil, false
	}
	r, ok := l.sparse[lsn]
	return r, ok
}

// lsnHeap is a max-heap of LSNs (undo processes the largest first).
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

// ExpectedCommittedState independently computes the post-recovery page
// values: the effect of committed transactions only, in LSN order. Used
// by tests and the simulator to cross-check the recovery engine.
func ExpectedCommittedState(records []*wal.Record) map[uint64]int64 {
	committed := make(map[uint64]bool)
	for _, r := range records {
		if r.Type == wal.RecCommit {
			committed[r.TxnID] = true
		}
	}
	state := make(map[uint64]int64)
	for _, r := range records {
		if r.Type == wal.RecUpdate && committed[r.TxnID] {
			state[r.PageID] = r.After
		}
	}
	return state
}
