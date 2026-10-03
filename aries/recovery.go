// Package aries implements the classic ARIES crash-recovery protocol:
// an Analysis pass that rebuilds the dirty page table and transaction
// table from the last checkpoint, a Redo pass that repeats history, and
// an Undo pass that rolls back loser transactions writing CLRs.
package aries

import (
	"container/heap"
	"fmt"
	"sync"

	"ariesdb/wal"
)

// Page is a single database page on stable storage.
type Page struct {
	LSN  wal.LSN
	Data []byte
}

// Storage is a page-oriented stable store. Safe for concurrent use.
type Storage struct {
	mu    sync.Mutex
	pages map[uint64]*Page
}

// NewStorage returns an empty page store.
func NewStorage() *Storage {
	return &Storage{pages: make(map[uint64]*Page)}
}

// Get returns the page with the given id, or a zero-LSN empty page.
func (s *Storage) Get(id uint64) Page {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.pages[id]; ok {
		return Page{LSN: p.LSN, Data: append([]byte(nil), p.Data...)}
	}
	return Page{}
}

// Apply installs data on a page and stamps it with lsn.
func (s *Storage) Apply(id uint64, lsn wal.LSN, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages[id] = &Page{LSN: lsn, Data: append([]byte(nil), data...)}
}

// TxnEntry is one row of the recovery transaction table.
type TxnEntry struct {
	TxnID   uint64
	Status  wal.TxnStatus
	LastLSN wal.LSN
}

// Recovery performs ARIES recovery of a Storage from a WAL file.
type Recovery struct {
	walPath string
	store   *Storage

	records []*wal.Record
	byLSN   map[wal.LSN]*wal.Record

	// TxnTable and DPT are the products of the Analysis pass.
	TxnTable map[uint64]*TxnEntry
	DPT      map[uint64]wal.LSN

	// Appended holds the CLR and END records written during Undo.
	Appended []*wal.Record
}

// NewRecovery prepares recovery of store from the WAL at walPath.
func NewRecovery(walPath string, store *Storage) *Recovery {
	return &Recovery{walPath: walPath, store: store}
}

// Recover runs the full Analysis, Redo and Undo pipeline.
func (r *Recovery) Recover() error {
	if err := r.Analysis(); err != nil {
		return err
	}
	if err := r.Redo(); err != nil {
		return err
	}
	return r.Undo()
}

// Analysis scans the log from the last checkpoint forward, rebuilding the
// transaction table and the dirty page table.
func (r *Recovery) Analysis() error {
	recs, err := wal.ReadAll(r.walPath)
	if err != nil {
		return err
	}
	r.records = recs
	r.byLSN = make(map[wal.LSN]*wal.Record, len(recs))
	for _, rec := range recs {
		r.byLSN[rec.LSN] = rec
	}
	r.TxnTable = make(map[uint64]*TxnEntry)
	r.DPT = make(map[uint64]wal.LSN)

	start := 0
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Type == wal.TypeCheckpoint {
			if cp := recs[i].Checkpoint; cp != nil {
				for txnID, snap := range cp.Txns {
					r.TxnTable[txnID] = &TxnEntry{TxnID: txnID, Status: snap.Status, LastLSN: snap.LastLSN}
				}
				for pageID, recLSN := range cp.DirtyPages {
					r.DPT[pageID] = recLSN
				}
			}
			start = i + 1
			break
		}
	}

	for _, rec := range recs[start:] {
		switch rec.Type {
		case wal.TypeUpdate, wal.TypeCLR:
			r.touchTxn(rec.TxnID).LastLSN = rec.LSN
			if _, ok := r.DPT[rec.PageID]; !ok {
				r.DPT[rec.PageID] = rec.LSN
			}
		case wal.TypeCommit:
			e := r.touchTxn(rec.TxnID)
			e.Status = wal.TxnCommitted
			e.LastLSN = rec.LSN
		case wal.TypeAbort:
			e := r.touchTxn(rec.TxnID)
			e.Status = wal.TxnAborting
			e.LastLSN = rec.LSN
		case wal.TypeEnd:
			delete(r.TxnTable, rec.TxnID)
		}
	}
	return nil
}

func (r *Recovery) touchTxn(txnID uint64) *TxnEntry {
	e, ok := r.TxnTable[txnID]
	if !ok {
		e = &TxnEntry{TxnID: txnID, Status: wal.TxnRunning}
		r.TxnTable[txnID] = e
	}
	return e
}

// Redo repeats history from the smallest recLSN in the DPT: every logged
// update whose LSN is newer than the page's on-disk LSN is reapplied.
func (r *Recovery) Redo() error {
	if r.TxnTable == nil {
		if err := r.Analysis(); err != nil {
			return err
		}
	}
	var start wal.LSN
	first := true
	for _, recLSN := range r.DPT {
		if first || recLSN < start {
			start = recLSN
			first = false
		}
	}
	for _, rec := range r.records {
		if rec.LSN < start {
			continue
		}
		if rec.Type != wal.TypeUpdate && rec.Type != wal.TypeCLR {
			continue
		}
		recLSN, dirty := r.DPT[rec.PageID]
		if !dirty || rec.LSN < recLSN {
			continue
		}
		page := r.store.Get(rec.PageID)
		if page.LSN < rec.LSN {
			r.store.Apply(rec.PageID, rec.LSN, rec.After)
		}
	}
	return nil
}

// Undo rolls back every loser transaction in reverse LSN order, writing a
// CLR for each undone update and an END record when a transaction is fully
// rolled back.
func (r *Recovery) Undo() error {
	if r.TxnTable == nil {
		if err := r.Analysis(); err != nil {
			return err
		}
		if err := r.Redo(); err != nil {
			return err
		}
	}
	w, err := wal.Open(r.walPath)
	if err != nil {
		return err
	}
	defer w.Close()

	todo := &lsnHeap{}
	for _, e := range r.TxnTable {
		if e.Status == wal.TxnCommitted {
			// Committed transactions need no further work: their
			// commit records are already durable in the log.
			delete(r.TxnTable, e.TxnID)
			continue
		}
		heap.Push(todo, lsnItem{lsn: e.LastLSN, txnID: e.TxnID})
	}
	for todo.Len() > 0 {
		item := heap.Pop(todo).(lsnItem)
		rec, ok := r.byLSN[item.lsn]
		if !ok {
			return fmt.Errorf("aries: missing record for LSN %d", item.lsn)
		}
		entry := r.TxnTable[item.txnID]
		var next wal.LSN
		switch rec.Type {
		case wal.TypeCLR:
			next = rec.UndoNextLSN
		case wal.TypeUpdate:
			clr := &wal.Record{
				Type:        wal.TypeCLR,
				TxnID:       rec.TxnID,
				PrevLSN:     entry.LastLSN,
				PageID:      rec.PageID,
				UndoNextLSN: rec.PrevLSN,
				Before:      rec.After,
				After:       rec.Before,
			}
			clrLSN, err := w.Append(clr)
			if err != nil {
				return err
			}
			r.store.Apply(rec.PageID, clrLSN, rec.Before)
			r.byLSN[clrLSN] = clr
			r.Appended = append(r.Appended, clr)
			entry.LastLSN = clrLSN
			next = rec.PrevLSN
		default:
			return fmt.Errorf("aries: unexpected record type %s during undo", rec.Type)
		}
		if next == 0 {
			end := &wal.Record{Type: wal.TypeEnd, TxnID: item.txnID, PrevLSN: entry.LastLSN}
			endLSN, err := w.Append(end)
			if err != nil {
				return err
			}
			end.LSN = endLSN
			r.Appended = append(r.Appended, end)
			delete(r.TxnTable, item.txnID)
		} else {
			heap.Push(todo, lsnItem{lsn: next, txnID: item.txnID})
		}
	}
	return w.Sync()
}

type lsnItem struct {
	lsn   wal.LSN
	txnID uint64
}

type lsnHeap []lsnItem

func (h lsnHeap) Len() int { return len(h) }

func (h lsnHeap) Less(i, j int) bool {
	if h[i].lsn != h[j].lsn {
		return h[i].lsn > h[j].lsn
	}
	return h[i].txnID > h[j].txnID
}

func (h lsnHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *lsnHeap) Push(x interface{}) { *h = append(*h, x.(lsnItem)) }

func (h *lsnHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
