package policy

import (
	"fmt"
	"sync"
)

// MemoryCatalog is an in-memory Store for tests and single-process execution
// layers. Each table is a slice of rows; transactions operate on a private
// copy and publish it atomically on Commit.
type MemoryCatalog struct {
	mu     sync.Mutex
	tables map[string]*memTable
}

type memTable struct {
	columns []string
	rows    []Row
}

// NewMemoryCatalog creates an empty catalog.
func NewMemoryCatalog() *MemoryCatalog {
	return &MemoryCatalog{tables: make(map[string]*memTable)}
}

// CreateTable registers a table with the given columns.
func (c *MemoryCatalog) CreateTable(name string, columns []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	dup := make([]string, len(columns))
	copy(dup, columns)
	c.tables[name] = &memTable{columns: dup}
}

// Seed inserts initial rows into a table outside any policy transaction.
func (c *MemoryCatalog) Seed(name string, rows ...Row) {
	c.mu.Lock()
	defer c.mu.Unlock()
	table := c.tables[name]
	for _, r := range rows {
		table.rows = append(table.rows, cloneRow(r))
	}
}

// Rows returns a defensive copy of the rows currently stored in a table.
func (c *MemoryCatalog) Rows(name string) []Row {
	c.mu.Lock()
	defer c.mu.Unlock()
	table := c.tables[name]
	out := make([]Row, len(table.rows))
	for i, r := range table.rows {
		out[i] = cloneRow(r)
	}
	return out
}

// Begin implements Store. The catalog lock is held for the transaction's
// lifetime, giving serializable isolation.
func (c *MemoryCatalog) Begin(table string) (Txn, error) {
	t, ok := c.tables[table]
	if !ok {
		return nil, fmt.Errorf("%w: table %q does not exist", ErrBadStatement, table)
	}
	c.mu.Lock()
	working := make([]Row, len(t.rows))
	for i, r := range t.rows {
		working[i] = cloneRow(r)
	}
	return &memTxn{
		catalog: c,
		name:    table,
		columns: append([]string(nil), t.columns...),
		rows:    working,
	}, nil
}

type memTxn struct {
	catalog *MemoryCatalog
	name    string
	columns []string
	rows    []Row
	done    bool
}

func (t *memTxn) Columns() []string { return append([]string(nil), t.columns...) }

func (t *memTxn) Snapshot() []Row {
	out := make([]Row, len(t.rows))
	for i, r := range t.rows {
		out[i] = r
	}
	return out
}

func (t *memTxn) Update(rowID int, next Row) error {
	if rowID < 0 || rowID >= len(t.rows) {
		return fmt.Errorf("%w: row id %d out of range", ErrBadStatement, rowID)
	}
	t.rows[rowID] = cloneRow(next)
	return nil
}

func (t *memTxn) Delete(rowID int) error {
	if rowID < 0 || rowID >= len(t.rows) {
		return fmt.Errorf("%w: row id %d out of range", ErrBadStatement, rowID)
	}
	t.rows = append(t.rows[:rowID], t.rows[rowID+1:]...)
	return nil
}

func (t *memTxn) Insert(row Row) error {
	complete := make(Row, len(t.columns))
	for _, c := range t.columns {
		complete[c] = nil
	}
	for k, v := range row {
		known := false
		for _, c := range t.columns {
			if c == k {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("%w: column %q", ErrUnknownColumn, k)
		}
		complete[k] = v
	}
	t.rows = append(t.rows, complete)
	return nil
}

func (t *memTxn) Commit() error {
	if t.done {
		return fmt.Errorf("%w: transaction already finished", ErrBadStatement)
	}
	t.done = true
	defer t.catalog.mu.Unlock()
	published := make([]Row, len(t.rows))
	for i, r := range t.rows {
		published[i] = cloneRow(r)
	}
	t.catalog.tables[t.name].rows = published
	return nil
}

func (t *memTxn) Rollback() error {
	if t.done {
		return nil
	}
	t.done = true
	t.catalog.mu.Unlock()
	return nil
}
