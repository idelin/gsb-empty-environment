package aries

import (
	"container/heap"
	"sort"
)

// TxnStatus is the analysis-phase state of a transaction.
type TxnStatus byte

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
	}
	return "UNKNOWN"
}

// TxnEntry is a transaction-table row rebuilt by the analysis phase.
type TxnEntry struct {
	TxnID   uint64
	Status  TxnStatus
	LastLSN uint64
}

// Recovery executes the three ARIES phases against a crashed database:
// Analysis rebuilds the transaction table (TT) and dirty page table (DPT),
// Redo repeats history, Undo rolls back loser transactions writing CLRs.
type Recovery struct {
	WAL   *WAL
	Image *WALImage
	Pages *PageStore
	TT    map[uint64]*TxnEntry
	DPT   map[uint32]uint64 // pageID -> recLSN
}

// NewRecovery opens the crashed WAL and page store and prepares recovery.
// The WAL is opened for appending so Undo can write CLRs and END records.
func NewRecovery(walPath, dbPath string) (*Recovery, error) {
	img, err := MapWAL(walPath)
	if err != nil {
		return nil, err
	}
	ps, err := OpenPageStore(dbPath)
	if err != nil {
		return nil, err
	}
	wal, err := OpenWAL(walPath)
	if err != nil {
		return nil, err
	}
	return &Recovery{
		WAL:   wal,
		Image: img,
		Pages: ps,
		TT:    make(map[uint64]*TxnEntry),
		DPT:   make(map[uint32]uint64),
	}, nil
}

// Recover runs the full pipeline: Analysis, Redo, Undo. The caller should
// Close the returned Recovery to flush pages and the log.
func Recover(walPath, dbPath string) (*Recovery, error) {
	r, err := NewRecovery(walPath, dbPath)
	if err != nil {
		return nil, err
	}
	r.Analysis()
	if err := r.Redo(); err != nil {
		return nil, err
	}
	if err := r.Undo(); err != nil {
		return nil, err
	}
	return r, nil
}

// Analysis scans the log from the most recent begin-checkpoint (seeded with
// the TT/DPT snapshot of the matching end-checkpoint) to the end of the log,
// rebuilding the transaction table and dirty page table at crash time.
func (r *Recovery) Analysis() {
	// Locate the most recent end-checkpoint.
	var seed *Record
	r.Image.Scan(func(rec *Record) bool {
		if rec.Type == TypeEndCheckpoint {
			seed = rec
		}
		return true
	})
	start := uint64(FileHeaderSize)
	if seed != nil {
		for _, t := range seed.Txns {
			r.TT[t.TxnID] = &TxnEntry{TxnID: t.TxnID, Status: TxnStatus(t.Status), LastLSN: t.LastLSN}
		}
		for _, d := range seed.DirtyPages {
			r.DPT[d.PageID] = d.RecLSN
		}
		start = seed.BeginLSN
	}
	r.Image.ScanFrom(start, func(rec *Record) bool {
		switch rec.Type {
		case TypeUpdate, TypeCLR:
			e := r.txnEntry(rec.TxnID)
			e.LastLSN = rec.LSN
			if _, ok := r.DPT[rec.PageID]; !ok {
				r.DPT[rec.PageID] = rec.LSN
			}
		case TypeCommit:
			e := r.txnEntry(rec.TxnID)
			e.Status = StatusCommitted
			e.LastLSN = rec.LSN
		case TypeAbort:
			e := r.txnEntry(rec.TxnID)
			e.Status = StatusAborting
			e.LastLSN = rec.LSN
		case TypeEnd:
			delete(r.TT, rec.TxnID)
		}
		return true
	})
}

func (r *Recovery) txnEntry(txnID uint64) *TxnEntry {
	e, ok := r.TT[txnID]
	if !ok {
		e = &TxnEntry{TxnID: txnID, Status: StatusRunning}
		r.TT[txnID] = e
	}
	return e
}

// Losers returns the transactions that must be rolled back (still active at
// crash time), ordered by TxnID for determinism.
func (r *Recovery) Losers() []*TxnEntry {
	var out []*TxnEntry
	for _, e := range r.TT {
		if e.Status != StatusCommitted {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TxnID < out[j].TxnID })
	return out
}

// Winners returns the committed transactions still present in the TT.
func (r *Recovery) Winners() []*TxnEntry {
	var out []*TxnEntry
	for _, e := range r.TT {
		if e.Status == StatusCommitted {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TxnID < out[j].TxnID })
	return out
}

// Redo repeats history: starting from the smallest recLSN in the DPT, every
// UPDATE/CLR whose page is dirty and whose effects are not yet on the page
// (pageLSN < record LSN) is re-applied.
func (r *Recovery) Redo() error {
	start := uint64(FileHeaderSize)
	first := true
	for _, recLSN := range r.DPT {
		if first || recLSN < start {
			start = recLSN
			first = false
		}
	}
	var scanErr error
	r.Image.ScanFrom(start, func(rec *Record) bool {
		if rec.Type != TypeUpdate && rec.Type != TypeCLR {
			return true
		}
		recLSN, ok := r.DPT[rec.PageID]
		if !ok || recLSN > rec.LSN {
			return true // page not dirty at rec.LSN
		}
		if r.Pages.PageLSN(rec.PageID) >= rec.LSN {
			return true // already on disk
		}
		if err := r.Pages.ApplyUpdate(rec.PageID, rec.Offset, rec.NewData, rec.LSN); err != nil {
			scanErr = err
			return false
		}
		return true
	})
	return scanErr
}

// Undo rolls back every loser transaction in reverse LSN order (largest LSN
// first, across all losers), writing a compensation log record (CLR) for
// each undone update and an END record when a transaction is fully undone.
func (r *Recovery) Undo() error {
	h := &lsnMaxHeap{}
	for _, e := range r.TT {
		if e.Status == StatusCommitted {
			continue
		}
		heap.Push(h, e.LastLSN)
	}
	for h.Len() > 0 {
		lsn := heap.Pop(h).(uint64)
		rec, err := r.Image.DecodeAt(lsn)
		if err != nil {
			return err
		}
		switch rec.Type {
		case TypeUpdate:
			// Write the CLR: its redo data is the update's old image, and
			// undoNextLSN skips the record we just compensated.
			clr := &Record{
				Type:        TypeCLR,
				TxnID:       rec.TxnID,
				PrevLSN:     rec.LSN,
				PageID:      rec.PageID,
				Offset:      rec.Offset,
				OldData:     rec.NewData,
				NewData:     rec.OldData,
				UndoNextLSN: rec.PrevLSN,
			}
			clrLSN, err := r.WAL.Append(clr)
			if err != nil {
				return err
			}
			if err := r.Pages.ApplyUpdate(rec.PageID, rec.Offset, rec.OldData, clrLSN); err != nil {
				return err
			}
			if rec.PrevLSN != 0 {
				heap.Push(h, rec.PrevLSN)
			} else if err := r.appendEnd(rec.TxnID, clrLSN); err != nil {
				return err
			}
		case TypeCLR:
			// Already compensated before the crash: never undone again.
			if rec.UndoNextLSN != 0 {
				heap.Push(h, rec.UndoNextLSN)
			} else if err := r.appendEnd(rec.TxnID, rec.LSN); err != nil {
				return err
			}
		case TypeAbort:
			if rec.PrevLSN != 0 {
				heap.Push(h, rec.PrevLSN)
			} else if err := r.appendEnd(rec.TxnID, rec.LSN); err != nil {
				return err
			}
		}
	}
	return r.WAL.Sync()
}

func (r *Recovery) appendEnd(txnID, prevLSN uint64) error {
	_, err := r.WAL.Append(&Record{Type: TypeEnd, TxnID: txnID, PrevLSN: prevLSN})
	return err
}

// Close flushes all pages to disk and closes the WAL, making the recovered
// state durable.
func (r *Recovery) Close() error {
	if err := r.Pages.Flush(); err != nil {
		return err
	}
	if err := r.Pages.Close(); err != nil {
		return err
	}
	return r.WAL.Close()
}

// lsnMaxHeap orders LSNs largest-first for the undo pass.
type lsnMaxHeap []uint64

func (h lsnMaxHeap) Len() int            { return len(h) }
func (h lsnMaxHeap) Less(i, j int) bool  { return h[i] > h[j] }
func (h lsnMaxHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *lsnMaxHeap) Push(x interface{}) { *h = append(*h, x.(uint64)) }
func (h *lsnMaxHeap) Pop() interface{} {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}
