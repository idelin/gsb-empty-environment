package aries

import (
	"fmt"
	"sort"
)

// RecoveryManager executes the three ARIES phases over a WAL and a disk:
// Analysis (rebuild ATT/DPT), Redo (repeat history), Undo (roll back losers
// while logging CLRs).
type RecoveryManager struct {
	wal  *WAL
	disk *DiskManager

	// Analysis results.
	ATT map[uint64]*TxnRecord // transaction table at crash time
	DPT map[uint64]uint64     // dirty page table: pageID -> recLSN

	records []*LogRecord
	byLSN   map[uint64]*LogRecord
}

// NewRecoveryManager prepares a recovery run.
func NewRecoveryManager(wal *WAL, disk *DiskManager) *RecoveryManager {
	return &RecoveryManager{wal: wal, disk: disk}
}

// Recover runs Analysis, Redo and Undo in order and syncs the disk.
func (rm *RecoveryManager) Recover() error {
	records, err := rm.wal.Records()
	if err != nil {
		return err
	}
	rm.records = records
	rm.byLSN = make(map[uint64]*LogRecord, len(records))
	for _, r := range records {
		rm.byLSN[r.LSN] = r
	}
	if err := rm.Analysis(); err != nil {
		return err
	}
	if err := rm.Redo(); err != nil {
		return err
	}
	if err := rm.Undo(); err != nil {
		return err
	}
	return rm.disk.Sync()
}

// Analysis rebuilds the transaction table and dirty page table by scanning
// forward from the most recent checkpoint (or the log start).
func (rm *RecoveryManager) Analysis() error {
	rm.ATT = make(map[uint64]*TxnRecord)
	rm.DPT = make(map[uint64]uint64)

	start := 0
	for i := len(rm.records) - 1; i >= 0; i-- {
		if rm.records[i].Type == LogCheckpoint {
			cp := rm.records[i].Checkpoint
			for id, tr := range cp.Transactions {
				trCopy := tr
				rm.ATT[id] = &trCopy
			}
			for pageID, recLSN := range cp.DirtyPages {
				rm.DPT[pageID] = recLSN
			}
			start = i + 1
			break
		}
	}

	for _, rec := range rm.records[start:] {
		switch rec.Type {
		case LogUpdate, LogCLR:
			rm.touchTxn(rec.TxnID, TxnRunning, rec.LSN)
			if _, ok := rm.DPT[rec.PageID]; !ok {
				rm.DPT[rec.PageID] = rec.LSN
			}
		case LogCommit:
			rm.touchTxn(rec.TxnID, TxnCommitted, rec.LSN)
		case LogAbort:
			rm.touchTxn(rec.TxnID, TxnAborted, rec.LSN)
		case LogEnd:
			delete(rm.ATT, rec.TxnID)
		}
	}
	return nil
}

func (rm *RecoveryManager) touchTxn(txnID uint64, status TxnStatus, lsn uint64) {
	tr, ok := rm.ATT[txnID]
	if !ok {
		tr = &TxnRecord{TxnID: txnID}
		rm.ATT[txnID] = tr
	}
	tr.Status = status
	tr.LastLSN = lsn
}

// Redo repeats history: starting from the smallest recLSN in the DPT,
// re-apply every UPDATE/CLR whose effects are not yet on disk.
func (rm *RecoveryManager) Redo() error {
	if len(rm.DPT) == 0 {
		return nil
	}
	startLSN := uint64(0)
	first := true
	for _, recLSN := range rm.DPT {
		if first || recLSN < startLSN {
			startLSN = recLSN
			first = false
		}
	}
	for _, rec := range rm.records {
		if rec.LSN < startLSN {
			continue
		}
		if rec.Type != LogUpdate && rec.Type != LogCLR {
			continue
		}
		recLSN, dirty := rm.DPT[rec.PageID]
		if !dirty || recLSN > rec.LSN {
			continue // page not dirty, or was dirtied after this record
		}
		page, err := rm.disk.ReadPage(rec.PageID)
		if err != nil {
			return err
		}
		if page.LSN >= rec.LSN {
			continue // already applied
		}
		copy(page.Payload, rec.After)
		page.LSN = rec.LSN
		if err := rm.disk.WritePage(page); err != nil {
			return err
		}
	}
	return nil
}

// Undo rolls back every loser transaction (running or aborted at crash time)
// in globally descending LSN order, writing a CLR per undone update and an
// END record once a transaction is fully undone.
func (rm *RecoveryManager) Undo() error {
	// toUndo: candidate LSNs still to process, per loser transaction.
	toUndo := make(map[uint64]uint64) // txnID -> next LSN to undo
	lastLSN := make(map[uint64]uint64)
	for id, tr := range rm.ATT {
		if tr.Status == TxnCommitted {
			continue // winner: commit record seen, nothing to undo
		}
		lastLSN[id] = tr.LastLSN
		if tr.LastLSN != 0 {
			toUndo[id] = tr.LastLSN
		} else {
			if err := rm.writeEnd(id, 0); err != nil {
				return err
			}
		}
	}

	for len(toUndo) > 0 {
		// Pick the maximum LSN among all candidates.
		var pickTxn, pickLSN uint64
		first := true
		for txnID, lsn := range toUndo {
			if first || lsn > pickLSN {
				pickTxn, pickLSN, first = txnID, lsn, false
			}
		}
		delete(toUndo, pickTxn)

		rec, ok := rm.byLSN[pickLSN]
		if !ok {
			return fmt.Errorf("recovery: dangling undo reference LSN %d for txn %d", pickLSN, pickTxn)
		}
		switch rec.Type {
		case LogUpdate:
			// Write CLR, undo the update on disk. The CLR chains onto the
			// transaction's current tail, keeping the prevLSN chain intact.
			clr := &LogRecord{
				PrevLSN:     lastLSN[pickTxn],
				TxnID:       rec.TxnID,
				Type:        LogCLR,
				PageID:      rec.PageID,
				UndoNextLSN: rec.PrevLSN,
				Before:      rec.After,  // the value being removed
				After:       rec.Before, // restore the before image
			}
			clrLSN, err := rm.wal.Append(clr)
			if err != nil {
				return err
			}
			rm.byLSN[clrLSN] = clr
			lastLSN[pickTxn] = clrLSN
			page, err := rm.disk.ReadPage(rec.PageID)
			if err != nil {
				return err
			}
			copy(page.Payload, rec.Before)
			page.LSN = clrLSN
			if err := rm.disk.WritePage(page); err != nil {
				return err
			}
			if rec.PrevLSN == 0 {
				if err := rm.writeEnd(pickTxn, lastLSN[pickTxn]); err != nil {
					return err
				}
			} else {
				toUndo[pickTxn] = rec.PrevLSN
			}
		case LogCLR:
			// Already compensated: skip to undoNextLSN.
			if rec.UndoNextLSN == 0 {
				if err := rm.writeEnd(pickTxn, lastLSN[pickTxn]); err != nil {
					return err
				}
			} else {
				toUndo[pickTxn] = rec.UndoNextLSN
			}
		case LogAbort:
			if rec.PrevLSN == 0 {
				if err := rm.writeEnd(pickTxn, lastLSN[pickTxn]); err != nil {
					return err
				}
			} else {
				toUndo[pickTxn] = rec.PrevLSN
			}
		default:
			return fmt.Errorf("recovery: unexpected record type %s in undo chain of txn %d", rec.Type, pickTxn)
		}
	}
	return nil
}

func (rm *RecoveryManager) writeEnd(txnID, prevLSN uint64) error {
	end := &LogRecord{PrevLSN: prevLSN, TxnID: txnID, Type: LogEnd}
	lsn, err := rm.wal.Append(end)
	if err != nil {
		return err
	}
	rm.byLSN[lsn] = end
	return nil
}

// Losers returns the sorted IDs of transactions that Undo must roll back.
func (rm *RecoveryManager) Losers() []uint64 {
	var out []uint64
	for id, tr := range rm.ATT {
		if tr.Status != TxnCommitted {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
