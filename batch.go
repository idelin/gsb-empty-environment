package schemamig

import "fmt"

// RowError pinpoints a failed row and field within a batch.
type RowError struct {
	// Index is the zero-based row position in the input batch.
	Index int
	// Path is the dotted location, e.g. "rows[2].price".
	Path string
	Err  error
}

func (e RowError) Error() string {
	return fmt.Sprintf("%s: %v", e.Path, e.Err)
}

// BatchError reports every row that failed migration.
type BatchError struct {
	Rows []RowError
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("batch migration failed: %d row(s) rejected", len(e.Rows))
}

// MigrateBatch migrates every row against the schema. The publish is
// atomic: if any row fails, no results are returned and the error lists
// a per-row path for each failure.
func (s *Schema) MigrateBatch(rows []Row) ([]Row, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	out := make([]Row, 0, len(rows))
	var errs []RowError
	for i, in := range rows {
		row, err := s.MigrateRow(in)
		if err != nil {
			errs = append(errs, RowError{
				Index: i,
				Path:  rowPath(i, err),
				Err:   err,
			})
			continue
		}
		out = append(out, row)
	}
	if len(errs) > 0 {
		return nil, &BatchError{Rows: errs}
	}
	return out, nil
}

// rowPath builds the per-row location, drilling into the field when the
// failure is tied to one.
func rowPath(i int, err error) string {
	if fe, ok := err.(*FieldError); ok {
		return fmt.Sprintf("rows[%d].%s", i, fe.Field)
	}
	return fmt.Sprintf("rows[%d]", i)
}
