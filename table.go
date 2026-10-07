package infer

import "fmt"

// Table infers column types across rows of string values. Rows are
// added and removed as a whole; every column updates independently.
type Table struct {
	cols []*Column
}

// NewTable returns a Table with width columns.
func NewTable(width int) *Table {
	if width < 0 {
		width = 0
	}
	cols := make([]*Column, width)
	for i := range cols {
		cols[i] = NewColumn()
	}
	return &Table{cols: cols}
}

// Width returns the number of columns.
func (t *Table) Width() int {
	return len(t.cols)
}

// AddRow records one value per column. It returns an error if the row
// width does not match the table width.
func (t *Table) AddRow(row []string) error {
	if err := t.checkWidth(row); err != nil {
		return err
	}
	for i, v := range row {
		t.cols[i].Add(v)
	}
	return nil
}

// RemoveRow retracts a previously added row. If any column rejects a
// value, the row is left fully removed for the columns that accepted
// the removal and an error is returned; callers should only remove
// rows that were previously added.
func (t *Table) RemoveRow(row []string) error {
	if err := t.checkWidth(row); err != nil {
		return err
	}
	for i, v := range row {
		if err := t.cols[i].Remove(v); err != nil {
			return fmt.Errorf("infer: column %d: %v", i, err)
		}
	}
	return nil
}

// Column returns the Column at index i.
func (t *Table) Column(i int) *Column {
	return t.cols[i]
}

// Kind returns the inferred Kind of column i.
func (t *Table) Kind(i int) Kind {
	return t.cols[i].Kind()
}

// Schema returns the inferred Kind of every column.
func (t *Table) Schema() []Kind {
	kinds := make([]Kind, len(t.cols))
	for i, c := range t.cols {
		kinds[i] = c.Kind()
	}
	return kinds
}

func (t *Table) checkWidth(row []string) error {
	if len(row) != len(t.cols) {
		return fmt.Errorf("infer: row has %d values, table has %d columns", len(row), len(t.cols))
	}
	return nil
}
