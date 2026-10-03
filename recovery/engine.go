// Package recovery implements the ARIES crash-recovery protocol:
// Analysis (rebuild transaction & dirty-page tables), Redo (repeat history),
// and Undo (roll back loser transactions, logging CLRs).
package recovery

import (
	"fmt"
	"sort"
	"sync"

	"aries/wal"
)

// Page is a durable page image with the LSN of the last log record applied.
type Page struct {
	LSN  uint64
	Data []byte
}

// page is the internal storage row (tracks whether LSN is meaningful).
type page struct {
	Page
	hasLSN bool
}

// Storage is the durable page store. It is safe for concurrent use.
type Storage struct {
	mu     sync.RWMutex
	pages  map[uint64]*page
	writes int
}

func NewStorage() *Storage {
	return &Storage{pages: make(map[uint64]*page)}
}

// SeedPage installs a page image (used to model pre-crash disk contents)
// whose durable page LSN is lsn. Use SeedCleanPage for pages no log record
// has ever been applied to.
func (s *Storage) SeedPage(id uint64, lsn uint64, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages[id] = &page{Page: Page{LSN: lsn, Data: append([]byte(nil), data...)}, hasLSN: true}
}

// SeedCleanPage installs a page image with no durable LSN: every logged
// update to it must be (re)applied during redo.
func (s *Storage) SeedCleanPage(id uint64, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages[id] = &page{Page: Page{Data: append([]byte(nil), data...)}}
}

// PageLSN returns the durable LSN of a page and whether any log record has
// been applied to it. The boolean disambiguates a real LSN 0 (the first
// record in a log) from "no record applied yet".
func (s *Storage) PageLSN(id uint64) (uint64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.pages[id]; ok && p.hasLSN {
		return p.LSN, true
	}
	return 0, false
}

// Apply writes img at off inside page id and advances its LSN to lsn.
// The page grows as needed; untouched bytes are preserved.
func (s *Storage) Apply(id uint64, off uint16, img []byte, lsn uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pages[id]
	if !ok {
		p = &page{}
		s.pages[id] = p
	}
	end := int(off) + len(img)
	if end > len(p.Data) {
		grown := make([]byte, end)
		copy(grown, p.Data)
		p.Data = grown
	}
	copy(p.Data[off:end], img)
	p.LSN = lsn
	p.hasLSN = true
	s.writes++
}

// ReadPage returns a copy of a page image.
func (s *Storage) ReadPage(id uint64) (Page, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.pages[id]
	if !ok {
		return Page{}, false
	}
	return Page{LSN: p.LSN, Data: append([]byte(nil), p.Data...)}, true
}

// Writes returns how many page applications the storage has performed.
func (s *Storage) Writes() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.writes
}

// TxnEntry is one row of the transaction table rebuilt by Analysis.
type TxnEntry struct {
	TxnID   uint64
	LastLSN uint64
	Status  wal.TxnStatus
}

// Report summarizes a recovery run.
type Report struct {
	Txns       map[uint64]*TxnEntry // transaction table after analysis
	DirtyPages map[uint64]uint64    // dirty page table after analysis (page -> recLSN)
	RedoStart  uint64               // smallest recLSN; 0 if DPT empty
	Redone     int                  // update/CLR records re-applied
	RedoSkip   int                  // records skipped (already durable / not dirty)
	CLRs       int                  // compensation records written during undo
	UndoneTxns []uint64             // loser transactions rolled back
	Ends       int                  // END records written
}

// Engine recovers a Storage from a WAL.
type Engine struct {
	Storage *Storage
}

func NewEngine(s *Storage) *Engine { return &Engine{Storage: s} }

// Recover runs the three ARIES phases over the log exposed by r. Records
// produced during undo (CLRs, ENDs) are appended through w, which must be
// positioned at the end of the valid log (see wal.AppendWriter).
func (e *Engine) Recover(r *wal.Reader, w *wal.Writer) (*Report, error) {
	recs, err := r.ScanValid()
	if err != nil {
		return nil, err
	}
	rep := &Report{}

	att, dpt := e.analysis(recs)
	rep.Txns, rep.DirtyPages = att, dpt

	e.redo(recs, dpt, rep)

	if err := e.undo(recs, att, w, rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// ---------------------------------------------------------------------------
// Phase 1: Analysis — replay the log from the last checkpoint to rebuild the
// transaction table (ATT) and dirty page table (DPT) as of the crash.
// ---------------------------------------------------------------------------
func (e *Engine) analysis(recs []*wal.Record) (map[uint64]*TxnEntry, map[uint64]uint64) {
	att := make(map[uint64]*TxnEntry)
	dpt := make(map[uint64]uint64)

	start := 0
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Type == wal.RecCheckpoint {
			for _, t := range recs[i].Txns {
				att[t.TxnID] = &TxnEntry{TxnID: t.TxnID, LastLSN: t.LastLSN, Status: t.Status}
			}
			for _, p := range recs[i].Pages {
				dpt[p.PageID] = p.RecLSN
			}
			start = i + 1
			break
		}
	}

	entry := func(id uint64) *TxnEntry {
		te, ok := att[id]
		if !ok {
			te = &TxnEntry{TxnID: id, Status: wal.TxnRunning}
			att[id] = te
		}
		return te
	}
	for _, rec := range recs[start:] {
		switch rec.Type {
		case wal.RecUpdate, wal.RecCLR:
			te := entry(rec.TxnID)
			te.LastLSN = rec.LSN
			if _, ok := dpt[rec.PageID]; !ok {
				dpt[rec.PageID] = rec.LSN
			}
		case wal.RecCommit:
			te := entry(rec.TxnID)
			te.LastLSN = rec.LSN
			te.Status = wal.TxnCommitted
		case wal.RecAbort:
			te := entry(rec.TxnID)
			te.LastLSN = rec.LSN
			te.Status = wal.TxnAborting
		case wal.RecEnd:
			delete(att, rec.TxnID)
		}
	}
	return att, dpt
}

// ---------------------------------------------------------------------------
// Phase 2: Redo — repeat history from the smallest recLSN, re-applying every
// logged update/CLR that is not yet reflected on the durable page.
// ---------------------------------------------------------------------------
func (e *Engine) redo(recs []*wal.Record, dpt map[uint64]uint64, rep *Report) {
	var start uint64
	first := true
	for _, recLSN := range dpt {
		if first || recLSN < start {
			start = recLSN
			first = false
		}
	}
	rep.RedoStart = start

	for _, rec := range recs {
		if rec.Type != wal.RecUpdate && rec.Type != wal.RecCLR {
			continue
		}
		if rec.LSN < start {
			rep.RedoSkip++
			continue
		}
		recLSN, dirty := dpt[rec.PageID]
		if !dirty || rec.LSN < recLSN {
			rep.RedoSkip++ // page was clean when this record was written
			continue
		}
		if pageLSN, ok := e.Storage.PageLSN(rec.PageID); ok && pageLSN >= rec.LSN {
			rep.RedoSkip++ // already durable on the page
			continue
		}
		e.Storage.Apply(rec.PageID, rec.Offset, rec.After, rec.LSN)
		rep.Redone++
	}
}

// ---------------------------------------------------------------------------
// Phase 3: Undo — roll back every loser transaction in reverse LSN order,
// writing a CLR per undone update so a crash during undo is idempotent.
// ---------------------------------------------------------------------------
func (e *Engine) undo(recs []*wal.Record, att map[uint64]*TxnEntry, w *wal.Writer, rep *Report) error {
	byLSN := make(map[uint64]*wal.Record, len(recs))
	for _, rec := range recs {
		byLSN[rec.LSN] = rec
	}

	pending := make(map[uint64]uint64) // txnID -> next LSN to process
	lastWritten := make(map[uint64]uint64)
	for id, te := range att {
		if te.Status == wal.TxnCommitted {
			// Winner that crashed before its END record: finish it.
			if _, err := w.Append(&wal.Record{Type: wal.RecEnd, TxnID: id, PrevLSN: te.LastLSN}); err != nil {
				return err
			}
			rep.Ends++
			continue
		}
		pending[id] = te.LastLSN
		lastWritten[id] = te.LastLSN
		rep.UndoneTxns = append(rep.UndoneTxns, id)
	}
	sort.Slice(rep.UndoneTxns, func(i, j int) bool { return rep.UndoneTxns[i] < rep.UndoneTxns[j] })

	for len(pending) > 0 {
		// Pick the globally largest next-LSN (priority queue semantics).
		var txn uint64
		var maxLSN uint64
		found := false
		for id, lsn := range pending {
			if !found || lsn > maxLSN || (lsn == maxLSN && id < txn) {
				txn, maxLSN = id, lsn
				found = true
			}
		}
		rec, ok := byLSN[maxLSN]
		if !ok {
			return fmt.Errorf("recovery: undo references missing record at LSN %d", maxLSN)
		}

		var next uint64
		switch rec.Type {
		case wal.RecUpdate:
			clr := &wal.Record{
				Type:     wal.RecCLR,
				TxnID:    rec.TxnID,
				PrevLSN:  lastWritten[rec.TxnID],
				PageID:   rec.PageID,
				Offset:   rec.Offset,
				UndoNext: rec.PrevLSN,
				Before:   rec.After,
				After:    rec.Before,
			}
			lsn, err := w.Append(clr)
			if err != nil {
				return err
			}
			e.Storage.Apply(rec.PageID, rec.Offset, rec.Before, lsn)
			byLSN[lsn] = clr
			lastWritten[rec.TxnID] = lsn
			rep.CLRs++
			next = rec.PrevLSN
		case wal.RecCLR:
			next = rec.UndoNext // already compensated: skip to undoNext
		case wal.RecAbort:
			next = rec.PrevLSN
		default:
			return fmt.Errorf("recovery: unexpected record type %d in undo chain", rec.Type)
		}

		if next != wal.NilLSN {
			pending[txn] = next
			continue
		}
		delete(pending, txn)
		if _, err := w.Append(&wal.Record{Type: wal.RecEnd, TxnID: txn, PrevLSN: lastWritten[txn]}); err != nil {
			return err
		}
		rep.Ends++
	}
	return w.Sync()
}
