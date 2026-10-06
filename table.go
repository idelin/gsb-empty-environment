// Package memtx implements an in-memory table with snapshot-isolation
// transactions. A transaction reads a fixed snapshot taken at Begin and
// buffers writes locally. Commit validates the final write set: write-write
// conflicts and unique-key races against rows committed after the snapshot
// abort the transaction, and unique-key uniqueness is enforced against the
// merged final state (so one transaction may swap two unique keys, or delete
// a row and reuse its key). Validation completes before any mutation, so a
// failed commit never partially modifies the main table or its indexes.
package memtx

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrWriteConflict is returned when a row written by this transaction
	// was modified by another transaction committed after our snapshot.
	ErrWriteConflict = errors.New("memtx: write-write conflict")
	// ErrUniqueConflict is returned when ownership of a unique key this
	// transaction claims changed after our snapshot was taken.
	ErrUniqueConflict = errors.New("memtx: unique-key conflict")
	// ErrTxnClosed is returned when using a transaction that already
	// committed or aborted.
	ErrTxnClosed = errors.New("memtx: transaction already finished")
)

// DuplicateKeyError reports a uniqueness violation visible in the final
// merged state (not a race): two live rows would share a unique key.
type DuplicateKeyError struct {
	Field string
	Value string
}

func (e *DuplicateKeyError) Error() string {
	return fmt.Sprintf("memtx: duplicate value %q for unique field %q", e.Value, e.Field)
}

// Row is a record keyed by field name. The primary-key field is required.
type Row map[string]string

// Table is an in-memory table with a primary key and unique secondary
// indexes. It is safe for concurrent use.
type Table struct {
	mu         sync.Mutex
	pkField    string
	uniqFields []string
	rows       map[string]Row               // pk -> row
	index      map[string]map[string]string // unique field -> value -> pk
	version    uint64                       // commit sequence number
	rowVer     map[string]uint64            // pk -> version of last committed write
	keyVer     map[string]uint64            // unique key -> version of last ownership change
}

// NewTable creates an empty table. pkField names the primary-key field;
// uniqFields name fields whose values must be unique across live rows.
func NewTable(pkField string, uniqFields ...string) *Table {
	index := make(map[string]map[string]string, len(uniqFields))
	for _, f := range uniqFields {
		index[f] = make(map[string]string)
	}
	return &Table{
		pkField:    pkField,
		uniqFields: uniqFields,
		rows:       make(map[string]Row),
		index:      index,
		rowVer:     make(map[string]uint64),
		keyVer:     make(map[string]uint64),
	}
}

func keyID(field, value string) string { return field + "\x00" + value }

func cloneRow(r Row) Row {
	if r == nil {
		return nil
	}
	c := make(Row, len(r))
	for k, v := range r {
		c[k] = v
	}
	return c
}

func cloneRows(rows map[string]Row) map[string]Row {
	c := make(map[string]Row, len(rows))
	for k, v := range rows {
		c[k] = cloneRow(v)
	}
	return c
}

// Seed inserts rows directly, outside any transaction. Intended for
// initializing fixtures.
func (t *Table) Seed(rows ...Row) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.version++
	for _, r := range rows {
		c := cloneRow(r)
		pk := c[t.pkField]
		t.rows[pk] = c
		t.rowVer[pk] = t.version
		for _, f := range t.uniqFields {
			if v, ok := c[f]; ok {
				t.index[f][v] = pk
				t.keyVer[keyID(f, v)] = t.version
			}
		}
	}
}

// View returns a deep copy of the currently committed rows.
func (t *Table) View() map[string]Row {
	t.mu.Lock()
	defer t.mu.Unlock()
	return cloneRows(t.rows)
}

// Begin opens a transaction reading the committed state as of now.
func (t *Table) Begin() *Txn {
	t.mu.Lock()
	defer t.mu.Unlock()
	return &Txn{
		tbl:     t,
		snapVer: t.version,
		snap:    cloneRows(t.rows),
		writes:  make(map[string]*writeOp),
	}
}

type writeOp struct {
	row Row // nil when del is true
	del bool
}

// Txn is a single-use snapshot-isolation transaction.
type Txn struct {
	tbl     *Table
	snapVer uint64
	snap    map[string]Row
	writes  map[string]*writeOp
	done    bool
}

// Get returns the row for pk as seen by this transaction: its own buffered
// writes overlaid on the fixed begin snapshot.
func (tx *Txn) Get(pk string) (Row, bool) {
	if op, ok := tx.writes[pk]; ok {
		if op.del {
			return nil, false
		}
		return cloneRow(op.row), true
	}
	r, ok := tx.snap[pk]
	if !ok {
		return nil, false
	}
	return cloneRow(r), true
}

// Put buffers an insert or replace of r. The row must carry the primary key.
func (tx *Txn) Put(r Row) error {
	if tx.done {
		return ErrTxnClosed
	}
	pk := r[tx.tbl.pkField]
	if pk == "" {
		return fmt.Errorf("memtx: row missing primary key %q", tx.tbl.pkField)
	}
	tx.writes[pk] = &writeOp{row: cloneRow(r)}
	return nil
}

// Delete buffers a delete of pk. Deleting a missing key is a no-op.
func (tx *Txn) Delete(pk string) error {
	if tx.done {
		return ErrTxnClosed
	}
	tx.writes[pk] = &writeOp{del: true}
	return nil
}

// Abort discards the transaction.
func (tx *Txn) Abort() { tx.done = true }

// Commit validates the final write set and, on success, atomically applies
// it to the main table and all unique indexes. Any failure leaves the table
// completely untouched.
func (tx *Txn) Commit() error {
	if tx.done {
		return ErrTxnClosed
	}
	tx.done = true
	t := tx.tbl
	t.mu.Lock()
	defer t.mu.Unlock()

	// 1. Write-write conflict: a row we write was committed after our snapshot.
	for pk := range tx.writes {
		if t.rowVer[pk] > tx.snapVer {
			return ErrWriteConflict
		}
	}

	// 2. Uniqueness within the final write set itself.
	type claim struct {
		field, value, pk string
	}
	var claims []claim
	seen := make(map[string]string) // unique key -> claiming pk
	for pk, op := range tx.writes {
		if op.del {
			continue
		}
		for _, f := range t.uniqFields {
			v, ok := op.row[f]
			if !ok {
				continue
			}
			id := keyID(f, v)
			if other, dup := seen[id]; dup && other != pk {
				return &DuplicateKeyError{Field: f, Value: v}
			}
			seen[id] = pk
			claims = append(claims, claim{f, v, pk})
		}
	}

	// 3. Uniqueness against committed state. A claimed key may currently be
	// owned by a row this same transaction deletes or rewrites (key swap /
	// delete-and-reuse); anything else is a duplicate, or a race if the
	// key's ownership changed after our snapshot.
	for _, c := range claims {
		id := keyID(c.field, c.value)
		owner, taken := t.index[c.field][c.value]
		switch {
		case taken:
			if _, transferred := tx.writes[owner]; transferred {
				continue // ownership moves inside this transaction
			}
			if t.keyVer[id] > tx.snapVer {
				return ErrUniqueConflict
			}
			return &DuplicateKeyError{Field: c.field, Value: c.value}
		default:
			if t.keyVer[id] > tx.snapVer {
				return ErrUniqueConflict
			}
		}
	}

	// 4. All validation passed: apply atomically. Index maintenance is
	// two-phase (remove every old key, then add every new key) so that a
	// key swap inside one transaction cannot delete an entry another of
	// its own writes just added.
	t.version++
	for pk := range tx.writes {
		if old := t.rows[pk]; old != nil {
			for _, f := range t.uniqFields {
				if ov, ok := old[f]; ok {
					delete(t.index[f], ov)
					t.keyVer[keyID(f, ov)] = t.version
				}
			}
		}
	}
	for pk, op := range tx.writes {
		if op.del {
			delete(t.rows, pk)
		} else {
			t.rows[pk] = cloneRow(op.row)
		}
		t.rowVer[pk] = t.version
	}
	for pk, op := range tx.writes {
		if op.del {
			continue
		}
		for _, f := range t.uniqFields {
			if nv, ok := op.row[f]; ok {
				t.index[f][nv] = pk
				t.keyVer[keyID(f, nv)] = t.version
			}
		}
	}
	return nil
}
