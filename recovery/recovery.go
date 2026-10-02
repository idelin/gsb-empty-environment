// Package recovery implements the ARIES crash-recovery protocol:
//
//	Analysis: scan the log from the last checkpoint, rebuilding the
//	          transaction table (TT) and dirty page table (DPT).
//	Redo:     repeat history from the oldest recLSN in the DPT, restoring
//	          the exact pre-crash page state (including loser updates).
//	Undo:     roll back loser transactions in reverse LSN order, writing
//	          compensation log records (CLRs) so recovery is idempotent.
//
// Every phase is instrumented with fine-grained metrics: phase latency
// timers, redo/undo counters, table-size gauges and error-code counters.
package recovery

import (
	"io"
	"time"

	"aries/metrics"
	"aries/storage"
	"aries/wal"
)

// Error codes recorded in the metrics registry.
const (
	ErrWALCorrupt = "WAL_CORRUPT"
	ErrRedoFetch  = "REDO_PAGE_FETCH"
	ErrUndoRead   = "UNDO_RECORD_READ"
	ErrUndoFetch  = "UNDO_PAGE_FETCH"
	ErrLogAppend  = "LOG_APPEND"
	ErrLogFlush   = "LOG_FLUSH"
	ErrPageFlush  = "PAGE_FLUSH"
)

// Engine runs ARIES recovery over a log manager and buffer pool.
type Engine struct {
	Log     *wal.LogManager
	Pool    *storage.BufferPool
	Metrics *metrics.Registry
}

// NewEngine builds an Engine. reg may be nil (metrics are then skipped).
func NewEngine(lm *wal.LogManager, pool *storage.BufferPool, reg *metrics.Registry) *Engine {
	return &Engine{Log: lm, Pool: pool, Metrics: reg}
}

func (e *Engine) inc(name string) {
	if e.Metrics != nil {
		e.Metrics.Counter(name).Inc()
	}
}

func (e *Engine) add(name string, n int64) {
	if e.Metrics != nil {
		e.Metrics.Counter(name).Add(n)
	}
}

func (e *Engine) gauge(name string, v int64) {
	if e.Metrics != nil {
		e.Metrics.Gauge(name).Set(v)
	}
}

func (e *Engine) incError(code string) {
	if e.Metrics != nil {
		e.Metrics.IncError(code)
	}
}

func (e *Engine) timePhase(name string) func() {
	if e.Metrics == nil {
		return func() {}
	}
	start := time.Now()
	timer := e.Metrics.Timer(name)
	return func() { timer.ObserveSince(start) }
}

// AnalysisResult holds the rebuilt transaction table and dirty page table.
type AnalysisResult struct {
	TT             map[int64]wal.TxnInfo
	DPT            map[int32]wal.LSN
	CheckpointLSN  wal.LSN // LSN of the last END_CHECKPOINT, LSNInvalid if none
	ScannedRecords int64
}

// Losers returns the ids of transactions that must be rolled back.
func (r *AnalysisResult) Losers() []int64 {
	var losers []int64
	for id, info := range r.TT {
		if info.Status == wal.StatusRunning || info.Status == wal.StatusAborting {
			losers = append(losers, id)
		}
	}
	return losers
}

// Analysis scans the log and rebuilds the TT and DPT as of the crash.
func (e *Engine) Analysis() (*AnalysisResult, error) {
	defer e.timePhase("recovery.analysis")()
	res := &AnalysisResult{
		TT:            make(map[int64]wal.TxnInfo),
		DPT:           make(map[int32]wal.LSN),
		CheckpointLSN: wal.LSNInvalid,
	}

	// Pass 1: locate the most recent END_CHECKPOINT.
	it := e.Log.Iterator()
	for {
		rec, err := it.Next()
		if err == io.EOF || err == wal.ErrCorrupt {
			if err == wal.ErrCorrupt {
				e.incError(ErrWALCorrupt)
			}
			break
		}
		if err != nil {
			return nil, err
		}
		if rec.Type == wal.TypeEndCheckpoint {
			res.CheckpointLSN = rec.LSN
		}
	}

	// Pass 2: scan forward from the matching BEGIN_CHECKPOINT (or the log
	// start when no checkpoint exists), seeding from the checkpoint's
	// TT/DPT snapshot when it is encountered.
	start := wal.LSN(0)
	if res.CheckpointLSN != wal.LSNInvalid {
		endCP, err := e.Log.ReadRecord(res.CheckpointLSN)
		if err != nil {
			return nil, err
		}
		if endCP.PrevLSN != wal.LSNInvalid {
			start = endCP.PrevLSN
		}
	}
	it = e.Log.IteratorFrom(start)
	for {
		rec, err := it.Next()
		if err == io.EOF || err == wal.ErrCorrupt {
			if err == wal.ErrCorrupt {
				e.incError(ErrWALCorrupt)
			}
			break
		}
		if err != nil {
			return nil, err
		}
		res.ScannedRecords++
		switch rec.Type {
		case wal.TypeEndCheckpoint:
			for pageID, recLSN := range rec.DPT {
				res.DPT[pageID] = recLSN
			}
			for txnID, info := range rec.TT {
				res.TT[txnID] = info
			}
		case wal.TypeUpdate, wal.TypeCLR:
			info, ok := res.TT[rec.TxnID]
			if !ok {
				info = wal.TxnInfo{Status: wal.StatusRunning}
			}
			info.LastLSN = rec.LSN
			res.TT[rec.TxnID] = info
			if _, ok := res.DPT[rec.PageID]; !ok {
				res.DPT[rec.PageID] = rec.LSN
			}
		case wal.TypeCommit:
			res.TT[rec.TxnID] = wal.TxnInfo{Status: wal.StatusCommitted, LastLSN: rec.LSN}
		case wal.TypeAbort:
			res.TT[rec.TxnID] = wal.TxnInfo{Status: wal.StatusAborting, LastLSN: rec.LSN}
		case wal.TypeEnd:
			delete(res.TT, rec.TxnID)
		}
	}
	e.gauge("recovery.tt_size", int64(len(res.TT)))
	e.gauge("recovery.dpt_size", int64(len(res.DPT)))
	return res, nil
}

// Redo repeats history: every logged update whose effects may be missing
// from disk is reapplied, restoring the exact pre-crash state.
func (e *Engine) Redo(res *AnalysisResult) error {
	defer e.timePhase("recovery.redo")()
	if len(res.DPT) == 0 {
		return nil
	}
	start := res.DPT[0]
	for _, recLSN := range res.DPT {
		if recLSN < start {
			start = recLSN
		}
	}
	var redone, skipped int64
	it := e.Log.IteratorFrom(start)
	for {
		rec, err := it.Next()
		if err == io.EOF || err == wal.ErrCorrupt {
			if err == wal.ErrCorrupt {
				e.incError(ErrWALCorrupt)
			}
			break
		}
		if err != nil {
			return err
		}
		if rec.Type != wal.TypeUpdate && rec.Type != wal.TypeCLR {
			continue
		}
		recLSN, ok := res.DPT[rec.PageID]
		if !ok || recLSN > rec.LSN {
			skipped++ // page not dirty, or dirtied only after this record
			continue
		}
		page, err := e.Pool.FetchPage(rec.PageID)
		if err != nil {
			e.incError(ErrRedoFetch)
			return err
		}
		if page.LSN < rec.LSN {
			copy(page.Data[rec.Offset:], rec.After)
			page.LSN = rec.LSN
			e.Pool.UnpinPage(rec.PageID, true)
			redone++
		} else {
			e.Pool.UnpinPage(rec.PageID, false)
			skipped++
		}
	}
	e.add("recovery.pages_redone", redone)
	e.add("recovery.redo_skipped", skipped)
	return nil
}

// Undo rolls back every loser transaction in reverse LSN order, writing a
// CLR per undone update and an END record per finished transaction.
func (e *Engine) Undo(res *AnalysisResult) error {
	defer e.timePhase("recovery.undo")()
	type pending struct {
		txnID int64
		lsn   wal.LSN
	}
	var queue []pending
	lastLSN := make(map[int64]wal.LSN)
	for txnID, info := range res.TT {
		if info.Status == wal.StatusCommitted {
			continue // winner: commit record already durable
		}
		queue = append(queue, pending{txnID: txnID, lsn: info.LastLSN})
		lastLSN[txnID] = info.LastLSN
	}
	var clrs, rolledBack int64
	for len(queue) > 0 {
		// Always undo the record with the largest LSN first.
		maxIdx := 0
		for i := range queue {
			if queue[i].lsn > queue[maxIdx].lsn {
				maxIdx = i
			}
		}
		cur := queue[maxIdx]
		rec, err := e.Log.ReadRecord(cur.lsn)
		if err != nil {
			e.incError(ErrUndoRead)
			return err
		}
		var next wal.LSN
		switch rec.Type {
		case wal.TypeUpdate:
			page, err := e.Pool.FetchPage(rec.PageID)
			if err != nil {
				e.incError(ErrUndoFetch)
				return err
			}
			clrLSN, err := e.Log.Append(&wal.LogRecord{
				TxnID:       rec.TxnID,
				Type:        wal.TypeCLR,
				PrevLSN:     lastLSN[rec.TxnID],
				UndoNextLSN: rec.PrevLSN,
				PageID:      rec.PageID,
				Offset:      rec.Offset,
				After:       rec.Before,
			})
			if err != nil {
				e.incError(ErrLogAppend)
				e.Pool.UnpinPage(rec.PageID, false)
				return err
			}
			copy(page.Data[rec.Offset:], rec.Before)
			page.LSN = clrLSN
			e.Pool.UnpinPage(rec.PageID, true)
			lastLSN[rec.TxnID] = clrLSN
			clrs++
			next = rec.PrevLSN
		case wal.TypeCLR:
			next = rec.UndoNextLSN // already compensated; skip over it
		default: // ABORT and anything else: keep walking the chain
			next = rec.PrevLSN
		}
		if next == wal.LSNInvalid {
			if _, err := e.Log.Append(&wal.LogRecord{
				TxnID: rec.TxnID, Type: wal.TypeEnd, PrevLSN: lastLSN[rec.TxnID],
			}); err != nil {
				e.incError(ErrLogAppend)
				return err
			}
			rolledBack++
			queue = append(queue[:maxIdx], queue[maxIdx+1:]...)
		} else {
			queue[maxIdx].lsn = next
		}
	}
	if err := e.Log.Flush(); err != nil {
		e.incError(ErrLogFlush)
		return err
	}
	if err := e.Pool.FlushAll(); err != nil {
		e.incError(ErrPageFlush)
		return err
	}
	e.add("recovery.clr_written", clrs)
	e.add("recovery.txns_rolled_back", rolledBack)
	return nil
}

// Recover runs the full Analysis -> Redo -> Undo pipeline.
func (e *Engine) Recover() (*AnalysisResult, error) {
	res, err := e.Analysis()
	if err != nil {
		return nil, err
	}
	if err := e.Redo(res); err != nil {
		return nil, err
	}
	if err := e.Undo(res); err != nil {
		return nil, err
	}
	return res, nil
}
