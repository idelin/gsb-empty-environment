package aries

import (
	"fmt"
	"sort"
	"strings"
)

// TxnEntry is a row of the transaction table rebuilt during Analysis.
type TxnEntry struct {
	Status  TxnStatus
	LastLSN LSN
}

// Engine performs ARIES recovery over a WAL and a page store.
type Engine struct {
	WAL  *WAL
	Disk *PageStore

	// Analysis results.
	TxnTable map[uint64]*TxnEntry
	DPT      map[uint64]LSN // pageID -> recLSN

	// Statistics, useful for tests and the demo.
	Redone      int // update/CLR records re-applied during Redo
	Undone      int // update records rolled back during Undo
	CLRsWritten int
	EndsWritten int
}

// NewEngine creates a recovery engine.
func NewEngine(wal *WAL, disk *PageStore) *Engine {
	return &Engine{
		WAL:      wal,
		Disk:     disk,
		TxnTable: make(map[uint64]*TxnEntry),
		DPT:      make(map[uint64]LSN),
	}
}

// Recover runs the full ARIES pipeline: Analysis, Redo, Undo.
func (e *Engine) Recover() error {
	if err := e.Analysis(); err != nil {
		return fmt.Errorf("analysis: %w", err)
	}
	if err := e.Redo(); err != nil {
		return fmt.Errorf("redo: %w", err)
	}
	if err := e.Undo(); err != nil {
		return fmt.Errorf("undo: %w", err)
	}
	return nil
}

// Analysis scans the log from the most recent checkpoint (or the beginning)
// and rebuilds the transaction table and the dirty page table exactly as
// they were at crash time.
func (e *Engine) Analysis() error {
	recs, err := e.WAL.Scan()
	if err != nil {
		return err
	}
	// Locate the last checkpoint and preload its snapshot.
	start := 0
	for i, r := range recs {
		if r.Type == RecCheckpoint {
			start = i + 1
			// A checkpoint snapshot replaces everything rebuilt so far.
			e.TxnTable = make(map[uint64]*TxnEntry)
			e.DPT = make(map[uint64]LSN)
			for id, snap := range r.Txns {
				e.TxnTable[id] = &TxnEntry{Status: snap.Status, LastLSN: snap.LastLSN}
			}
			for pageID, recLSN := range r.DPT {
				e.DPT[pageID] = recLSN
			}
		}
	}
	for _, r := range recs[start:] {
		switch r.Type {
		case RecBegin:
			e.TxnTable[r.TxnID] = &TxnEntry{Status: TxnRunning, LastLSN: r.LSN}
		case RecUpdate, RecCLR:
			e.touchTxn(r.TxnID).LastLSN = r.LSN
			if _, ok := e.DPT[r.PageID]; !ok {
				e.DPT[r.PageID] = r.LSN // recLSN: first record that dirtied the page
			}
		case RecCommit:
			entry := e.touchTxn(r.TxnID)
			entry.Status = TxnCommitted
			entry.LastLSN = r.LSN
		case RecAbort:
			entry := e.touchTxn(r.TxnID)
			entry.Status = TxnAborting
			entry.LastLSN = r.LSN
		case RecEnd:
			delete(e.TxnTable, r.TxnID)
		}
	}
	return nil
}

func (e *Engine) touchTxn(id uint64) *TxnEntry {
	entry, ok := e.TxnTable[id]
	if !ok {
		entry = &TxnEntry{Status: TxnRunning}
		e.TxnTable[id] = entry
	}
	return entry
}

// Redo repeats history: starting from the smallest recLSN in the DPT it
// re-applies every logged update and CLR whose effects had not reached disk,
// restoring the exact physical state at crash time.
func (e *Engine) Redo() error {
	if len(e.DPT) == 0 {
		return nil
	}
	redoFrom := LSN(^uint64(0))
	for _, recLSN := range e.DPT {
		if recLSN < redoFrom {
			redoFrom = recLSN
		}
	}
	recs, err := e.WAL.ScanFrom(redoFrom)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if r.Type != RecUpdate && r.Type != RecCLR {
			continue
		}
		recLSN, dirty := e.DPT[r.PageID]
		if !dirty || recLSN > r.LSN {
			continue // page was clean when this record was written
		}
		page, err := e.Disk.ReadPage(r.PageID)
		if err != nil {
			return err
		}
		if page.LSN >= r.LSN {
			continue // already on disk
		}
		copy(page.Data, r.After)
		page.LSN = r.LSN
		if err := e.Disk.WritePage(page); err != nil {
			return err
		}
		e.Redone++
	}
	return nil
}

// Undo rolls back every loser transaction (running or aborting at crash
// time) by walking the prevLSN/undoNext chains backwards, emitting a CLR per
// undone update and an END record per finished transaction. Transactions
// that committed but never got their END record are ended here as well.
func (e *Engine) Undo() error {
	// Committed transactions only need their END record.
	for _, id := range e.sortedTxns() {
		if e.TxnTable[id].Status == TxnCommitted {
			if err := e.appendSimple(&LogRecord{Type: RecEnd, TxnID: id}); err != nil {
				return err
			}
			delete(e.TxnTable, id)
			e.EndsWritten++
		}
	}
	// Losers: transactions still running or aborting at crash time.
	losers := make(map[uint64]LSN)
	for id, entry := range e.TxnTable {
		losers[id] = entry.LastLSN
	}
	for len(losers) > 0 {
		// Always undo the transaction with the largest LSN first so the
		// global undo order is the exact reverse of the log order.
		txnID := maxLSNTxn(losers)
		rec, err := e.WAL.ReadAt(losers[txnID])
		if err != nil {
			return err
		}
		switch rec.Type {
		case RecUpdate:
			clr := &LogRecord{
				Type:     RecCLR,
				TxnID:    txnID,
				PrevLSN:  losers[txnID],
				PageID:   rec.PageID,
				UndoNext: rec.PrevLSN,
				After:    rec.Before, // CLR re-installs the before image
			}
			clrLSN, err := e.WAL.Append(clr)
			if err != nil {
				return err
			}
			e.CLRsWritten++
			page, err := e.Disk.ReadPage(rec.PageID)
			if err != nil {
				return err
			}
			if page.LSN < clrLSN {
				copy(page.Data, rec.Before)
				page.LSN = clrLSN
				if err := e.Disk.WritePage(page); err != nil {
					return err
				}
			}
			e.Undone++
			losers[txnID] = rec.PrevLSN
		case RecCLR:
			// Already compensated before the crash: jump to undoNext.
			losers[txnID] = rec.UndoNext
		case RecBegin:
			losers[txnID] = NilLSN
		case RecAbort:
			// Crash happened after the ABORT was logged but before any CLR:
			// skip the ABORT record and continue with the last update/CLR.
			losers[txnID] = rec.PrevLSN
		default:
			return fmt.Errorf("aries: unexpected %s record in undo chain of txn %d",
				rec.Type, txnID)
		}
		if losers[txnID] == NilLSN {
			if err := e.appendSimple(&LogRecord{Type: RecEnd, TxnID: txnID}); err != nil {
				return err
			}
			e.EndsWritten++
			delete(losers, txnID)
			delete(e.TxnTable, txnID)
		}
	}
	return nil
}

func (e *Engine) appendSimple(rec *LogRecord) error {
	_, err := e.WAL.Append(rec)
	return err
}

func (e *Engine) sortedTxns() []uint64 {
	ids := make([]uint64, 0, len(e.TxnTable))
	for id := range e.TxnTable {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func maxLSNTxn(losers map[uint64]LSN) uint64 {
	var best uint64
	var bestLSN LSN
	first := true
	for id, lsn := range losers {
		if first || lsn > bestLSN {
			best, bestLSN, first = id, lsn, false
		}
	}
	return best
}

// Describe renders the analysis tables, used by the demo.
func (e *Engine) Describe() string {
	var b strings.Builder
	b.WriteString("Transaction Table:\n")
	for _, id := range e.sortedTxns() {
		entry := e.TxnTable[id]
		fmt.Fprintf(&b, "  txn=%-3d status=%-10s lastLSN=%d\n", id, entry.Status, entry.LastLSN)
	}
	if len(e.TxnTable) == 0 {
		b.WriteString("  (empty)\n")
	}
	b.WriteString("Dirty Page Table:\n")
	pages := make([]uint64, 0, len(e.DPT))
	for p := range e.DPT {
		pages = append(pages, p)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i] < pages[j] })
	for _, p := range pages {
		fmt.Fprintf(&b, "  page=%-3d recLSN=%d\n", p, e.DPT[p])
	}
	if len(e.DPT) == 0 {
		b.WriteString("  (empty)\n")
	}
	return b.String()
}
