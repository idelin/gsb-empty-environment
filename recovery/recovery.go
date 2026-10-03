// Package recovery implements the classic ARIES crash recovery
// protocol: an Analysis pass that reconstructs the transaction table
// and dirty page table, a Redo pass that repeats history, and an
// Undo pass that rolls back loser transactions writing compensation
// log records (CLRs).
package recovery

import (
	"container/heap"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"

	"aries/store"
	"aries/wal"
)

// TxnAtt is one row of the transaction table built during Analysis.
type TxnAtt struct {
	Status  wal.TxnStatus
	LastLSN uint64
}

// ErrSimulatedCrash is returned by Undo when the CrashAfterUndo test
// hook triggers a simulated crash mid-undo.
var ErrSimulatedCrash = errors.New("recovery: simulated crash during undo")

// Engine performs ARIES recovery over a WAL and a page store.
type Engine struct {
	WAL   *wal.WAL
	Store *store.Store
	Log   *log.Logger // optional progress logger

	// CrashAfterUndo, when > 0, simulates a crash after that many
	// CLRs have been applied during Undo (test hook).
	CrashAfterUndo int
}

func (e *Engine) logf(format string, args ...interface{}) {
	if e.Log != nil {
		e.Log.Printf(format, args...)
	}
}

// Analysis scans the log from the most recent checkpoint (or the
// beginning of the log) and reconstructs the transaction table and
// the dirty page table. It also returns the LSN at which Redo must
// start (the smallest recLSN in the dirty page table).
func (e *Engine) Analysis() (tt map[uint64]*TxnAtt, dpt map[uint64]uint64, redoLSN uint64, err error) {
	// Locate the most recent checkpoint.
	var ckpt *wal.Record
	rd := e.WAL.ReaderFrom(0)
	for {
		rec, rerr := rd.Next()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, nil, 0, rerr
		}
		if rec.Type == wal.RecCheckpoint {
			ckpt = rec
		}
	}

	tt = make(map[uint64]*TxnAtt)
	dpt = make(map[uint64]uint64)
	var start uint64
	if ckpt != nil {
		start = ckpt.LSN
		for _, te := range ckpt.Txns {
			tt[te.TxnID] = &TxnAtt{Status: te.Status, LastLSN: te.LastLSN}
		}
		for _, pe := range ckpt.Pages {
			dpt[pe.PageID] = pe.RecLSN
		}
		e.logf("analysis: starting from checkpoint at LSN %d (%d txns, %d dirty pages)",
			ckpt.LSN, len(ckpt.Txns), len(ckpt.Pages))
	} else {
		e.logf("analysis: no checkpoint found, scanning from LSN 0")
	}

	rd = e.WAL.ReaderFrom(start)
	for {
		rec, rerr := rd.Next()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, nil, 0, rerr
		}
		switch rec.Type {
		case wal.RecUpdate, wal.RecCLR:
			att := tt[rec.TxnID]
			if att == nil {
				att = &TxnAtt{Status: wal.StatusRunning}
				tt[rec.TxnID] = att
			}
			att.LastLSN = rec.LSN
			if _, ok := dpt[rec.PageID]; !ok {
				dpt[rec.PageID] = rec.LSN
			}
		case wal.RecCommit:
			att := tt[rec.TxnID]
			if att == nil {
				att = &TxnAtt{}
				tt[rec.TxnID] = att
			}
			att.Status = wal.StatusCommitted
			att.LastLSN = rec.LSN
		case wal.RecAbort:
			att := tt[rec.TxnID]
			if att == nil {
				att = &TxnAtt{}
				tt[rec.TxnID] = att
			}
			att.Status = wal.StatusAborting
			att.LastLSN = rec.LSN
		case wal.RecEnd:
			delete(tt, rec.TxnID)
		}
	}

	redoLSN = e.WAL.End()
	for _, recLSN := range dpt {
		if recLSN < redoLSN {
			redoLSN = recLSN
		}
	}
	e.logf("analysis: complete: %d open txns, %d dirty pages, redo starts at LSN %d",
		len(tt), len(dpt), redoLSN)
	return tt, dpt, redoLSN, nil
}

// Redo repeats history: every update and CLR whose effects may be
// missing from the durable page image is reapplied.
func (e *Engine) Redo(dpt map[uint64]uint64, redoLSN uint64) error {
	rd := e.WAL.ReaderFrom(redoLSN)
	for {
		rec, err := rd.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if rec.Type != wal.RecUpdate && rec.Type != wal.RecCLR {
			continue
		}
		recLSN, dirty := dpt[rec.PageID]
		if !dirty || recLSN > rec.LSN {
			continue // page was not dirty when this record was written
		}
		pg := e.Store.Fetch(rec.PageID)
		if pg.PageLSN >= rec.LSN {
			continue // already on disk
		}
		if err := e.Store.Write(rec.PageID, int(rec.Offset), rec.After, rec.LSN); err != nil {
			return err
		}
		e.logf("redo: applied %s txn=%d page=%d lsn=%d", rec.Type, rec.TxnID, rec.PageID, rec.LSN)
	}
}

// lsnItem is a heap entry: the next record to undo for a transaction.
type lsnItem struct {
	lsn uint64
	txn uint64
}

// lsnHeap is a max-heap ordered by LSN so that undo proceeds in
// strictly reverse log order across all loser transactions.
type lsnHeap []lsnItem

func (h lsnHeap) Len() int            { return len(h) }
func (h lsnHeap) Less(i, j int) bool  { return h[i].lsn > h[j].lsn }
func (h lsnHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *lsnHeap) Push(x interface{}) { *h = append(*h, x.(lsnItem)) }
func (h *lsnHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// Undo rolls back every loser transaction (running or aborting at
// crash time) in reverse LSN order, logging a CLR for each undone
// update. Committed transactions missing their END record are
// finished first.
func (e *Engine) Undo(tt map[uint64]*TxnAtt) error {
	// Finish committed transactions that crashed before writing END.
	committed := make([]uint64, 0)
	for id, att := range tt {
		if att.Status == wal.StatusCommitted {
			committed = append(committed, id)
		}
	}
	sort.Slice(committed, func(i, j int) bool { return committed[i] < committed[j] })
	for _, id := range committed {
		if _, err := e.WAL.Append(&wal.Record{
			Type:    wal.RecEnd,
			TxnID:   id,
			PrevLSN: tt[id].LastLSN,
		}); err != nil {
			return err
		}
		e.logf("undo: wrote END for committed txn=%d", id)
		delete(tt, id)
	}

	h := &lsnHeap{}
	for id, att := range tt {
		heap.Push(h, lsnItem{lsn: att.LastLSN, txn: id})
	}
	e.logf("undo: %d loser transactions to roll back", h.Len())

	steps := 0
	for h.Len() > 0 {
		it := heap.Pop(h).(lsnItem)
		rec, err := e.WAL.ReadAt(it.lsn)
		if err != nil {
			return fmt.Errorf("recovery: undo read at LSN %d: %w", it.lsn, err)
		}
		switch rec.Type {
		case wal.RecUpdate:
			clr := &wal.Record{
				Type:     wal.RecCLR,
				TxnID:    rec.TxnID,
				PrevLSN:  tt[rec.TxnID].LastLSN,
				PageID:   rec.PageID,
				Offset:   rec.Offset,
				UndoNext: rec.PrevLSN,
				Before:   rec.After,
				After:    rec.Before,
			}
			clrLSN, err := e.WAL.Append(clr)
			if err != nil {
				return err
			}
			if err := e.WAL.Flush(); err != nil {
				return err
			}
			if err := e.Store.Write(rec.PageID, int(rec.Offset), rec.Before, clrLSN); err != nil {
				return err
			}
			tt[rec.TxnID].LastLSN = clrLSN
			e.logf("undo: CLR txn=%d page=%d clrLSN=%d undoNext=%d",
				rec.TxnID, rec.PageID, clrLSN, rec.PrevLSN)
			steps++
			if e.CrashAfterUndo > 0 && steps >= e.CrashAfterUndo {
				e.Store.Crash()
				return ErrSimulatedCrash
			}
			if rec.PrevLSN == 0 {
				if err := e.endTxn(tt, rec.TxnID); err != nil {
					return err
				}
			} else {
				heap.Push(h, lsnItem{lsn: rec.PrevLSN, txn: rec.TxnID})
			}
		case wal.RecCLR:
			// Already compensated: skip to undoNext, never undo twice.
			if rec.UndoNext == 0 {
				if err := e.endTxn(tt, rec.TxnID); err != nil {
					return err
				}
			} else {
				heap.Push(h, lsnItem{lsn: rec.UndoNext, txn: rec.TxnID})
			}
		case wal.RecAbort, wal.RecCommit:
			if rec.PrevLSN == 0 {
				if err := e.endTxn(tt, rec.TxnID); err != nil {
					return err
				}
			} else {
				heap.Push(h, lsnItem{lsn: rec.PrevLSN, txn: rec.TxnID})
			}
		default:
			return fmt.Errorf("recovery: unexpected record %s in undo chain", rec.Type)
		}
	}

	if err := e.Store.FlushAll(); err != nil {
		return err
	}
	e.logf("undo: complete, all pages flushed")
	return nil
}

func (e *Engine) endTxn(tt map[uint64]*TxnAtt, txnID uint64) error {
	_, err := e.WAL.Append(&wal.Record{
		Type:    wal.RecEnd,
		TxnID:   txnID,
		PrevLSN: tt[txnID].LastLSN,
	})
	if err != nil {
		return err
	}
	e.logf("undo: txn=%d fully rolled back, END written", txnID)
	delete(tt, txnID)
	return nil
}

// Recover runs the full ARIES pipeline: Analysis, Redo, Undo.
func (e *Engine) Recover() error {
	tt, dpt, redoLSN, err := e.Analysis()
	if err != nil {
		return err
	}
	if err := e.Redo(dpt, redoLSN); err != nil {
		return err
	}
	return e.Undo(tt)
}
