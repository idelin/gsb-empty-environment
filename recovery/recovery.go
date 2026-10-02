package recovery

import (
	"math"
	"sort"

	"aries/storage"
	"aries/wal"
)

// TxnStatus is the recovery-time state of a transaction.
type TxnStatus int

const (
	StatusRunning TxnStatus = iota
	StatusCommitted
	StatusAborting
)

func (s TxnStatus) String() string {
	switch s {
	case StatusRunning:
		return "RUNNING"
	case StatusCommitted:
		return "COMMITTED"
	case StatusAborting:
		return "ABORTING"
	}
	return "?"
}

// TxnEntry is one row of the transaction table rebuilt by Analysis.
type TxnEntry struct {
	Status  TxnStatus
	LastLSN uint64
}

// AnalysisResult is the output of the analysis pass: the transaction
// table (winners and losers) and the dirty page table.
type AnalysisResult struct {
	Txns          map[uint64]*TxnEntry
	DPT           map[uint32]uint64 // pageID -> recLSN
	CheckpointLSN uint64            // 0 if the log has no checkpoint
}

// Report summarizes one recovery run.
type Report struct {
	Analysis    *AnalysisResult
	RedoApplied int      // update/CLR records re-applied
	RedoSkipped int      // records skipped (already on disk / not dirty)
	UndoCLRs    int      // compensation records written during undo
	EndsWritten int      // END records written (winners + finished losers)
	Losers      []uint64 // transaction IDs rolled back, sorted
}

// RecoveryManager performs ARIES recovery over a WAL and a page store.
type RecoveryManager struct {
	Wal   *wal.WAL
	Store *storage.PageStore
}

// Recover executes the full ARIES pipeline: Analysis, Redo, Undo, then
// forces the log and the buffer pool to disk.
func (rm *RecoveryManager) Recover() (*Report, error) {
	records := rm.Wal.Records()
	rep := &Report{}
	res := rm.Analyze(records)
	rep.Analysis = res
	rep.RedoApplied, rep.RedoSkipped = rm.Redo(records, res)
	if err := rm.Undo(res, rep); err != nil {
		return rep, err
	}
	if err := rm.Wal.Flush(); err != nil {
		return rep, err
	}
	rm.Store.FlushAll()
	return rep, nil
}

// Analyze scans the log forward from the last checkpoint (or the start of
// the log) and rebuilds the transaction table and the dirty page table.
func (rm *RecoveryManager) Analyze(records []*wal.Record) *AnalysisResult {
	res := &AnalysisResult{
		Txns: make(map[uint64]*TxnEntry),
		DPT:  make(map[uint32]uint64),
	}
	start := 0
	for i, rec := range records {
		if rec.Type == wal.TypeCheckpoint {
			start = i
			res.CheckpointLSN = rec.LSN
		}
	}
	if res.CheckpointLSN != 0 {
		cp := records[start]
		for pid, lsn := range cp.DirtyPages {
			res.DPT[pid] = lsn
		}
		for tid, lsn := range cp.Txns {
			res.Txns[tid] = &TxnEntry{Status: StatusRunning, LastLSN: lsn}
		}
	}
	for _, rec := range records[start:] {
		switch rec.Type {
		case wal.TypeBegin:
			res.Txns[rec.TxnID] = &TxnEntry{Status: StatusRunning, LastLSN: rec.LSN}
		case wal.TypeUpdate, wal.TypeCLR:
			e, ok := res.Txns[rec.TxnID]
			if !ok {
				e = &TxnEntry{Status: StatusRunning}
				res.Txns[rec.TxnID] = e
			}
			e.LastLSN = rec.LSN
			if _, ok := res.DPT[rec.PageID]; !ok {
				res.DPT[rec.PageID] = rec.LSN
			}
		case wal.TypeCommit:
			res.Txns[rec.TxnID] = &TxnEntry{Status: StatusCommitted, LastLSN: rec.LSN}
		case wal.TypeAbort:
			res.Txns[rec.TxnID] = &TxnEntry{Status: StatusAborting, LastLSN: rec.LSN}
		case wal.TypeEnd:
			delete(res.Txns, rec.TxnID)
		}
	}
	return res
}

// Redo repeats history: starting from the smallest recLSN in the dirty
// page table, every UPDATE and CLR whose effects may be missing from disk
// is re-applied. A record is applied only if its page is in the DPT with
// recLSN <= record LSN and the on-disk pageLSN < record LSN, which makes
// the pass idempotent.
func (rm *RecoveryManager) Redo(records []*wal.Record, res *AnalysisResult) (applied, skipped int) {
	if len(res.DPT) == 0 {
		return 0, 0
	}
	start := uint64(math.MaxUint64)
	for _, recLSN := range res.DPT {
		if recLSN < start {
			start = recLSN
		}
	}
	for _, rec := range records {
		if rec.LSN < start {
			continue
		}
		if rec.Type != wal.TypeUpdate && rec.Type != wal.TypeCLR {
			continue
		}
		recLSN, ok := res.DPT[rec.PageID]
		if !ok || recLSN > rec.LSN {
			skipped++
			continue
		}
		page := rm.Store.Fetch(rec.PageID)
		if page.LSN >= rec.LSN {
			skipped++
			continue
		}
		copy(page.Data[rec.Offset:], rec.NewData)
		page.LSN = rec.LSN
		rm.Store.UnpinDirty(page)
		applied++
	}
	return applied, skipped
}

// Undo rolls back every loser transaction in reverse LSN order (largest
// LSN first across all losers), writing a CLR for each undone update and
// following UndoNextLSN so work already logged by a previous, interrupted
// undo is never repeated. Committed transactions are finalized with END
// records first.
func (rm *RecoveryManager) Undo(res *AnalysisResult, rep *Report) error {
	// Winners: finalize committed transactions.
	for tid, e := range res.Txns {
		if e.Status == StatusCommitted {
			rm.Wal.Append(&wal.Record{Type: wal.TypeEnd, TxnID: tid, PrevLSN: e.LastLSN})
			delete(res.Txns, tid)
			rep.EndsWritten++
		}
	}

	// Losers: priority queue (by LSN, descending) of records to process.
	last := make(map[uint64]uint64)
	var pq []uint64
	for tid, e := range res.Txns {
		last[tid] = e.LastLSN
		pq = append(pq, e.LastLSN)
		rep.Losers = append(rep.Losers, tid)
	}
	sort.Slice(rep.Losers, func(i, j int) bool { return rep.Losers[i] < rep.Losers[j] })

	popMax := func() uint64 {
		mi := 0
		for i := range pq {
			if pq[i] > pq[mi] {
				mi = i
			}
		}
		lsn := pq[mi]
		pq = append(pq[:mi], pq[mi+1:]...)
		return lsn
	}

	for len(pq) > 0 {
		lsn := popMax()
		rec := rm.Wal.Record(lsn)
		if rec == nil {
			continue
		}
		switch rec.Type {
		case wal.TypeUpdate:
			clr := &wal.Record{
				Type:        wal.TypeCLR,
				TxnID:       rec.TxnID,
				PrevLSN:     last[rec.TxnID],
				PageID:      rec.PageID,
				Offset:      rec.Offset,
				OldData:     rec.NewData,
				NewData:     rec.OldData,
				UndoNextLSN: rec.PrevLSN,
			}
			clrLSN := rm.Wal.Append(clr)
			page := rm.Store.Fetch(rec.PageID)
			copy(page.Data[rec.Offset:], rec.OldData)
			page.LSN = clrLSN
			rm.Store.UnpinDirty(page)
			last[rec.TxnID] = clrLSN
			rep.UndoCLRs++
			if rec.PrevLSN != 0 {
				pq = append(pq, rec.PrevLSN)
			}
		case wal.TypeCLR:
			if rec.UndoNextLSN != 0 {
				pq = append(pq, rec.UndoNextLSN)
			}
		case wal.TypeAbort:
			if rec.PrevLSN != 0 {
				pq = append(pq, rec.PrevLSN)
			}
		case wal.TypeBegin:
			rm.Wal.Append(&wal.Record{Type: wal.TypeEnd, TxnID: rec.TxnID, PrevLSN: last[rec.TxnID]})
			rep.EndsWritten++
		}
	}
	return nil
}
