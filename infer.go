package infer

import "fmt"

// Option configures an Inferencer.
type Option func(*Inferencer)

// WithEmptyAsNull makes empty strings count as nulls instead of text.
// Nulls contribute no type evidence, so a column of only nulls falls
// back to TypeText.
func WithEmptyAsNull() Option {
	return func(inf *Inferencer) { inf.emptyAsNull = true }
}

// Inferencer incrementally infers the type of every column of a record
// stream. Records can be added and removed (undo); types widen and
// narrow accordingly.
type Inferencer struct {
	cols        []*Column
	emptyAsNull bool
}

// New returns an Inferencer for records with the given column names.
func New(columns []string, opts ...Option) *Inferencer {
	inf := &Inferencer{}
	for _, opt := range opts {
		opt(inf)
	}
	for _, name := range columns {
		inf.cols = append(inf.cols, newColumn(name, inf.emptyAsNull))
	}
	return inf
}

// AddRecord folds one record into the inference state. The record must
// have exactly one value per column.
func (inf *Inferencer) AddRecord(values []string) error {
	if len(values) != len(inf.cols) {
		return fmt.Errorf("infer: record has %d values, want %d", len(values), len(inf.cols))
	}
	for i, v := range values {
		inf.cols[i].Add(v)
	}
	return nil
}

// RemoveRecord undoes a previous AddRecord of the same values. It is
// atomic: if any value has no matching evidence, nothing is removed
// and an error is returned.
func (inf *Inferencer) RemoveRecord(values []string) error {
	if len(values) != len(inf.cols) {
		return fmt.Errorf("infer: record has %d values, want %d", len(values), len(inf.cols))
	}
	for i, v := range values {
		if inf.cols[i].counts.of(inf.cols[i].classOf(v)) == 0 {
			return fmt.Errorf("infer: column %q: cannot remove %q: no matching evidence", inf.cols[i].name, v)
		}
	}
	for i, v := range values {
		// Cannot fail: checked above.
		_ = inf.cols[i].Remove(v)
	}
	return nil
}

// Types returns the current inferred type of every column.
func (inf *Inferencer) Types() []Type {
	types := make([]Type, len(inf.cols))
	for i, c := range inf.cols {
		types[i] = c.Type()
	}
	return types
}

// Column returns the i-th column, exposing its evidence counts.
func (inf *Inferencer) Column(i int) *Column { return inf.cols[i] }

// Mismatch reports a column whose incrementally inferred type disagrees
// with a full scan of the source records.
type Mismatch struct {
	Column int    // column index
	Name   string // column name
	Got    Type   // type from the incremental state
	Want   Type   // type from the full scan
}

func (m Mismatch) String() string {
	return fmt.Sprintf("column %d (%s): got %s, full scan wants %s", m.Column, m.Name, m.Got, m.Want)
}

// Verify re-infers every column from scratch over the given records and
// compares the result with the current incremental state. It returns
// one Mismatch per disagreeing column; an empty slice means the
// incremental state is consistent with a full scan.
func (inf *Inferencer) Verify(records [][]string) ([]Mismatch, error) {
	names := make([]string, len(inf.cols))
	for i, c := range inf.cols {
		names[i] = c.name
	}
	fresh := New(names)
	fresh.emptyAsNull = inf.emptyAsNull
	for _, c := range fresh.cols {
		c.emptyAsNull = inf.emptyAsNull
	}
	for row, rec := range records {
		if err := fresh.AddRecord(rec); err != nil {
			return nil, fmt.Errorf("infer: verify: record %d: %v", row, err)
		}
	}
	var mismatches []Mismatch
	for i := range inf.cols {
		if got, want := inf.cols[i].Type(), fresh.cols[i].Type(); got != want {
			mismatches = append(mismatches, Mismatch{
				Column: i,
				Name:   names[i],
				Got:    got,
				Want:   want,
			})
		}
	}
	return mismatches, nil
}
