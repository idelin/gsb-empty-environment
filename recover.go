package waldb

import "sort"

// recover runs the three ARIES phases: analysis (rebuild transaction
// and dirty-page tables from the last usable checkpoint), redo
// (repeat history, driven by pageLSN), and undo (roll back losers in
// reverse LSN order, writing CLRs). It is idempotent and can itself
// be interrupted at any point.
func (db *DB) recover() {
	recs := make([]*LogRecord, 0, len(db.lm.records))
	for _, r := range db.lm.records {
		recs = append(recs, r)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].LSN < recs[j].LSN })

	// ---- analysis ----
	// Start from the most recent complete checkpoint; a torn
	// checkpoint never made it into the record index, so the previous
	// one (or the empty state) remains a usable starting point.
	txnTable := map[uint64]*TxnEntry{}
	dpt := map[uint32]uint64{}
	start := 0
	for i, r := range recs {
		if r.Type == RecCheckpoint && r.Checkpoint != nil {
			start = i + 1
			txnTable = map[uint64]*TxnEntry{}
			for id, e := range r.Checkpoint.Txns {
				e := e
				txnTable[id] = &e
			}
			dpt = map[uint32]uint64{}
			for pg, lsn := range r.Checkpoint.DPT {
				dpt[pg] = lsn
			}
		}
	}
	entry := func(txn uint64) *TxnEntry {
		e, ok := txnTable[txn]
		if !ok {
			e = &TxnEntry{Status: StatusRunning}
			txnTable[txn] = e
		}
		return e
	}
	var maxTxn uint64
	for _, r := range recs {
		if r.TxnID > maxTxn {
			maxTxn = r.TxnID
		}
	}
	for _, r := range recs[start:] {
		switch r.Type {
		case RecBegin:
			e := entry(r.TxnID)
			e.Status = StatusRunning
			e.LastLSN = r.LSN
		case RecUpdate, RecCLR:
			entry(r.TxnID).LastLSN = r.LSN
			if _, ok := dpt[r.PageID]; !ok {
				dpt[r.PageID] = r.LSN
			}
		case RecCommit:
			e := entry(r.TxnID)
			e.Status = StatusCommitted
			e.LastLSN = r.LSN
		case RecAbort:
			e := entry(r.TxnID)
			e.Status = StatusAborting
			e.LastLSN = r.LSN
		case RecEnd:
			delete(txnTable, r.TxnID)
		}
	}
	db.inj.hit(PtRecAnalysis)

	// ---- redo: repeat history ----
	// A record is applied only if its page is in the DPT with
	// recLSN <= record LSN and the durable page's pageLSN is older.
	var redoLSN uint64
	first := true
	for _, lsn := range dpt {
		if first || lsn < redoLSN {
			redoLSN = lsn
			first = false
		}
	}
	for _, r := range recs {
		if r.LSN < redoLSN {
			continue
		}
		if r.Type != RecUpdate && r.Type != RecCLR {
			continue
		}
		db.inj.hit(PtRecRedo)
		recLSN, ok := dpt[r.PageID]
		if !ok || recLSN > r.LSN {
			continue
		}
		p, err := db.bp.fix(r.PageID)
		if err != nil {
			continue
		}
		if p.pageLSN >= r.LSN {
			continue
		}
		p.value = r.After
		p.pageLSN = r.LSN
		p.dirty = true
	}

	// ---- undo: roll back losers in reverse LSN order ----
	// Winners (commit record durable, end possibly missing) keep
	// their effects. Losers are undone; every undo writes a CLR with
	// undoNextLSN before touching the page, and CLRs are never undone.
	pending := map[uint64]uint64{} // txnID -> LSN of next record to process
	for id, e := range txnTable {
		if e.Status == StatusCommitted {
			continue
		}
		pending[id] = e.LastLSN
	}
	for len(pending) > 0 {
		var txn, cur uint64
		for id, lsn := range pending {
			if cur == 0 || lsn > cur {
				cur = lsn
				txn = id
			}
		}
		rec := db.lm.records[cur]
		e := txnTable[txn]
		switch rec.Type {
		case RecUpdate:
			clr := &LogRecord{
				Type: RecCLR, TxnID: txn, PrevLSN: e.LastLSN,
				PageID: rec.PageID, After: rec.Before,
				UndoNextLSN: rec.PrevLSN,
			}
			clrLSN := db.lm.append(clr)
			e.LastLSN = clrLSN
			db.inj.hit(PtRecUndo)
			if p, err := db.bp.fix(rec.PageID); err == nil {
				p.value = rec.Before
				p.pageLSN = clrLSN
				p.dirty = true
			}
			db.stepUndo(pending, txn, e, rec.PrevLSN)
		case RecCLR:
			db.stepUndo(pending, txn, e, rec.UndoNextLSN)
		case RecBegin:
			db.stepUndo(pending, txn, e, 0)
		default: // abort record
			pending[txn] = rec.PrevLSN
		}
	}

	db.lm.flushAll()
	db.bp.flushAll()
	db.st.dpt = dpt
	db.nextTxn = maxTxn + 1
	db.inj.hit(PtRecEnd)
}

// stepUndo advances a loser's undo chain; reaching 0 means the
// transaction is fully undone and gets its end record.
func (db *DB) stepUndo(pending map[uint64]uint64, txn uint64, e *TxnEntry, next uint64) {
	if next != 0 {
		pending[txn] = next
		return
	}
	endLSN := db.lm.append(&LogRecord{Type: RecEnd, TxnID: txn, PrevLSN: e.LastLSN})
	e.LastLSN = endLSN
	delete(pending, txn)
}
