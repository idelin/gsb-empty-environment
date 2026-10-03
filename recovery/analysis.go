// Package recovery implements the three ARIES restart phases:
// analysis, redo (repeating history) and undo (compensating losers
// via CLRs), plus a transaction-processing facade used to build
// workloads ahead of a crash.
package recovery

import (
	"fmt"

	"aries/wal"
)

// TxnStatus is the transaction state tracked by the ATT.
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
	default:
		return "UNKNOWN"
	}
}

// TxnEntry is one row of the transaction table.
type TxnEntry struct {
	TxnID   int
	Status  TxnStatus
	LastLSN uint64
}

// Analysis is the result of the ARIES analysis phase: the rebuilt
// transaction table (ATT) and dirty page table (DPT), plus the set of
// transactions observed to commit anywhere in the scanned suffix.
type Analysis struct {
	ATT           map[int]*TxnEntry
	DPT           map[int]uint64
	Committed     map[int]bool
	CheckpointLSN uint64
}

func (a *Analysis) entry(txnID int) *TxnEntry {
	e, ok := a.ATT[txnID]
	if !ok {
		e = &TxnEntry{TxnID: txnID, Status: StatusRunning}
		a.ATT[txnID] = e
	}
	return e
}

// Losers returns the IDs of transactions that must be rolled back
// (running or aborting when the crash hit).
func (a *Analysis) Losers() []int {
	var out []int
	for id, e := range a.ATT {
		if e.Status != StatusCommitted {
			out = append(out, id)
		}
	}
	return out
}

// Analyze rebuilds the ATT and DPT starting from the checkpoint at
// checkpointLSN (pass 0 to scan the whole log).
func Analyze(log *wal.LogManager, checkpointLSN uint64) (*Analysis, error) {
	a := &Analysis{
		ATT:           make(map[int]*TxnEntry),
		DPT:           make(map[int]uint64),
		Committed:     make(map[int]bool),
		CheckpointLSN: checkpointLSN,
	}
	if checkpointLSN > 0 {
		rec, err := log.Read(checkpointLSN)
		if err != nil {
			return nil, fmt.Errorf("analysis: read checkpoint: %w", err)
		}
		if rec.Type != wal.TypeCheckpoint {
			return nil, fmt.Errorf("analysis: LSN %d is a %s, not a checkpoint", checkpointLSN, rec.Type)
		}
		for txn, last := range rec.ATT {
			a.ATT[txn] = &TxnEntry{TxnID: txn, Status: StatusRunning, LastLSN: last}
		}
		for page, recLSN := range rec.DPT {
			a.DPT[page] = recLSN
		}
	}
	err := log.ScanFrom(checkpointLSN, func(rec *wal.Record) error {
		if rec.LSN == checkpointLSN {
			return nil
		}
		switch rec.Type {
		case wal.TypeBegin:
			a.ATT[rec.TxnID] = &TxnEntry{TxnID: rec.TxnID, Status: StatusRunning, LastLSN: rec.LSN}
		case wal.TypeUpdate, wal.TypeCLR:
			a.entry(rec.TxnID).LastLSN = rec.LSN
			if _, ok := a.DPT[rec.PageID]; !ok {
				a.DPT[rec.PageID] = rec.LSN
			}
		case wal.TypeCommit:
			e := a.entry(rec.TxnID)
			e.Status = StatusCommitted
			e.LastLSN = rec.LSN
			a.Committed[rec.TxnID] = true
		case wal.TypeAbort:
			e := a.entry(rec.TxnID)
			e.Status = StatusAborting
			e.LastLSN = rec.LSN
		case wal.TypeEnd:
			delete(a.ATT, rec.TxnID)
		case wal.TypeCheckpoint:
			for txn, last := range rec.ATT {
				if _, ok := a.ATT[txn]; !ok {
					a.ATT[txn] = &TxnEntry{TxnID: txn, Status: StatusRunning, LastLSN: last}
				}
			}
			for page, recLSN := range rec.DPT {
				if _, ok := a.DPT[page]; !ok {
					a.DPT[page] = recLSN
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}
