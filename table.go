package fieldbinding

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Snapshot is an immutable, published view of the table. Readers may hold
// a Snapshot without synchronization; it is never mutated after publish.
type Snapshot struct {
	Version uint64
	Schema  *Schema
	Rows    []Value
}

// Batch is a single versioned update batch from the writer.
type Batch struct {
	Schema *Schema
	Rows   []Value
}

// Table publishes snapshots atomically: a batch is fully validated against
// the current snapshot before any reader can observe its rows.
type Table struct {
	mu   sync.Mutex // serializes writers
	snap atomic.Pointer[Snapshot]
}

// NewTable creates an empty table with the given schema at version 0.
func NewTable(s *Schema) *Table {
	t := &Table{}
	t.snap.Store(&Snapshot{Schema: s})
	return t
}

// Snapshot returns the latest published snapshot. Readers never observe a
// partially applied batch.
func (t *Table) Snapshot() *Snapshot { return t.snap.Load() }

// Commit validates the whole batch against the current snapshot and, only
// if the plan succeeds and every row projects cleanly, publishes a new
// snapshot atomically. On any error the current snapshot is left untouched.
func (t *Table) Commit(b *Batch) (*Snapshot, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	cur := t.snap.Load()
	m, err := Plan(b.Schema, cur.Schema)
	if err != nil {
		return nil, err
	}
	rows := make([]Value, len(cur.Rows), len(cur.Rows)+len(b.Rows))
	copy(rows, cur.Rows)
	for i, r := range b.Rows {
		pv, err := m.Project(r)
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
		rows = append(rows, pv)
	}
	next := &Snapshot{Version: cur.Version + 1, Schema: cur.Schema, Rows: rows}
	t.snap.Store(next)
	return next, nil
}
