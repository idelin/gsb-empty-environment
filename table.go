// Package memtx implements an in-memory table with snapshot-isolation
// transactions and a unique secondary index.
//
// A transaction reads from a fixed snapshot taken at Begin, buffers its
// writes, and validates at Commit: write-write conflicts against rows
// modified after the snapshot, and uniqueness of the final write set
// against the live unique index. Validation completes before any mutation,
// so a failed commit never partially modifies the table or its index.
package memtx

import (
	"errors"
	"sync"
)

var (
	// ErrWriteConflict is returned when a row written by this transaction
	// was modified by another transaction committed after our snapshot.
	ErrWriteConflict = errors.New("memtx: write-write conflict")
	// ErrUniqueConflict is returned when the final write set violates the
	// unique index, either internally or against committed state.
	ErrUniqueConflict = errors.New("memtx: unique key conflict")
	// ErrTxnDone is returned when committing or aborting a finished txn.
	ErrTxnDone = errors.New("memtx: transaction already finished")
)

// Row is a table record. ID is the primary key, Email is unique.
type Row struct {
	ID    int
	Email string
}

// Table is an in-memory table: a primary map plus a unique index.
type Table struct {
	mu      sync.Mutex
	rows    map[int]Row
	byEmail map[string]int
	version uint64
	rowVer  map[int]uint64 // last version at which each primary key changed
}

// NewTable builds a table from the given rows.
func NewTable(rows ...Row) *Table {
	t := &Table{
		rows:    make(map[int]Row, len(rows)),
		byEmail: make(map[string]int, len(rows)),
		rowVer:  make(map[int]uint64, len(rows)),
	}
	for _, r := range rows {
		t.rows[r.ID] = r
		t.byEmail[r.Email] = r.ID
	}
	return t
}

// Dump returns deep copies of the primary map and the unique index.
func (t *Table) Dump() (rows map[int]Row, byEmail map[string]int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rows = make(map[int]Row, len(t.rows))
	for id, r := range t.rows {
		rows[id] = r
	}
	byEmail = make(map[string]int, len(t.byEmail))
	for e, id := range t.byEmail {
		byEmail[e] = id
	}
	return rows, byEmail
}

// Begin starts a transaction reading a fixed snapshot of the table.
func (t *Table) Begin() *Txn {
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := make(map[int]Row, len(t.rows))
	for id, r := range t.rows {
		snap[id] = r
	}
	return &Txn{
		table:  t,
		base:   t.version,
		snap:   snap,
		writes: make(map[int]*Row),
	}
}

// Txn is a snapshot-isolation transaction. It is not safe for concurrent
// use from multiple goroutines.
type Txn struct {
	table  *Table
	base   uint64
	snap   map[int]Row
	writes map[int]*Row // nil value means delete
	done   bool
}

// Get returns the row as of the transaction snapshot, overlaid with the
// transaction's own buffered writes.
func (tx *Txn) Get(id int) (Row, bool) {
	if w, ok := tx.writes[id]; ok {
		if w == nil {
			return Row{}, false
		}
		return *w, true
	}
	r, ok := tx.snap[id]
	return r, ok
}

// Put buffers an insert or update of r.
func (tx *Txn) Put(r Row) {
	w := r
	tx.writes[r.ID] = &w
}

// Delete buffers a delete of the row with the given primary key.
func (tx *Txn) Delete(id int) {
	tx.writes[id] = nil
}

// Abort discards the transaction's buffered writes.
func (tx *Txn) Abort() error {
	if tx.done {
		return ErrTxnDone
	}
	tx.done = true
	return nil
}

// Commit validates the final write set and applies it atomically.
// On any error the table and its unique index are left untouched.
func (tx *Txn) Commit() error {
	if tx.done {
		return ErrTxnDone
	}
	tx.done = true

	t := tx.table
	t.mu.Lock()
	defer t.mu.Unlock()

	// Write-write conflict: any row we touch that changed after our snapshot.
	for id := range tx.writes {
		if t.rowVer[id] > tx.base {
			return ErrWriteConflict
		}
	}

	// Uniqueness within the final write set.
	seen := make(map[string]int, len(tx.writes))
	for id, w := range tx.writes {
		if w == nil {
			continue
		}
		if prev, ok := seen[w.Email]; ok && prev != id {
			return ErrUniqueConflict
		}
		seen[w.Email] = id
	}

	// Uniqueness against the live index. A committed row holding an email
	// we want only blocks us if our write set does not move or delete it.
	for id, w := range tx.writes {
		if w == nil {
			continue
		}
		owner, ok := t.byEmail[w.Email]
		if !ok || owner == id {
			continue
		}
		if ow, moved := tx.writes[owner]; moved && (ow == nil || ow.Email != w.Email) {
			continue
		}
		return ErrUniqueConflict
	}

	// All validation passed; apply. Phase 1 drops stale index entries so a
	// swapped email cannot be removed after being re-inserted in phase 2.
	t.version++
	for id := range tx.writes {
		if old, ok := t.rows[id]; ok {
			delete(t.byEmail, old.Email)
		}
	}
	for id, w := range tx.writes {
		if w == nil {
			delete(t.rows, id)
		} else {
			t.rows[id] = *w
			t.byEmail[w.Email] = id
		}
		t.rowVer[id] = t.version
	}
	return nil
}
