package aries

import (
	"container/heap"
	"fmt"
	"sort"
)

// Page is a database page. PageLSN is the LSN of the most recent log
// record whose update is reflected in Data.
type Page struct {
	ID      uint64
	PageLSN uint64
	Data    []byte
}

// Storage is a page-addressable store (disk or buffer pool).
type Storage interface {
	ReadPage(id uint64) (*Page, bool)
	WritePage(p *Page)
}

// MapStorage is a simple in-memory Storage implementation.
type MapStorage struct {
	Pages map[uint64]*Page
}

// NewMapStorage creates an empty in-memory page store.
func NewMapStorage() *MapStorage { return &MapStorage{Pages: map[uint64]*Page{}} }

// ReadPage fetches a page by id.
func (s *MapStorage) ReadPage(id uint64) (*Page, bool) {
	p, ok := s.Pages[id]
	return p, ok
}

// WritePage stores a copy of p.
func (s *MapStorage) WritePage(p *Page) {
	cp := &Page{ID: p.ID, PageLSN: p.PageLSN, Data: append([]byte(nil), p.Data...)}
	s.Pages[p.ID] = cp
}

// TxnStatus is the analysis-phase state of a transaction.
type TxnStatus int

const (
	TxnRunning TxnStatus = iota
	TxnCommitted
	TxnAborting
)

func (s TxnStatus) String() string {
	switch s {
	case TxnRunning:
		return "RUNNING"
	case TxnCommitted:
		return "COMMITTED"
	case TxnAborting:
		return "ABORTING"
	}
	return "UNKNOWN"
}

// TxnEntry is a transaction-table row.
type TxnEntry struct {
	TxnID   uint64
	Status  TxnStatus
	LastLSN uint64
}

// RecoveryResult summarizes one Recover() run.
type RecoveryResult struct {
	TxnTable  map[uint64]*TxnEntry // after analysis (before undo)
	DirtyPage map[uint64]uint64    // pageID -> recLSN, after analysis
	RedoLSN   uint64               // LSN where redo started
	Redone    []uint64             // LSNs actually re-applied
	CLRs      []*LogRecord         // compensation records written by undo
	EndedTxns []uint64             // txns closed with END records
}

// Engine performs ARIES recovery over a WAL against durable storage.
type Engine struct {
	wal  *WAL
	disk Storage
}

// NewEngine builds a recovery engine for the given WAL and storage.
func NewEngine(wal *WAL, disk Storage) *Engine { return &Engine{wal: wal, disk: disk} }

// Recover runs the full ARIES protocol: Analysis, Redo, Undo.
func (e *Engine) Recover() (*RecoveryResult, error) {
	recs, err := e.wal.Scan()
	if err != nil {
		return nil, fmt.Errorf("scan WAL: %w", err)
	}
	byLSN := make(map[uint64]*LogRecord, len(recs))
	for _, r := range recs {
		byLSN[r.LSN] = r
	}

	res := &RecoveryResult{}

	// ---------------- Phase 1: Analysis ----------------
	startIdx := 0
	txnTable := map[uint64]*TxnEntry{}
	dpt := map[uint64]uint64{}
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Type == LogCheckpoint {
			payload, err := DecodeCheckpointPayload(recs[i].After)
			if err != nil {
				return nil, fmt.Errorf("decode checkpoint at LSN %d: %w", recs[i].LSN, err)
			}
			for txnID, lastLSN := range payload.Txns {
				txnTable[txnID] = &TxnEntry{TxnID: txnID, Status: TxnRunning, LastLSN: lastLSN}
			}
			for pageID, recLSN := range payload.DirtyPages {
				dpt[pageID] = recLSN
			}
			startIdx = i + 1
			break
		}
	}
	for _, r := range recs[startIdx:] {
		switch r.Type {
		case LogUpdate, LogCLR:
			if _, ok := dpt[r.PageID]; !ok {
				dpt[r.PageID] = r.LSN
			}
			if t, ok := txnTable[r.TxnID]; ok {
				t.LastLSN = r.LSN
			} else {
				txnTable[r.TxnID] = &TxnEntry{TxnID: r.TxnID, Status: TxnRunning, LastLSN: r.LSN}
			}
		case LogCommit:
			t := txnTable[r.TxnID]
			t.Status = TxnCommitted
			t.LastLSN = r.LSN
		case LogAbort:
			t := txnTable[r.TxnID]
			t.Status = TxnAborting
			t.LastLSN = r.LSN
		case LogEnd:
			delete(txnTable, r.TxnID)
		}
	}
	res.TxnTable = txnTable
	res.DirtyPage = dpt

	// ---------------- Phase 2: Redo (repeat history) ----------------
	redoLSN := uint64(0)
	first := true
	for _, recLSN := range dpt {
		if first || recLSN < redoLSN {
			redoLSN = recLSN
			first = false
		}
	}
	res.RedoLSN = redoLSN
	for _, r := range recs {
		if r.LSN < redoLSN {
			continue
		}
		if r.Type != LogUpdate && r.Type != LogCLR {
			continue
		}
		recLSN, dirty := dpt[r.PageID]
		if !dirty || r.LSN < recLSN {
			continue // page was not dirty when this record was written
		}
		page, ok := e.disk.ReadPage(r.PageID)
		if !ok {
			page = &Page{ID: r.PageID}
		}
		if page.PageLSN >= r.LSN {
			continue // update already on disk
		}
		page.Data = append([]byte(nil), r.After...)
		page.PageLSN = r.LSN
		e.disk.WritePage(page)
		res.Redone = append(res.Redone, r.LSN)
	}

	// ---------------- Phase 3: Undo (losers, reverse scan, CLRs) ----------------
	// Losers: transactions still in the table after analysis.
	pq := &lsnHeap{}
	for _, t := range txnTable {
		if t.Status == TxnCommitted {
			// Committed but no END yet: close it out.
			end := &LogRecord{Type: LogEnd, TxnID: t.TxnID, PrevLSN: t.LastLSN}
			if _, err := e.wal.Append(end); err != nil {
				return nil, err
			}
			res.EndedTxns = append(res.EndedTxns, t.TxnID)
			continue
		}
		heap.Push(pq, lsnItem{lsn: t.LastLSN, txnID: t.TxnID})
	}
	for pq.Len() > 0 {
		item := heap.Pop(pq).(lsnItem)
		r := byLSN[item.lsn]
		if r == nil {
			return nil, fmt.Errorf("undo: missing record LSN %d", item.lsn)
		}
		var next uint64
		switch r.Type {
		case LogCLR:
			next = r.UndoNextLSN
		case LogUpdate:
			clr := &LogRecord{
				Type:        LogCLR,
				TxnID:       r.TxnID,
				PageID:      r.PageID,
				PrevLSN:     txnLastLSN(txnTable, r.TxnID, item.lsn),
				UndoNextLSN: r.PrevLSN,
				Before:      append([]byte(nil), r.After...),
				After:       append([]byte(nil), r.Before...),
			}
			clrLSN, err := e.wal.Append(clr)
			if err != nil {
				return nil, err
			}
			byLSN[clrLSN] = clr
			page, ok := e.disk.ReadPage(r.PageID)
			if !ok {
				page = &Page{ID: r.PageID}
			}
			page.Data = append([]byte(nil), r.Before...)
			page.PageLSN = clrLSN
			e.disk.WritePage(page)
			res.CLRs = append(res.CLRs, clr)
			next = r.PrevLSN
		default: // ABORT and other non-update records: just follow the chain
			next = r.PrevLSN
		}
		if next == 0 {
			end := &LogRecord{Type: LogEnd, TxnID: r.TxnID, PrevLSN: item.lsn}
			if _, err := e.wal.Append(end); err != nil {
				return nil, err
			}
			res.EndedTxns = append(res.EndedTxns, r.TxnID)
		} else {
			heap.Push(pq, lsnItem{lsn: next, txnID: r.TxnID})
		}
	}
	sort.Slice(res.EndedTxns, func(i, j int) bool { return res.EndedTxns[i] < res.EndedTxns[j] })
	return res, nil
}

func txnLastLSN(table map[uint64]*TxnEntry, txnID, fallback uint64) uint64 {
	if t, ok := table[txnID]; ok {
		return t.LastLSN
	}
	return fallback
}

// lsnItem / lsnHeap implement a max-heap of LSNs so undo always
// processes the globally largest outstanding LSN first.
type lsnItem struct {
	lsn   uint64
	txnID uint64
}

type lsnHeap []lsnItem

func (h lsnHeap) Len() int           { return len(h) }
func (h lsnHeap) Less(i, j int) bool { return h[i].lsn > h[j].lsn }
func (h lsnHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *lsnHeap) Push(x any)        { *h = append(*h, x.(lsnItem)) }
func (h *lsnHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
