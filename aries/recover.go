package aries

import "sort"

// RecoveryStats reports what the three recovery phases did.
type RecoveryStats struct {
	AnalysisTT  map[TxID]TTEntry // transaction table after analysis
	AnalysisDPT map[PageID]LSN   // dirty page table after analysis
	Losers      []TxID           // transactions rolled back by undo
	Redone      []LSN            // update/CLR records re-applied by redo
	CLRs        []LSN            // compensation records written by undo
}

// Recover runs the ARIES three-phase protocol (analysis, redo, undo)
// against a durable WAL and disk, returning recovery statistics. All
// recovered dirty pages are flushed to disk before it returns.
func Recover(wal *WAL, disk *Disk) *RecoveryStats {
	records := wal.Records()
	stats := &RecoveryStats{}

	tt, dpt := analysis(records)
	stats.AnalysisTT = tt
	stats.AnalysisDPT = dpt

	bp := NewBufferPool(disk)
	stats.Redone = redo(records, dpt, bp)
	stats.Losers, stats.CLRs = undo(wal, records, tt, bp)

	bp.FlushAll()
	_ = wal.Flush()
	return stats
}

// analysis scans the log forward from the last checkpoint (or the
// beginning of the log) and reconstructs the transaction table and the
// dirty page table.
func analysis(records []*LogRecord) (map[TxID]TTEntry, map[PageID]LSN) {
	tt := make(map[TxID]TTEntry)
	dpt := make(map[PageID]LSN)

	start := 0
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if rec.Type != TypeEndCheckpoint {
			continue
		}
		for tx, e := range rec.TT {
			tt[tx] = e
		}
		for p, lsn := range rec.DPT {
			dpt[p] = lsn
		}
		for j := 0; j < len(records); j++ {
			if records[j].LSN == rec.BeginLSN {
				start = j
				break
			}
		}
		break
	}

	for i := start; i < len(records); i++ {
		rec := records[i]
		switch rec.Type {
		case TypeUpdate, TypeCLR:
			if _, ok := dpt[rec.PageID]; !ok {
				dpt[rec.PageID] = rec.LSN
			}
			if rec.TxID != 0 {
				tt[rec.TxID] = TTEntry{Status: StatusRunning, LastLSN: rec.LSN}
			}
		case TypeCommit:
			tt[rec.TxID] = TTEntry{Status: StatusCommitted, LastLSN: rec.LSN}
		case TypeAbort:
			tt[rec.TxID] = TTEntry{Status: StatusAborted, LastLSN: rec.LSN}
		case TypeEnd:
			delete(tt, rec.TxID)
		}
	}
	return tt, dpt
}

// redo repeats history: starting at the smallest recLSN in the DPT, every
// update and CLR whose effects are not yet on disk is re-applied.
func redo(records []*LogRecord, dpt map[PageID]LSN, bp *BufferPool) []LSN {
	if len(dpt) == 0 {
		return nil
	}
	redoFrom := dpt[firstDPTKey(dpt)]
	for _, recLSN := range dpt {
		if recLSN < redoFrom {
			redoFrom = recLSN
		}
	}

	var redone []LSN
	for i := 0; i < len(records); i++ {
		rec := records[i]
		if rec.LSN < redoFrom {
			continue
		}
		if rec.Type != TypeUpdate && rec.Type != TypeCLR {
			continue
		}
		recLSN, dirty := dpt[rec.PageID]
		if !dirty || recLSN > rec.LSN {
			continue
		}
		page := bp.Fetch(rec.PageID)
		if page.PageLSN >= rec.LSN {
			continue
		}
		page.Data = append([]byte(nil), rec.After...)
		page.PageLSN = rec.LSN
		page.Dirty = true
		redone = append(redone, rec.LSN)
	}
	return redone
}

// undo rolls back every loser transaction in reverse LSN order, writing a
// CLR for each undone update and an END record when a transaction is
// fully rolled back.
func undo(wal *WAL, records []*LogRecord, tt map[TxID]TTEntry, bp *BufferPool) ([]TxID, []LSN) {
	toUndo := make(map[TxID]LSN)
	for tx, e := range tt {
		toUndo[tx] = e.LastLSN
	}

	var losers []TxID
	var clrs []LSN
	for len(toUndo) > 0 {
		var maxLSN LSN
		for _, lsn := range toUndo {
			if maxLSN == 0 || lsn > maxLSN {
				maxLSN = lsn
			}
		}
		rec := records[maxLSN-1]

		switch rec.Type {
		case TypeUpdate:
			clr := &LogRecord{
				TxID:     rec.TxID,
				Type:     TypeCLR,
				PrevLSN:  maxLSN,
				PageID:   rec.PageID,
				UndoNext: rec.PrevLSN,
				After:    append([]byte(nil), rec.Before...),
			}
			clrLSN := wal.Append(clr)
			clrs = append(clrs, clrLSN)

			page := bp.Fetch(rec.PageID)
			page.Data = append([]byte(nil), rec.Before...)
			page.PageLSN = clrLSN
			page.Dirty = true

			if rec.PrevLSN == 0 {
				wal.Append(&LogRecord{TxID: rec.TxID, Type: TypeEnd, PrevLSN: clrLSN})
				delete(toUndo, rec.TxID)
				losers = append(losers, rec.TxID)
			} else {
				toUndo[rec.TxID] = rec.PrevLSN
			}
		case TypeCLR:
			if rec.UndoNext == 0 {
				wal.Append(&LogRecord{TxID: rec.TxID, Type: TypeEnd, PrevLSN: maxLSN})
				delete(toUndo, rec.TxID)
				losers = append(losers, rec.TxID)
			} else {
				toUndo[rec.TxID] = rec.UndoNext
			}
		case TypeCommit, TypeAbort:
			// Already finished at the log level; just close it out.
			wal.Append(&LogRecord{TxID: rec.TxID, Type: TypeEnd, PrevLSN: maxLSN})
			delete(toUndo, rec.TxID)
		default:
			delete(toUndo, rec.TxID)
		}
	}

	sort.Slice(losers, func(i, j int) bool { return losers[i] < losers[j] })
	return losers, clrs
}

func firstDPTKey(dpt map[PageID]LSN) PageID {
	for p := range dpt {
		return p
	}
	return 0
}
