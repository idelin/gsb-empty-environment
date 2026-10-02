// Package starjoin implements a star-schema join optimizer: it recognizes a
// central fact table joined to several small dimension tables, pushes each
// dimension predicate down into an in-memory bitmap over the dimension's
// primary-key domain, and applies all bitmaps as a combined filter during a
// single scan of the fact table (bitmap join filter / semijoin reduction).
package starjoin

import "fmt"

// Kind is the storage type of a column.
type Kind int

const (
	IntCol Kind = iota
	StrCol
)

// Column is a typed columnar vector. Exactly one of Ints/Strs is populated.
type Column struct {
	Name string
	Kind Kind
	Ints []int64
	Strs []string
}

// IntColumn builds an int64 column.
func IntColumn(name string, vals []int64) *Column {
	return &Column{Name: name, Kind: IntCol, Ints: vals}
}

// StrColumn builds a string column.
func StrColumn(name string, vals []string) *Column {
	return &Column{Name: name, Kind: StrCol, Strs: vals}
}

// Table is a minimal columnar relation.
type Table struct {
	Name  string
	Cols  []*Column
	NRows int
	idx   map[string]int
}

// NewTable assembles a table from columns, validating equal lengths and
// unique names.
func NewTable(name string, cols ...*Column) (*Table, error) {
	t := &Table{Name: name, Cols: cols, idx: make(map[string]int, len(cols))}
	for i, c := range cols {
		if _, dup := t.idx[c.Name]; dup {
			return nil, fmt.Errorf("table %s: duplicate column %q", name, c.Name)
		}
		n := c.Len()
		if i == 0 {
			t.NRows = n
		} else if n != t.NRows {
			return nil, fmt.Errorf("table %s: column %q has %d rows, want %d", name, c.Name, n, t.NRows)
		}
		t.idx[c.Name] = i
	}
	return t, nil
}

// Len returns the number of values in the column.
func (c *Column) Len() int {
	if c.Kind == IntCol {
		return len(c.Ints)
	}
	return len(c.Strs)
}

// Col returns the column with the given name, or nil.
func (t *Table) Col(name string) *Column {
	i, ok := t.idx[name]
	if !ok {
		return nil
	}
	return t.Cols[i]
}
