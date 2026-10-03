// Package recovery implements the classic ARIES crash-recovery
// protocol: an Analysis pass that reconstructs the DirtyPageTable and
// TransactionTable from the WAL, a Redo pass that repeats history,
// and an Undo pass that rolls back loser transactions writing
// compensation log records (CLRs).
package recovery

import (
	"container/heap"
	"fmt"
	"io"
	"sort"

	"aries/buffer"
	"aries/metrics"
	"aries/wal"
)

// Metric names recorded by the engine.
const (
	MAnalysisDuration = "aries.analysis.duration"
	MRedoDuration     = "aries.redo.duration"
	MUndoDuration     = "aries.undo.duration"
	MAnalysisRecords  = "aries.analysis.records"
	MRedoApplied      = "aries.redo.applied"
	MRedoSkipped      = "aries.redo.skipped"
	MUndoCLR          = "aries.undo.clr"
	MUndoEnd          = "aries.undo.end"
	MUndoRecords      = "aries.undo.records"
)

// TxnEntry is one row of the transaction table.
type TxnEntry struct {
	Status  wal.TxnStatus
	LastLSN wal.LSN
}

// Report summarises a completed recovery run.
type Report struct {
	Losers  []uint64
	Winners []uint64
	Redone  int
	Undone  int
	CLRs    int
}

// Engine performs ARIES recovery over a WAL file and a buffer pool.
type Engine struct {
	walPath string
	pool    *buffer.Pool
	reg     *metrics.Registry

	// TxnTable and DPT are the reconstructed analysis-phase state.
	TxnTable map[uint64]*TxnEntry
	DPT      map[uint64]wal.LSN

	records map[wal.LSN]*wal.LogRecord
}

// NewEngine creates an engine recovering walPath into pool, recording
// fine-grained metrics into reg.
func NewEngine(walPath string, pool *buffer.Pool, reg *metrics.Registry) *Engine {
	return &Engine{
		walPath:  walPath,
		pool:     pool,
		reg:      reg,
		TxnTable: make(map[uint64]*TxnEntry),
		DPT:      make(map[uint64]wal.LSN),
		records:  make(map[wal.LSN]*wal.LogRecord),
	}
}

// Recover runs the three ARIES phases in order and flushes all dirty
// pages to stable storage on success.
func (e *Engine) Recover() (*Report, error) {
	if err := e.Analysis(); err != nil {
		return nil, fmt.Errorf("analysis: %w", err)
	}
	redone, err := e.Redo()
	if err != nil {
		return nil, fmt.Errorf("redo: %w", err)
	}
	rep, err := e.Undo()
	if err != nil {
		return nil, fmt.Errorf("undo: %w", err)
	}
	rep.Redone = redone
	e.pool.FlushAll()
	return rep, nil
}

// Analysis scans the WAL from the last checkpoint (or the beginning)
// and reconstructs the transaction table and dirty page table.
func (e *Engine) Analysis() error {
	end := e.reg.Timer(MAnalysisDuration)
	defer end()

	r, err := wal.NewReader(e.walPath)
	if err != nil {
		e.reg.RecordError(metrics.ErrIO)
		return err
	}
	defer r.Close()

	var ckpt *wal.LogRecord
	for {
		rec, err := r.Next()
		if err == io.EOF {
			break
		}
		if err == wal.ErrCorrupt {
			e.reg.RecordError(metrics.ErrWALCorrupt)
			break // torn tail write: stop scanning
		}
		if err != nil {
			return err
		}
		e.records[rec.LSN] = rec
		if rec.Type == wal.TypeCheckpoint {
			ckpt = rec
		}
	}

	start := wal.LSN(0)
	if ckpt != nil {
		start = ckpt.LSN
		for page, lsn := range ckpt.DPT {
			e.DPT[page] = lsn
		}
		for txn, lsn := range ckpt.Txns {
			e.TxnTable[txn] = &TxnEntry{Status: wal.StatusRunning, LastLSN: lsn}
		}
	}

	scanned := e.reg.Counter(MAnalysisRecords)
	for lsn := start + 1; ; lsn++ {
		rec, ok := e.records[lsn]
		if !ok {
			break
		}
		scanned.Inc()
		switch rec.Type {
		case wal.TypeUpdate, wal.TypeCLR:
			e.TxnTable[rec.TxnID] = upsertTxn(e.TxnTable, rec)
			if _, ok := e.DPT[rec.PageID]; !ok {
				e.DPT[rec.PageID] = rec.LSN
			}
		case wal.TypeCommit:
			t := upsertTxn(e.TxnTable, rec)
			t.Status = wal.StatusCommitted
			e.TxnTable[rec.TxnID] = t
		case wal.TypeAbort:
			t := upsertTxn(e.TxnTable, rec)
			t.Status = wal.StatusAborting
			e.TxnTable[rec.TxnID] = t
		case wal.TypeEnd:
			delete(e.TxnTable, rec.TxnID)
		}
	}
	return nil
}

func upsertTxn(table map[uint64]*TxnEntry, rec *wal.LogRecord) *TxnEntry {
	if t, ok := table[rec.TxnID]; ok {
		t.LastLSN = rec.LSN
		return t
	}
	return &TxnEntry{Status: wal.StatusRunning, LastLSN: rec.LSN}
}

// Redo repeats history from the smallest recLSN in the DPT: every
// update or CLR whose effects are not yet on the page is reapplied.
func (e *Engine) Redo() (int, error) {
	end := e.reg.Timer(MRedoDuration)
	defer end()

	applied := e.reg.Counter(MRedoApplied)
	skipped := e.reg.Counter(MRedoSkipped)

	if len(e.DPT) == 0 {
		return 0, nil
	}
	start := wal.LSN(0)
	first := true
	for _, recLSN := range e.DPT {
		if first || recLSN < start {
			start = recLSN
			first = false
		}
	}

	count := 0
	for lsn := start; ; lsn++ {
		rec, ok := e.records[lsn]
		if !ok {
			break
		}
		if rec.Type != wal.TypeUpdate && rec.Type != wal.TypeCLR {
			continue
		}
		recLSN, dirty := e.DPT[rec.PageID]
		if !dirty || rec.LSN < recLSN {
			skipped.Inc()
			continue
		}
		page := e.pool.Fetch(rec.PageID)
		if page.LSN >= rec.LSN {
			skipped.Inc()
			continue
		}
		page.Data = append([]byte(nil), rec.After...)
		page.LSN = rec.LSN
		page.Dirty = true
		applied.Inc()
		count++
	}
	return count, nil
}

// undoItem is a to-be-undone log record referenced by LSN.
type undoItem struct {
	lsn wal.LSN
	txn uint64
}

// undoHeap is a max-heap of undoItems ordered by LSN.
type undoHeap []undoItem

func (h undoHeap) Len() int            { return len(h) }
func (h undoHeap) Less(i, j int) bool  { return h[i].lsn > h[j].lsn }
func (h undoHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *undoHeap) Push(x interface{}) { *h = append(*h, x.(undoItem)) }
func (h *undoHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// Undo rolls back every loser transaction in reverse LSN order,
// writing a CLR per undone update and an END record per finished
// transaction. Committed transactions missing their END record
// receive one.
func (e *Engine) Undo() (*Report, error) {
	end := e.reg.Timer(MUndoDuration)
	defer end()

	clrCount := e.reg.Counter(MUndoCLR)
	endCount := e.reg.Counter(MUndoEnd)
	undoCount := e.reg.Counter(MUndoRecords)

	w, err := wal.OpenAppender(e.walPath)
	if err != nil {
		e.reg.RecordError(metrics.ErrIO)
		return nil, err
	}
	defer w.Close()

	rep := &Report{}
	h := &undoHeap{}
	txns := make([]uint64, 0, len(e.TxnTable))
	for txn := range e.TxnTable {
		txns = append(txns, txn)
	}
	sort.Slice(txns, func(i, j int) bool { return txns[i] < txns[j] })
	for _, txn := range txns {
		t := e.TxnTable[txn]
		if t.Status == wal.StatusCommitted {
			rep.Winners = append(rep.Winners, txn)
			e.appendEnd(w, txn, t)
			endCount.Inc()
			continue
		}
		rep.Losers = append(rep.Losers, txn)
		heap.Push(h, undoItem{lsn: t.LastLSN, txn: txn})
	}

	for h.Len() > 0 {
		it := heap.Pop(h).(undoItem)
		rec, ok := e.records[it.lsn]
		if !ok {
			e.reg.RecordError(metrics.ErrTxnUnknown)
			return nil, fmt.Errorf("undo: missing record at LSN %d", it.lsn)
		}
		undoCount.Inc()
		var next wal.LSN
		switch rec.Type {
		case wal.TypeUpdate:
			page := e.pool.Fetch(rec.PageID)
			page.Data = append([]byte(nil), rec.Before...)
			t := e.TxnTable[rec.TxnID]
			clr := &wal.LogRecord{
				PrevLSN:  t.LastLSN,
				TxnID:    rec.TxnID,
				Type:     wal.TypeCLR,
				PageID:   rec.PageID,
				After:    rec.Before,
				UndoNext: rec.PrevLSN,
			}
			clrLSN, err := w.Append(clr)
			if err != nil {
				e.reg.RecordError(metrics.ErrIO)
				return nil, err
			}
			e.records[clrLSN] = clr
			page.LSN = clrLSN
			page.Dirty = true
			t.LastLSN = clrLSN
			clrCount.Inc()
			rep.Undone++
			rep.CLRs++
			next = rec.PrevLSN
		case wal.TypeCLR:
			next = rec.UndoNext
		default:
			next = rec.PrevLSN
		}
		if next != 0 {
			heap.Push(h, undoItem{lsn: next, txn: it.txn})
		} else {
			t := e.TxnTable[it.txn]
			e.appendEnd(w, it.txn, t)
			endCount.Inc()
			delete(e.TxnTable, it.txn)
		}
	}
	if err := w.Sync(); err != nil {
		e.reg.RecordError(metrics.ErrIO)
		return nil, err
	}
	return rep, nil
}

func (e *Engine) appendEnd(w *wal.Writer, txn uint64, t *TxnEntry) {
	rec := &wal.LogRecord{PrevLSN: t.LastLSN, TxnID: txn, Type: wal.TypeEnd}
	lsn, err := w.Append(rec)
	if err != nil {
		e.reg.RecordError(metrics.ErrIO)
		return
	}
	e.records[lsn] = rec
	t.LastLSN = lsn
}
