// Package recovery implements the classic ARIES crash-recovery protocol:
// Analysis (rebuild DirtyPageTable + TransactionTable), Redo (repeat
// history) and Undo (rollback losers via compensation log records).
package recovery

import (
	"container/heap"
	"fmt"
	"sort"
	"sync"

	"aries/wal"
)

// Page is a single database page on stable storage.
type Page struct {
	PageLSN wal.LSN
	Data    []byte
}

// PageStore is an in-memory stand-in for the disk page pool. It is safe
// for concurrent use; internally it shards pages to avoid one big lock.
type PageStore struct {
	shards [64]pageShard
}

type pageShard struct {
	mu    sync.RWMutex
	pages map[wal.PageID]*Page
}

func (s *PageStore) shard(id wal.PageID) *pageShard {
	sh := &s.shards[id%uint64(len(s.shards))]
	if sh.pages == nil {
		sh.mu.Lock()
		if sh.pages == nil {
			sh.pages = make(map[wal.PageID]*Page)
		}
		sh.mu.Unlock()
	}
	return sh
}

// NewPageStore creates an empty store.
func NewPageStore() *PageStore { return &PageStore{} }

// Get returns the page, or nil if it does not exist.
func (s *PageStore) Get(id wal.PageID) *Page {
	sh := s.shard(id)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	return sh.pages[id]
}

// Put inserts or replaces a page.
func (s *PageStore) Put(id wal.PageID, p *Page) {
	sh := s.shard(id)
	sh.mu.Lock()
	sh.pages[id] = p
	sh.mu.Unlock()
}

// Apply writes data into the page and bumps its pageLSN.
func (s *PageStore) Apply(id wal.PageID, lsn wal.LSN, data []byte) {
	sh := s.shard(id)
	sh.mu.Lock()
	p := sh.pages[id]
	if p == nil {
		p = &Page{}
		sh.pages[id] = p
	}
	p.Data = append(p.Data[:0], data...)
	p.PageLSN = lsn
	sh.mu.Unlock()
}

// AnalysisResult is the output of the analysis phase.
type AnalysisResult struct {
	TT  wal.TxnTable
	DPT wal.DirtyPageTable
	// RedoFrom is the smallest recLSN in the DPT (0 when the DPT is empty).
	RedoFrom wal.LSN
}

// Engine runs ARIES recovery over a WAL file against a PageStore.
type Engine struct {
	walPath string
	store   *PageStore
	walOut  *wal.WAL // opened lazily by Undo to append CLRs/ENDs
}

// NewEngine creates a recovery engine for the given WAL file and page store.
func NewEngine(walPath string, store *PageStore) *Engine {
	return &Engine{walPath: walPath, store: store}
}

// Recover executes the full ARIES pipeline and returns the analysis
// result plus statistics about the run.
func (e *Engine) Recover() (*AnalysisResult, *Stats, error) {
	records, err := wal.Scan(e.walPath)
	if err != nil {
		return nil, nil, err
	}
	ar := Analyze(records)
	stats := &Stats{}
	if err := e.Redo(records, ar, stats); err != nil {
		return nil, nil, err
	}
	if err := e.Undo(records, ar, stats); err != nil {
		return nil, nil, err
	}
	return ar, stats, nil
}

// Stats reports what recovery did.
type Stats struct {
	RedoneUpdates int
	UndoneUpdates int
	CLRsWritten   int
	EndsWritten   int
}

// Analyze rebuilds the transaction table and dirty page table. If the log
// contains a checkpoint, analysis starts from the most recent one and
// seeds TT/DPT from its snapshot.
func Analyze(records []*wal.Record) *AnalysisResult {
	start := 0
	tt := wal.TxnTable{}
	dpt := wal.DirtyPageTable{}
	for i, r := range records {
		if r.Type == wal.TypeCheckpoint {
			start = i + 1
			tt = wal.TxnTable{}
			dpt = wal.DirtyPageTable{}
			for id, e := range r.ChkTT {
				tt[id] = &wal.TxnEntry{Status: e.Status, LastLSN: e.LastLSN}
			}
			for pid, lsn := range r.ChkDPT {
				dpt[pid] = lsn
			}
		}
	}
	for _, r := range records[start:] {
		switch r.Type {
		case wal.TypeUpdate, wal.TypeCLR:
			entry := tt[r.TxnID]
			if entry == nil {
				entry = &wal.TxnEntry{Status: wal.StatusRunning}
				tt[r.TxnID] = entry
			}
			entry.LastLSN = r.LSN
			if _, ok := dpt[r.PageID]; !ok {
				dpt[r.PageID] = r.LSN // recLSN: first record dirtying the page
			}
		case wal.TypeCommit:
			entry := tt[r.TxnID]
			if entry == nil {
				entry = &wal.TxnEntry{}
				tt[r.TxnID] = entry
			}
			entry.Status = wal.StatusCommitted
			entry.LastLSN = r.LSN
		case wal.TypeAbort:
			entry := tt[r.TxnID]
			if entry == nil {
				entry = &wal.TxnEntry{}
				tt[r.TxnID] = entry
			}
			entry.Status = wal.StatusAborting
			entry.LastLSN = r.LSN
		case wal.TypeEnd:
			delete(tt, r.TxnID)
		}
	}
	ar := &AnalysisResult{TT: tt, DPT: dpt}
	var min wal.LSN
	first := true
	for _, recLSN := range dpt {
		if first || recLSN < min {
			min = recLSN
			first = false
		}
	}
	if !first {
		ar.RedoFrom = min
	}
	return ar
}

// Redo repeats history: every logged update/CLR whose effects may not have
// reached disk is re-applied, starting at the smallest recLSN in the DPT.
func (e *Engine) Redo(records []*wal.Record, ar *AnalysisResult, st *Stats) error {
	for _, r := range records {
		if r.LSN < ar.RedoFrom {
			continue
		}
		if r.Type != wal.TypeUpdate && r.Type != wal.TypeCLR {
			continue
		}
		recLSN, dirty := ar.DPT[r.PageID]
		if !dirty || recLSN > r.LSN {
			continue // page not dirty, or record predates recLSN
		}
		page := e.store.Get(r.PageID)
		if page != nil && page.PageLSN >= r.LSN {
			continue // already on disk
		}
		e.store.Apply(r.PageID, r.LSN, r.After)
		st.RedoneUpdates++
	}
	return nil
}

// lsnHeap is a max-heap of LSNs driving the reverse-order undo scan.
type lsnHeap []wal.LSN

func (h lsnHeap) Len() int           { return len(h) }
func (h lsnHeap) Less(i, j int) bool { return h[i] > h[j] }
func (h lsnHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *lsnHeap) Push(x any)        { *h = append(*h, x.(wal.LSN)) }
func (h *lsnHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

// Undo rolls back every loser transaction (running or aborting at crash
// time) in reverse LSN order, writing a CLR per undone update and an END
// record when a transaction is fully undone.
func (e *Engine) Undo(records []*wal.Record, ar *AnalysisResult, st *Stats) error {
	byLSN := make(map[wal.LSN]*wal.Record, len(records))
	var maxLSN wal.LSN
	for _, r := range records {
		byLSN[r.LSN] = r
		if r.LSN > maxLSN {
			maxLSN = r.LSN
		}
	}
	// Seed the to-undo set with the lastLSN of every loser.
	h := &lsnHeap{}
	for _, entry := range ar.TT {
		// Committed transactions still in the TT (END lost in the crash)
		// are winners; only running/aborting ones are losers.
		if entry.Status == wal.StatusCommitted {
			continue
		}
		heap.Push(h, entry.LastLSN)
	}
	if h.Len() == 0 {
		return nil
	}
	w, err := wal.OpenAppend(e.walPath, 1024, maxLSN+1)
	if err != nil {
		return err
	}

	for h.Len() > 0 {
		lsn := heap.Pop(h).(wal.LSN)
		r := byLSN[lsn]
		if r == nil {
			return fmt.Errorf("recovery: dangling LSN %d in undo chain", lsn)
		}
		switch r.Type {
		case wal.TypeUpdate:
			// Write the compensation record and apply the before-image.
			clr := &wal.Record{
				TxnID:    r.TxnID,
				Type:     wal.TypeCLR,
				PrevLSN:  lsn,
				PageID:   r.PageID,
				UndoNext: r.PrevLSN,
				Before:   r.After,
				After:    r.Before,
			}
			clrLSN := w.Append(clr)
			e.store.Apply(r.PageID, clrLSN, r.Before)
			st.UndoneUpdates++
			st.CLRsWritten++
			if r.PrevLSN == 0 {
				e.writeEnd(w, r.TxnID, clrLSN, st)
			} else {
				heap.Push(h, r.PrevLSN)
			}
		case wal.TypeCLR:
			// Already compensated: follow the undoNext pointer.
			if r.UndoNext == 0 {
				e.writeEnd(w, r.TxnID, lsn, st)
			} else {
				heap.Push(h, r.UndoNext)
			}
		default:
			// ABORT (or any non-update) record: keep walking the chain.
			if r.PrevLSN == 0 {
				e.writeEnd(w, r.TxnID, lsn, st)
			} else {
				heap.Push(h, r.PrevLSN)
			}
		}
	}
	return w.Close()
}

func (e *Engine) writeEnd(w *wal.WAL, txn wal.TxnID, prev wal.LSN, st *Stats) {
	w.Append(&wal.Record{TxnID: txn, Type: wal.TypeEnd, PrevLSN: prev})
	st.EndsWritten++
}

// SortedTxnIDs is a test/helper utility returning TT keys in order.
func SortedTxnIDs(tt wal.TxnTable) []wal.TxnID {
	ids := make([]wal.TxnID, 0, len(tt))
	for id := range tt {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
