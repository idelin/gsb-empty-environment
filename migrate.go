package migrate

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Row is one record keyed by field name.
type Row map[string]Value

// FieldError describes why one field of one row could not be migrated.
type FieldError struct {
	Field string // empty for row-level problems such as unknown fields
	Msg   string
}

// RowError locates a field failure within a batch.
type RowError struct {
	Row   int
	Field string
	Msg   string
}

// Path returns the error location, e.g. rows[3].age.
func (e RowError) Path() string {
	if e.Field == "" {
		return fmt.Sprintf("rows[%d]", e.Row)
	}
	return fmt.Sprintf("rows[%d].%s", e.Row, e.Field)
}

func (e RowError) Error() string { return e.Path() + ": " + e.Msg }

// BatchError reports every per-row failure of a rejected batch. When a
// BatchError is returned no results are published.
type BatchError struct {
	Errors []RowError
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("migration rejected: %d error(s) across batch; no results published", len(e.Errors))
}

// MigrateRow converts one row into the target schema. Missing fields take
// their explicit default; unknown source fields are preserved in the single
// UnknownColumn. On any failure it returns nil and all field errors found.
func (s *Schema) MigrateRow(r Row) (Row, []FieldError) {
	out := Row{}
	var errs []FieldError
	for _, f := range s.Fields {
		v, ok := r[f.Name]
		if !ok {
			if f.HasDefault {
				out[f.Name] = f.Default
			} else {
				errs = append(errs, FieldError{f.Name, "missing field and no default declared"})
			}
			continue
		}
		cv, err := convertField(f, v)
		if err != nil {
			errs = append(errs, FieldError{f.Name, err.Error()})
			continue
		}
		out[f.Name] = cv
	}
	var unknown []string
	for name := range r {
		if !s.has(name) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		if s.UnknownColumn == "" {
			sort.Strings(unknown)
			errs = append(errs, FieldError{"", fmt.Sprintf("unknown fields %v and no unknown column configured", unknown)})
		} else {
			out[s.UnknownColumn] = TextVal(encodeUnknown(r, unknown))
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return out, nil
}

// MigrateBatch migrates every row. The batch is atomic: if any row fails,
// it returns nil results and a *BatchError listing each per-row error path.
func (s *Schema) MigrateBatch(rows []Row) ([]Row, error) {
	var errs []RowError
	out := make([]Row, 0, len(rows))
	for i, r := range rows {
		nr, ferrs := s.MigrateRow(r)
		for _, fe := range ferrs {
			errs = append(errs, RowError{Row: i, Field: fe.Field, Msg: fe.Msg})
		}
		if len(ferrs) == 0 {
			out = append(out, nr)
		}
	}
	if len(errs) > 0 {
		return nil, &BatchError{Errors: errs}
	}
	return out, nil
}

func convertField(f Field, v Value) (Value, error) {
	if v.IsNull() {
		if f.Nullable {
			return NullVal(), nil
		}
		return Value{}, fmt.Errorf("null not allowed for non-nullable %s field", f.Type)
	}
	return Convert(v, f.Type)
}

// encodeUnknown serializes unknown fields deterministically as a JSON object.
func encodeUnknown(r Row, names []string) string {
	sort.Strings(names)
	obj := make(map[string]interface{}, len(names))
	for _, name := range names {
		obj[name] = jsonValue(r[name])
	}
	b, err := json.Marshal(obj) // json.Marshal sorts map keys
	if err != nil {
		panic("migrate: " + err.Error())
	}
	return string(b)
}

func jsonValue(v Value) interface{} {
	switch v.Kind() {
	case Null:
		return nil
	case Int:
		return v.Int()
	case Rat:
		return ratText(v.Rat())
	case Text:
		return v.Text()
	}
	return nil
}
