// Package db is a tiny database facade used to drive the WAL with
// realistic ARIES runtime behavior: a steal/no-force buffer pool,
// WAL-first updates, fuzzy checkpoints, flushes, and crashes.
package db

import (
	"sync"

	"aries/storage"
	"aries/wal"
)

type bufPage struct {
	data []byte
	lsn  wal.LSN // pageLSN of the in-memory copy
}

// DB is safe for concurrent use by multiple transactions.
type DB struct {
	mu      sync.Mutex
	WAL     *wal.WAL
	Disk    *storage.Disk
	buf     map[uint32]*bufPage
	recLSN  map[uint32]wal.LSN // dirty pages: first LSN since last flush
	lastLSN map[uint32]wal.LSN // active transactions
}

func New(w *wal.WAL, d *storage.Disk) *DB {
	return &DB{
		WAL:     w,
		Disk:    d,
		buf:     make(map[uint32]*bufPage),
		recLSN:  make(map[uint32]wal.LSN),
		lastLSN: make(map[uint32]wal.LSN),
	}
}

func zeroPage() []byte { return make([]byte, storage.PageSize) }

// Update applies a full-page physical update, logging first (WAL rule).
func (db *DB) Update(txn, page uint32, after []byte) wal.LSN {
	db.mu.Lock()
	defer db.mu.Unlock()
	bp, ok := db.buf[page]
	if !ok {
		bp = &bufPage{data: zeroPage()}
		if p, found := db.Disk.Read(page); found {
			copy(bp.data, p.Data)
			bp.lsn = p.LSN
		}
		db.buf[page] = bp
	}
	before := append([]byte(nil), bp.data...)
	lsn := db.WAL.Append(&wal.Record{
		Type:    wal.TypeUpdate,
		TxnID:   txn,
		PageID:  page,
		PrevLSN: db.lastLSN[txn],
		Before:  before,
		After:   append([]byte(nil), after...),
	})
	bp.data = append(bp.data[:0], after...)
	bp.lsn = lsn
	if _, dirty := db.recLSN[page]; !dirty {
		db.recLSN[page] = lsn
	}
	db.lastLSN[txn] = lsn
	return lsn
}

// Commit logs commit+end and makes the transaction durable.
func (db *DB) Commit(txn uint32) {
	db.mu.Lock()
	defer db.mu.Unlock()
	prev := db.lastLSN[txn]
	c := db.WAL.Append(&wal.Record{Type: wal.TypeCommit, TxnID: txn, PrevLSN: prev})
	db.WAL.Append(&wal.Record{Type: wal.TypeEnd, TxnID: txn, PrevLSN: c})
	delete(db.lastLSN, txn)
	db.WAL.Sync()
}

// Abort rolls back a live transaction, writing CLRs (same logic the
// recovery undo phase uses), then logs end.
func (db *DB) Abort(txn uint32) {
	db.mu.Lock()
	defer db.mu.Unlock()
	prev := db.WAL.Append(&wal.Record{Type: wal.TypeAbort, TxnID: txn, PrevLSN: db.lastLSN[txn]})
	lsn := db.lastLSN[txn]
	for lsn != wal.NilLSN {
		rec := db.WAL.Get(lsn)
		if rec == nil {
			break
		}
		switch rec.Type {
		case wal.TypeUpdate:
			clr := db.WAL.Append(&wal.Record{
				Type:        wal.TypeCLR,
				TxnID:       txn,
				PageID:      rec.PageID,
				PrevLSN:     prev,
				UndoNextLSN: rec.PrevLSN,
				Before:      rec.After,
				After:       rec.Before,
			})
			if bp, ok := db.buf[rec.PageID]; ok {
				bp.data = append(bp.data[:0], rec.Before...)
				bp.lsn = clr
				// The CLR dirties the page again; without this the
				// analysis DPT would miss it and redo would skip the
				// CLR, leaking the aborted value on disk.
				if _, dirty := db.recLSN[rec.PageID]; !dirty {
					db.recLSN[rec.PageID] = clr
				}
			}
			prev = clr
			lsn = rec.PrevLSN
		case wal.TypeCLR:
			lsn = rec.UndoNextLSN
		default:
			lsn = rec.PrevLSN
		}
	}
	db.WAL.Append(&wal.Record{Type: wal.TypeEnd, TxnID: txn, PrevLSN: prev})
	delete(db.lastLSN, txn)
}

// Checkpoint writes a fuzzy checkpoint with the current DPT and TT.
func (db *DB) Checkpoint() wal.LSN {
	db.mu.Lock()
	defer db.mu.Unlock()
	dpt := make(map[uint32]wal.LSN, len(db.recLSN))
	for p, l := range db.recLSN {
		dpt[p] = l
	}
	tt := make(map[uint32]wal.TxnCheckpointEntry, len(db.lastLSN))
	for t, l := range db.lastLSN {
		tt[t] = wal.TxnCheckpointEntry{LastLSN: l, Status: wal.StatusRunning}
	}
	return db.WAL.Append(&wal.Record{Type: wal.TypeCheckpoint, DPT: dpt, TT: tt})
}

// Flush writes one dirty page to disk (steal).
func (db *DB) Flush(page uint32) {
	db.mu.Lock()
	defer db.mu.Unlock()
	bp, ok := db.buf[page]
	if !ok {
		return
	}
	db.Disk.Write(page, bp.data, bp.lsn)
	delete(db.recLSN, page)
}

// FlushAll writes every dirty page to disk.
func (db *DB) FlushAll() {
	db.mu.Lock()
	pages := make([]uint32, 0, len(db.recLSN))
	for p := range db.recLSN {
		pages = append(pages, p)
	}
	db.mu.Unlock()
	for _, p := range pages {
		db.Flush(p)
	}
}

// DirtyPages returns the current dirty page set (for tests).
func (db *DB) DirtyPages() []uint32 {
	db.mu.Lock()
	defer db.mu.Unlock()
	out := make([]uint32, 0, len(db.recLSN))
	for p := range db.recLSN {
		out = append(out, p)
	}
	return out
}

// Crash simulates a power failure: all volatile state (buffer pool,
// transaction table) is lost. The WAL file and disk survive.
func (db *DB) Crash() {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.buf = make(map[uint32]*bufPage)
	db.recLSN = make(map[uint32]wal.LSN)
	db.lastLSN = make(map[uint32]wal.LSN)
}
