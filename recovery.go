package aries

import (
	"container/heap"
	"sort"
)

// TxnStatus is the analysis-phase state of a transaction.
type TxnStatus byte

// Transaction states tracked by the analysis phase.
const (
	TxnRunning TxnStatus = iota + 1
	TxnCommitted
	TxnAborted
)

// TxnInfo is one transaction-table entry after analysis.
type TxnInfo struct {
	Status  TxnStatus
	LastLSN LSN
}

// Report describes one recovery pass for inspection and testing.
type Report struct {
	CheckpointFound bool
	DPT             map[int64]LSN     // dirty page table after analysis
	TxnTable        map[int64]TxnInfo // transaction table after analysis
	Committed       []int64           // every txn that logged COMMIT
	Losers          []int64           // uncommitted, rolled back
	Redone          []LSN             // update/CLR records re-applied
	RedoSkipped     []LSN             // records skipped (already on disk)
	Undone          []LSN             // update records rolled back
	CLRs            []*LogRecord      // compensation records written
}

// RecoveryEngine executes the ARIES three-phase restart recovery against a
// WAL and the durable page store that survived the crash.
type RecoveryEngine struct {
	wal  *WAL
	disk *Disk
}

// NewRecoveryEngine builds an engine over the crashed system's WAL and disk.
func NewRecoveryEngine(wal *WAL, disk *Disk) *RecoveryEngine {
	return &RecoveryEngine{wal: wal, disk: disk}
}

// Recover runs Analysis, Redo and Undo in order and returns a Report.
func (e *RecoveryEngine) Recover() (*Report, error) {
	records, err := e.wal.ReadAll()
	if err != nil {
		return nil, err
	}
	byLSN := make(map[LSN]*LogRecord, len(records))
	for _, r := range records {
		byLSN[r.LSN] = r
	}
	rep := &Report{
		DPT:      make(map[int64]LSN),
		TxnTable: make(map[int64]TxnInfo),
	}
	committed := make(map[int64]bool)

	// ---------------- Phase 1: Analysis ----------------
	// Locate the most recent checkpoint and seed DPT/TT from it.
	start := 0
	for i, r := range records {
		if r.Type == RecCheckpoint {
			rep.CheckpointFound = true
			start = i + 1
			for id, lsn := range r.DPT {
				rep.DPT[id] = lsn
			}
			for id, lsn := range r.TT {
				rep.TxnTable[id] = TxnInfo{Status: TxnRunning, LastLSN: lsn}
			}
		}
	}
	// Committed txns are collected over the whole log: a txn that committed
	// before the checkpoint leaves no post-checkpoint trace.
	for _, r := range records {
		if r.Type == RecCommit {
			committed[r.TxnID] = true
		}
	}
	// Scan forward to end-of-log, maintaining both tables.
	for _, r := range records[start:] {
		switch r.Type {
		case RecBegin:
			rep.TxnTable[r.TxnID] = TxnInfo{Status: TxnRunning, LastLSN: r.LSN}
		case RecUpdate, RecCLR:
			info := rep.TxnTable[r.TxnID]
			info.Status = TxnRunning
			info.LastLSN = r.LSN
			rep.TxnTable[r.TxnID] = info
			if _, ok := rep.DPT[r.PageID]; !ok {
				rep.DPT[r.PageID] = r.LSN
			}
		case RecCommit:
			rep.TxnTable[r.TxnID] = TxnInfo{Status: TxnCommitted, LastLSN: r.LSN}
		case RecAbort:
			rep.TxnTable[r.TxnID] = TxnInfo{Status: TxnAborted, LastLSN: r.LSN}
		case RecEnd:
			delete(rep.TxnTable, r.TxnID)
		case RecCheckpoint:
			for id, lsn := range r.DPT {
				if _, ok := rep.DPT[id]; !ok {
					rep.DPT[id] = lsn
				}
			}
			for id, lsn := range r.TT {
				if _, ok := rep.TxnTable[id]; !ok {
					rep.TxnTable[id] = TxnInfo{Status: TxnRunning, LastLSN: lsn}
				}
			}
		}
	}
	for id := range committed {
		rep.Committed = append(rep.Committed, id)
	}
	for id, info := range rep.TxnTable {
		if info.Status != TxnCommitted {
			rep.Losers = append(rep.Losers, id)
		}
	}
	sort.Slice(rep.Committed, func(i, j int) bool { return rep.Committed[i] < rep.Committed[j] })
	sort.Slice(rep.Losers, func(i, j int) bool { return rep.Losers[i] < rep.Losers[j] })

	// ---------------- Phase 2: Redo (repeat history) ----------------
	var redoLSN LSN
	first := true
	for _, lsn := range rep.DPT {
		if first || lsn < redoLSN {
			redoLSN = lsn
			first = false
		}
	}
	if !first {
		for _, r := range records {
			if r.LSN < redoLSN || (r.Type != RecUpdate && r.Type != RecCLR) {
				continue
			}
			recLSN, dirty := rep.DPT[r.PageID]
			if !dirty || recLSN > r.LSN {
				rep.RedoSkipped = append(rep.RedoSkipped, r.LSN)
				continue
			}
			page := e.disk.ReadPage(r.PageID)
			if page.LSN >= r.LSN {
				rep.RedoSkipped = append(rep.RedoSkipped, r.LSN)
				continue // already reflected on disk
			}
			copy(page.Data[r.Offset:], r.NewData)
			page.LSN = r.LSN
			e.disk.WritePage(page)
			rep.Redone = append(rep.Redone, r.LSN)
		}
	}

	// ---------------- Phase 3: Undo (loser rollback with CLRs) ----------------
	pq := &lsnHeap{}
	heap.Init(pq)
	lastLSN := make(map[int64]LSN, len(rep.Losers))
	for _, id := range rep.Losers {
		last := rep.TxnTable[id].LastLSN
		lastLSN[id] = last
		heap.Push(pq, lsnItem{lsn: last, txn: id})
	}
	endTxn := func(txn int64) {
		e.wal.Append(&LogRecord{Type: RecEnd, TxnID: txn, PrevLSN: lastLSN[txn]})
		delete(lastLSN, txn)
	}
	for pq.Len() > 0 {
		item := heap.Pop(pq).(lsnItem)
		r := byLSN[item.lsn]
		if r == nil {
			continue
		}
		switch r.Type {
		case RecUpdate:
			clr := &LogRecord{
				Type:        RecCLR,
				TxnID:       r.TxnID,
				PrevLSN:     lastLSN[r.TxnID],
				PageID:      r.PageID,
				Offset:      r.Offset,
				OldData:     append([]byte(nil), r.NewData...),
				NewData:     append([]byte(nil), r.OldData...),
				UndoNextLSN: r.PrevLSN,
			}
			lsn := e.wal.Append(clr)
			page := e.disk.ReadPage(r.PageID)
			copy(page.Data[r.Offset:], r.OldData)
			page.LSN = lsn
			e.disk.WritePage(page)
			rep.CLRs = append(rep.CLRs, clr)
			rep.Undone = append(rep.Undone, r.LSN)
			lastLSN[r.TxnID] = lsn
			if r.PrevLSN != NilLSN {
				heap.Push(pq, lsnItem{lsn: r.PrevLSN, txn: r.TxnID})
			} else {
				endTxn(r.TxnID)
			}
		case RecCLR:
			// Already compensated once (crash during an earlier undo):
			// never undo a CLR, just follow the chain.
			if r.UndoNextLSN != NilLSN {
				heap.Push(pq, lsnItem{lsn: r.UndoNextLSN, txn: r.TxnID})
			} else {
				endTxn(r.TxnID)
			}
		case RecBegin:
			endTxn(r.TxnID)
		default:
			// ABORT records and strays: keep walking the chain.
			if r.PrevLSN != NilLSN {
				heap.Push(pq, lsnItem{lsn: r.PrevLSN, txn: r.TxnID})
			} else {
				endTxn(r.TxnID)
			}
		}
	}
	e.wal.Sync()
	return rep, nil
}

// lsnItem is a (lsn, txn) undo task ordered by descending LSN.
type lsnItem struct {
	lsn LSN
	txn int64
}

type lsnHeap []lsnItem

func (h lsnHeap) Len() int            { return len(h) }
func (h lsnHeap) Less(i, j int) bool  { return h[i].lsn > h[j].lsn } // max-heap
func (h lsnHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *lsnHeap) Push(x interface{}) { *h = append(*h, x.(lsnItem)) }
func (h *lsnHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
