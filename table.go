package starjoin

// Row is a single record keyed by column name.
type Row map[string]any

// Table is an in-memory relation.
type Table struct {
	Name    string
	Columns []string
	Rows    []Row
}

// Len returns the row count.
func (t *Table) Len() int { return len(t.Rows) }
