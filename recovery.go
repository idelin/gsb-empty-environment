package aries

import "sort"

// RecoveryReport summarizes one ARIES recovery pass.
type RecoveryReport struct {
	// Analysis phase.
	TxnTable map[TxID]TxnStatus // transaction table after analysis
	DPT      map[PageID]LSN     // dirty page table after analysis
	RedoFrom LSN                // oldest recLSN: where redo started

	// Redo phase.
	Redone []LSN // LSNs of records actually re-applied to pages

	// Undo phase.
	Losers []TxID // transactions rolled back
	CLRs   []LSN  // compensation records written during undo
}

// recover runs the three ARIES phases: analysis, redo, undo.
func (db *DB) recover() *RecoveryReport {
	report := &RecoveryReport{
		TxnTable: make(map[TxID]TxnStatus),
		DPT:      make(map[PageID]LSN),
	}
	records := db.lm.Records()

	// ---------- Phase 1: Analysis ----------
	// Start from the most recent checkpoint (if any) and scan forward,
	// rebuilding the transaction table and the dirty page table.
	start := 0
	for i, rec := range records {
		if rec.Type != RecCheckpoint {
			continue
		}
		start = i
		db.txnTable = make(map[TxID]*TxnInfo)
		db.dpt = make(map[PageID]LSN)
		for id, info := range rec.TxnTable {
			cp := info
			db.txnTable[id] = &cp
		}
		for pid, lsn := range rec.DPT {
			db.dpt[pid] = lsn
		}
	}
	for _, rec := range records[start:] {
		switch rec.Type {
		case RecUpdate, RecCLR:
			info := db.txnInfo(rec.TxID)
			info.LastLSN = rec.LSN
			if _, ok := db.dpt[rec.PageID]; !ok {
				db.dpt[rec.PageID] = rec.LSN
			}
		case RecCommit:
			info := db.txnInfo(rec.TxID)
			info.Status = StatusCommitted
			info.LastLSN = rec.LSN
		case RecAbort:
			info := db.txnInfo(rec.TxID)
			info.Status = StatusAborting
			info.LastLSN = rec.LSN
		case RecEnd:
			delete(db.txnTable, rec.TxID)
		}
	}
	for id, info := range db.txnTable {
		report.TxnTable[id] = info.Status
	}
	for pid, lsn := range db.dpt {
		report.DPT[pid] = lsn
	}

	// ---------- Phase 2: Redo ----------
	// Repeat history from the oldest recLSN in the DPT. A record is
	// re-applied only if its page is dirty per the DPT and the page's
	// on-disk pageLSN is older than the record.
	redoFrom := NilLSN
	for _, recLSN := range db.dpt {
		if redoFrom == NilLSN || recLSN < redoFrom {
			redoFrom = recLSN
		}
	}
	report.RedoFrom = redoFrom
	if redoFrom != NilLSN {
		for _, rec := range records {
			if rec.LSN < redoFrom {
				continue
			}
			if rec.Type != RecUpdate && rec.Type != RecCLR {
				continue
			}
			recLSN, ok := db.dpt[rec.PageID]
			if !ok || recLSN > rec.LSN {
				continue // page was flushed before this record
			}
			page, err := db.bp.Fetch(rec.PageID)
			if err != nil {
				continue
			}
			if page.LSN() >= rec.LSN {
				continue // already applied
			}
			copy(page.Data()[rec.Offset:], rec.After)
			page.SetLSN(rec.LSN)
			page.dirty = true
			report.Redone = append(report.Redone, rec.LSN)
		}
	}

	// ---------- Phase 3: Undo ----------
	// Roll back every loser transaction (running or aborting at crash
	// time) by walking its log chain backwards, writing a CLR for each
	// undone update.
	var losers []TxID
	for id, info := range db.txnTable {
		if info.Status == StatusRunning || info.Status == StatusAborting {
			losers = append(losers, id)
		}
	}
	sort.Slice(losers, func(i, j int) bool { return losers[i] < losers[j] })
	for _, id := range losers {
		report.CLRs = append(report.CLRs, db.undo(id)...)
		report.Losers = append(report.Losers, id)
		db.lm.Append(&LogRecord{Type: RecEnd, TxID: id, PrevLSN: db.txnTable[id].LastLSN})
		delete(db.txnTable, id)
	}
	// Transactions that committed but crashed before their END record
	// was written are finished here.
	for id, info := range db.txnTable {
		if info.Status == StatusCommitted {
			db.lm.Append(&LogRecord{Type: RecEnd, TxID: id, PrevLSN: info.LastLSN})
			delete(db.txnTable, id)
		}
	}
	db.lm.Sync()
	return report
}

func (db *DB) txnInfo(id TxID) *TxnInfo {
	info, ok := db.txnTable[id]
	if !ok {
		info = &TxnInfo{Status: StatusRunning, LastLSN: NilLSN}
		db.txnTable[id] = info
	}
	return info
}

// undo rolls back one transaction by following its prevLSN/undoNextLSN
// chain to the beginning, writing a CLR for every update it undoes.
// Shared by recovery (undo phase) and Txn.Abort.
func (db *DB) undo(id TxID) []LSN {
	var clrs []LSN
	for {
		db.mu.Lock()
		info, ok := db.txnTable[id]
		db.mu.Unlock()
		if !ok || info.LastLSN == NilLSN {
			return clrs
		}
		rec := db.lm.Get(info.LastLSN)
		if rec == nil {
			return clrs
		}
		switch rec.Type {
		case RecUpdate:
			page, err := db.bp.Fetch(rec.PageID)
			if err != nil {
				return clrs
			}
			clr := &LogRecord{
				Type:        RecCLR,
				TxID:        id,
				PrevLSN:     rec.LSN,
				PageID:      rec.PageID,
				Offset:      rec.Offset,
				After:       rec.Before, // CLR redoes the undo
				UndoNextLSN: rec.PrevLSN,
			}
			clrLSN := db.lm.Append(clr)
			copy(page.Data()[rec.Offset:], rec.Before)
			page.SetLSN(clrLSN)
			page.dirty = true
			db.mu.Lock()
			db.txnTable[id].LastLSN = clrLSN
			if _, ok := db.dpt[rec.PageID]; !ok {
				db.dpt[rec.PageID] = clrLSN
			}
			db.mu.Unlock()
			clrs = append(clrs, clrLSN)
		case RecCLR:
			db.mu.Lock()
			db.txnTable[id].LastLSN = rec.UndoNextLSN
			db.mu.Unlock()
		default:
			// ABORT (or any non-update record): keep walking back.
			db.mu.Lock()
			db.txnTable[id].LastLSN = rec.PrevLSN
			db.mu.Unlock()
		}
	}
}
