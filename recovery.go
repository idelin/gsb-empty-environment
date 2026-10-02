package aries

import (
	"fmt"
	"sort"
)

// RecoveryStats summarizes one recovery run.
type RecoveryStats struct {
	CheckpointLSN int64    // LSN of the checkpoint used, -1 if none
	RedoFromLSN   uint64   // smallest recLSN in the DPT after analysis
	Redone        int      // number of update/CLR records re-applied
	Winners       []uint64 // committed transactions (ended during recovery)
	Losers        []uint64 // incomplete transactions (undone during recovery)
	Undone        int      // number of CLRs written during undo
}

// Recover runs the three ARIES phases:
//
//	Analysis - scan the WAL from the last checkpoint, rebuilding the
//	           transaction table and the dirty page table.
//	Redo     - repeat history from the smallest recLSN, re-applying every
//	           logged update whose effects are not yet on disk (committed
//	           or not), so the database reaches the exact crash-time state.
//	Undo     - reverse-scan the log for loser transactions and roll them
//	           back, writing a CLR for each undone update.
//
// After Recover returns, all pages are flushed and the database is
// consistent: committed transactions are durable, uncommitted ones are
// gone.
func (s *System) Recover() (*RecoveryStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	records := s.lm.All()
	stats := &RecoveryStats{CheckpointLSN: -1}

	// ---------- Phase 1: Analysis ----------
	start := 0
	for i, r := range records {
		if r.Type == LogCheckpoint {
			stats.CheckpointLSN = int64(r.LSN)
			start = i
			for _, te := range r.Txns {
				s.txnTable[te.ID] = &TxnEntry{ID: te.ID, LastLSN: te.LastLSN, Status: te.Status}
			}
			for pageID, recLSN := range r.DPT {
				s.dpt[pageID] = recLSN
			}
		}
	}
	for _, r := range records[start:] {
		switch r.Type {
		case LogBegin:
			s.txnTable[r.TxnID] = &TxnEntry{ID: r.TxnID, LastLSN: r.LSN, Status: StatusRunning}
		case LogUpdate, LogCLR:
			te := s.txnTable[r.TxnID]
			if te == nil {
				te = &TxnEntry{ID: r.TxnID, Status: StatusRunning}
				s.txnTable[r.TxnID] = te
			}
			te.LastLSN = r.LSN
			if _, ok := s.dpt[uint64(r.PageID)]; !ok {
				s.dpt[uint64(r.PageID)] = r.LSN
			}
		case LogCommit, LogAbort:
			te := s.txnTable[r.TxnID]
			if te == nil {
				te = &TxnEntry{ID: r.TxnID}
				s.txnTable[r.TxnID] = te
			}
			te.LastLSN = r.LSN
			te.Status = StatusCommitted
		case LogEnd:
			delete(s.txnTable, r.TxnID)
		}
	}

	// ---------- Phase 2: Redo ----------
	// Start at the smallest recLSN in the DPT; repeat history for every
	// update/CLR whose LSN is newer than the pageLSN on disk.
	redoFrom := uint64(0)
	first := true
	for _, recLSN := range s.dpt {
		if first || recLSN < redoFrom {
			redoFrom = recLSN
			first = false
		}
	}
	stats.RedoFromLSN = redoFrom
	for _, r := range records {
		if r.LSN < redoFrom {
			continue
		}
		if r.Type != LogUpdate && r.Type != LogCLR {
			continue
		}
		pageID := uint64(r.PageID)
		recLSN, ok := s.dpt[pageID]
		if !ok || recLSN > r.LSN {
			continue // page was not dirty when this record was written
		}
		p, err := s.bp.Fetch(pageID)
		if err != nil {
			return nil, err
		}
		if p.PageLSN >= r.LSN {
			continue // already on disk; keeps redo idempotent
		}
		copy(p.Data[r.Offset:], r.NewData)
		p.PageLSN = r.LSN
		p.Dirty = true
		stats.Redone++
	}

	// End transactions that committed but whose END record was lost.
	for id, te := range s.txnTable {
		if te.Status == StatusCommitted {
			if _, err := s.lm.Append(&LogRecord{Type: LogEnd, TxnID: id, PrevLSN: te.LastLSN}); err != nil {
				return nil, err
			}
			delete(s.txnTable, id)
			stats.Winners = append(stats.Winners, id)
		}
	}

	// ---------- Phase 3: Undo ----------
	// Losers: transactions still in the table. Repeatedly pick the loser
	// with the largest lastLSN and roll one record back, following
	// prevLSN/undoNextLSN chains, so all losers retreat together in
	// reverse LSN order.
	losers := s.txnTable
	for id := range losers {
		stats.Losers = append(stats.Losers, id)
	}
	for len(losers) > 0 {
		var victim *TxnEntry
		for _, te := range losers {
			if victim == nil || te.LastLSN > victim.LastLSN {
				victim = te
			}
		}
		r := s.lm.Get(victim.LastLSN)
		if r == nil {
			return nil, fmt.Errorf("aries: undo: missing log record %d", victim.LastLSN)
		}
		switch r.Type {
		case LogUpdate:
			clr := &LogRecord{
				Type:        LogCLR,
				TxnID:       victim.ID,
				PrevLSN:     victim.LastLSN,
				PageID:      r.PageID,
				Offset:      r.Offset,
				NewData:     r.OldData,
				UndoNextLSN: r.PrevLSN,
			}
			clrLSN, err := s.lm.Append(clr)
			if err != nil {
				return nil, err
			}
			p, err := s.bp.Fetch(uint64(r.PageID))
			if err != nil {
				return nil, err
			}
			copy(p.Data[r.Offset:], r.OldData)
			p.PageLSN = clrLSN
			p.Dirty = true
			victim.LastLSN = clrLSN
			stats.Undone++
			if r.PrevLSN == 0 {
				if err := s.endLoser(losers, victim); err != nil {
					return nil, err
				}
			}
		case LogCLR:
			victim.LastLSN = r.UndoNextLSN
			if r.UndoNextLSN == 0 {
				if err := s.endLoser(losers, victim); err != nil {
					return nil, err
				}
			}
		case LogBegin:
			if err := s.endLoser(losers, victim); err != nil {
				return nil, err
			}
		default: // ABORT/COMMIT of a loser: keep walking the chain
			victim.LastLSN = r.PrevLSN
			if r.PrevLSN == 0 {
				if err := s.endLoser(losers, victim); err != nil {
					return nil, err
				}
			}
		}
	}
	sort.Slice(stats.Winners, func(i, j int) bool { return stats.Winners[i] < stats.Winners[j] })
	sort.Slice(stats.Losers, func(i, j int) bool { return stats.Losers[i] < stats.Losers[j] })

	// Make the recovered state durable and reset in-memory tables.
	if _, err := s.bp.FlushAll(); err != nil {
		return nil, err
	}
	s.dpt = make(map[uint64]uint64)
	s.txnTable = make(map[uint64]*TxnEntry)
	return stats, nil
}

// endLoser writes the final END record for a fully undone transaction.
// Callers must hold s.mu.
func (s *System) endLoser(losers map[uint64]*TxnEntry, te *TxnEntry) error {
	if _, err := s.lm.Append(&LogRecord{Type: LogEnd, TxnID: te.ID, PrevLSN: te.LastLSN}); err != nil {
		return err
	}
	delete(losers, te.ID)
	return nil
}
