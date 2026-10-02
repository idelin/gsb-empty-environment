package recovery

import (
	"container/heap"
	"encoding/binary"
	"sort"

	"aries/wal"
)

// encodeCheckpoint serializes the TransactionTable and DirtyPageTable into
// a checkpoint record payload.
func encodeCheckpoint(tt []TxnEntry, dpt []DPTEntry) []byte {
	buf := make([]byte, 8+len(tt)*24+len(dpt)*16)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(tt)))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(dpt)))
	p := 8
	for _, e := range tt {
		binary.LittleEndian.PutUint64(buf[p:p+8], e.TxnID)
		binary.LittleEndian.PutUint64(buf[p+8:p+16], e.LastLSN)
		binary.LittleEndian.PutUint64(buf[p+16:p+24], uint64(e.Status))
		p += 24
	}
	for _, e := range dpt {
		binary.LittleEndian.PutUint64(buf[p:p+8], e.PageID)
		binary.LittleEndian.PutUint64(buf[p+8:p+16], e.RecLSN)
		p += 16
	}
	return buf
}

func decodeCheckpoint(payload []byte) (map[wal.TxnID]*TxnEntry, map[wal.PageID]wal.LSN) {
	tt := make(map[wal.TxnID]*TxnEntry)
	dpt := make(map[wal.PageID]wal.LSN)
	if len(payload) < 8 {
		return tt, dpt
	}
	nTT := int(binary.LittleEndian.Uint32(payload[0:4]))
	nDPT := int(binary.LittleEndian.Uint32(payload[4:8]))
	p := 8
	for i := 0; i < nTT && p+24 <= len(payload); i++ {
		id := binary.LittleEndian.Uint64(payload[p : p+8])
		tt[id] = &TxnEntry{
			TxnID:   id,
			LastLSN: binary.LittleEndian.Uint64(payload[p+8 : p+16]),
			Status:  TxnStatus(binary.LittleEndian.Uint64(payload[p+16 : p+24])),
		}
		p += 24
	}
	for i := 0; i < nDPT && p+16 <= len(payload); i++ {
		id := binary.LittleEndian.Uint64(payload[p : p+8])
		dpt[id] = binary.LittleEndian.Uint64(payload[p+8 : p+16])
		p += 16
	}
	return tt, dpt
}

// Result summarizes a completed restart recovery (test/observability aid).
type Result struct {
	TxTable     map[wal.TxnID]*TxnEntry // state at end of Analysis
	DirtyPages  map[wal.PageID]wal.LSN  // DPT at end of Analysis
	Redone      []wal.LSN               // records re-applied during Redo
	Undone      []wal.LSN               // UPDATE records compensated in Undo
	CLRsWritten []wal.LSN
	Winners     []wal.TxnID
	Losers      []wal.TxnID
}

// Engine performs ARIES restart recovery over a WAL and a fresh buffer pool.
type Engine struct {
	log *wal.WAL
	bp  *BufferPool
}

func NewEngine(log *wal.WAL, bp *BufferPool) *Engine {
	return &Engine{log: log, bp: bp}
}

// Recover runs the full Analysis -> Redo -> Undo restart sequence and
// returns a summary of what was done.
func (e *Engine) Recover() (*Result, error) {
	recs, err := e.log.ScanAll()
	if err != nil {
		return nil, err
	}
	res := &Result{}

	// ---------- Phase 1: Analysis ----------
	start := wal.LSN(0)
	tt := make(map[wal.TxnID]*TxnEntry)
	dpt := make(map[wal.PageID]wal.LSN)
	// Find the most recent checkpoint and seed the tables from it.
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Type == wal.TypeCheckpoint {
			tt, dpt = decodeCheckpoint(recs[i].Payload)
			start = recs[i].LSN
			break
		}
	}
	for _, r := range recs {
		if r.LSN < start {
			continue
		}
		switch r.Type {
		case wal.TypeBegin, wal.TypeUpdate, wal.TypeCLR:
			if _, ok := tt[r.TxnID]; !ok {
				tt[r.TxnID] = &TxnEntry{TxnID: r.TxnID, Status: StatusRunning}
			}
			tt[r.TxnID].LastLSN = r.LSN
		case wal.TypeCommit:
			if e, ok := tt[r.TxnID]; ok {
				e.Status = StatusCommitted
				e.LastLSN = r.LSN
			}
		case wal.TypeAbort:
			if e, ok := tt[r.TxnID]; ok {
				e.Status = StatusAborting
				e.LastLSN = r.LSN
			}
		case wal.TypeEnd:
			delete(tt, r.TxnID)
		}
		if r.Type == wal.TypeUpdate || r.Type == wal.TypeCLR {
			if _, ok := dpt[r.PageID]; !ok {
				dpt[r.PageID] = r.LSN
			}
		}
	}
	res.TxTable = tt
	res.DirtyPages = dpt

	// ---------- Phase 2: Redo (repeat history) ----------
	redoFrom := wal.LSN(0)
	first := true
	for _, recLSN := range dpt {
		if first || recLSN < redoFrom {
			redoFrom = recLSN
			first = false
		}
	}
	if len(dpt) == 0 {
		redoFrom = start
	}
	for _, r := range recs {
		if r.LSN < redoFrom {
			continue
		}
		if r.Type != wal.TypeUpdate && r.Type != wal.TypeCLR {
			continue
		}
		recLSN, dirty := dpt[r.PageID]
		if !dirty || r.LSN < recLSN {
			continue // page was clean at crash time, or already newer
		}
		p := e.bp.Fetch(r.PageID)
		if p.LSN >= r.LSN {
			continue // already applied on disk: idempotence via pageLSN
		}
		copy(p.Data[r.Offset:], r.NewData)
		e.bp.MarkDirty(p, r.LSN)
		res.Redone = append(res.Redone, r.LSN)
	}

	// ---------- Phase 3: Undo (losers, reverse scan, CLRs) ----------
	pq := &lsnHeap{}
	for _, e2 := range tt {
		if e2.Status == StatusCommitted {
			res.Winners = append(res.Winners, e2.TxnID)
			continue
		}
		res.Losers = append(res.Losers, e2.TxnID)
		heap.Push(pq, lsnItem{lsn: e2.LastLSN, txn: e2.TxnID})
	}
	sort.Slice(res.Winners, func(i, j int) bool { return res.Winners[i] < res.Winners[j] })
	sort.Slice(res.Losers, func(i, j int) bool { return res.Losers[i] < res.Losers[j] })

	byLSN := make(map[wal.LSN]*wal.Record, len(recs))
	for _, r := range recs {
		byLSN[r.LSN] = r
	}
	lastLSN := make(map[wal.TxnID]wal.LSN) // txn -> last CLR/record written during undo

	appendRec := func(rec *wal.Record) wal.LSN {
		rec.PrevLSN = lastLSN[rec.TxnID]
		lsn, err := e.log.Append(rec)
		if err != nil {
			panic(err)
		}
		lastLSN[rec.TxnID] = lsn
		return lsn
	}

	for pq.Len() > 0 {
		item := heap.Pop(pq).(lsnItem)
		r := byLSN[item.lsn]
		if r == nil {
			continue
		}
		switch r.Type {
		case wal.TypeUpdate:
			// Write a CLR, apply the before-image, and continue at PrevLSN.
			clr := &wal.Record{
				Type: wal.TypeCLR, TxnID: r.TxnID, PageID: r.PageID,
				Offset: r.Offset, UndoNext: r.PrevLSN,
				OldData: r.NewData, NewData: r.OldData,
			}
			clrLSN := appendRec(clr)
			p := e.bp.Fetch(r.PageID)
			copy(p.Data[r.Offset:], r.OldData)
			e.bp.MarkDirty(p, clrLSN)
			res.Undone = append(res.Undone, r.LSN)
			res.CLRsWritten = append(res.CLRsWritten, clrLSN)
			if r.PrevLSN == 0 {
				appendRec(&wal.Record{Type: wal.TypeEnd, TxnID: r.TxnID})
			} else {
				heap.Push(pq, lsnItem{lsn: r.PrevLSN, txn: r.TxnID})
			}
		case wal.TypeCLR:
			// Already compensated once: never undo a CLR, follow UndoNext.
			if r.UndoNext == 0 {
				appendRec(&wal.Record{Type: wal.TypeEnd, TxnID: r.TxnID})
			} else {
				heap.Push(pq, lsnItem{lsn: r.UndoNext, txn: r.TxnID})
			}
		default: // BEGIN / ABORT
			if r.PrevLSN == 0 {
				appendRec(&wal.Record{Type: wal.TypeEnd, TxnID: r.TxnID})
			} else {
				heap.Push(pq, lsnItem{lsn: r.PrevLSN, txn: r.TxnID})
			}
		}
	}

	// Persist everything recovery produced, then force the log.
	e.bp.FlushAll()
	if err := e.log.Flush(); err != nil {
		return res, err
	}
	return res, nil
}

// lsnItem is a (LSN, txn) pair scheduled for undo.
type lsnItem struct {
	lsn wal.LSN
	txn wal.TxnID
}

// lsnHeap is a max-heap ordered by LSN so that Undo always processes the
// most recent record across all loser transactions first.
type lsnHeap []lsnItem

func (h lsnHeap) Len() int            { return len(h) }
func (h lsnHeap) Less(i, j int) bool  { return h[i].lsn > h[j].lsn }
func (h lsnHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *lsnHeap) Push(x interface{}) { *h = append(*h, x.(lsnItem)) }
func (h *lsnHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
