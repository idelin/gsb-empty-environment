package recovery

import (
	"sync"

	"aries/wal"
)

// TxnStatus is the Analysis-phase transaction state.
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
	}
	return "?"
}

// TxnEntry is one row of the TransactionTable.
type TxnEntry struct {
	TxnID   wal.TxnID
	Status  TxnStatus
	LastLSN wal.LSN
}

// DPTEntry is one row of the DirtyPageTable: the oldest LSN that may not
// yet be reflected on disk for this page.
type DPTEntry struct {
	PageID wal.PageID
	RecLSN wal.LSN
}

// TxnManager issues transactions against a WAL + buffer pool pair.
type TxnManager struct {
	mu      sync.Mutex
	log     *wal.WAL
	bp      *BufferPool
	nextTxn wal.TxnID
	lastLSN map[wal.TxnID]wal.LSN
}

func NewTxnManager(log *wal.WAL, bp *BufferPool) *TxnManager {
	return &TxnManager{log: log, bp: bp, nextTxn: 1, lastLSN: make(map[wal.TxnID]wal.LSN)}
}

func (tm *TxnManager) append(rec *wal.Record) wal.LSN {
	rec.PrevLSN = tm.lastLSN[rec.TxnID]
	lsn, err := tm.log.Append(rec)
	if err != nil {
		panic(err)
	}
	tm.lastLSN[rec.TxnID] = lsn
	return lsn
}

// Begin starts a new transaction and returns its ID.
func (tm *TxnManager) Begin() wal.TxnID {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	id := tm.nextTxn
	tm.nextTxn++
	tm.append(&wal.Record{Type: wal.TypeBegin, TxnID: id})
	return id
}

// Update logs and applies a byte-range write within one page.
func (tm *TxnManager) Update(txn wal.TxnID, page wal.PageID, off uint32, data []byte) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	p := tm.bp.Fetch(page)
	old := append([]byte(nil), p.Data[off:uint32(off)+uint32(len(data))]...)
	lsn := tm.append(&wal.Record{
		Type: wal.TypeUpdate, TxnID: txn, PageID: page, Offset: off,
		OldData: old, NewData: append([]byte(nil), data...),
	})
	copy(p.Data[off:], data)
	tm.bp.MarkDirty(p, lsn)
}

// Commit logs COMMIT and forces the log to disk (WAL rule: log before data).
func (tm *TxnManager) Commit(txn wal.TxnID) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.append(&wal.Record{Type: wal.TypeCommit, TxnID: txn})
	tm.log.Flush()
}

// Abort logs ABORT; the actual rollback is left to restart recovery.
func (tm *TxnManager) Abort(txn wal.TxnID) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.append(&wal.Record{Type: wal.TypeAbort, TxnID: txn})
}

// Checkpoint writes a checkpoint record capturing the current
// TransactionTable and DirtyPageTable, then flushes the log.
func (tm *TxnManager) Checkpoint() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tt := make([]TxnEntry, 0, len(tm.lastLSN))
	for id, lsn := range tm.lastLSN {
		tt = append(tt, TxnEntry{TxnID: id, Status: StatusRunning, LastLSN: lsn})
	}
	dptMap := tm.bp.DirtyPages()
	dpt := make([]DPTEntry, 0, len(dptMap))
	for id := range dptMap {
		dpt = append(dpt, DPTEntry{PageID: id, RecLSN: dptMap[id]})
	}
	tm.append(&wal.Record{Type: wal.TypeCheckpoint, Payload: encodeCheckpoint(tt, dpt)})
	tm.log.Flush()
}
