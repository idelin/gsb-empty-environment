package schemamig

import "fmt"

// ExtraColumn is the single output column that preserves unknown input
// fields. Its value is a map of field name to original value.
const ExtraColumn = "_extra"

// Field describes one column of the target schema.
type Field struct {
	Name     string
	Type     Kind
	Nullable bool
	// Default is used when the input row lacks the field. It must itself
	// be a non-null value of Type, or null when Nullable is set.
	HasDefault bool
	Default    Value
}

// Schema is the target schema: an ordered set of fields.
type Schema struct {
	Fields []Field
}

// Validate checks that defaults agree with their field types.
func (s *Schema) Validate() error {
	seen := map[string]bool{}
	for _, f := range s.Fields {
		if f.Name == "" {
			return fmt.Errorf("schema: field with empty name")
		}
		if f.Name == ExtraColumn {
			return fmt.Errorf("schema: field name %q is reserved", ExtraColumn)
		}
		if f.Type == KindNull {
			return fmt.Errorf("schema: field %q has no type", f.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("schema: duplicate field %q", f.Name)
		}
		seen[f.Name] = true
		if !f.HasDefault {
			continue
		}
		if f.Default.Kind == KindNull {
			if !f.Nullable {
				return fmt.Errorf("schema: field %q has null default but is not nullable", f.Name)
			}
			continue
		}
		if _, err := Convert(f.Default, f.Type); err != nil {
			return fmt.Errorf("schema: field %q has invalid default: %v", f.Name, err)
		}
	}
	return nil
}

// Row is one record: field name to value.
type Row map[string]Value

// FieldError ties a migration failure to a specific field.
type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("field %q: %v", e.Field, e.Err)
}

// MigrateRow converts in into the target schema. Known fields are
// converted (with round-trip verification); missing fields fall back to
// their explicit default, or to null when nullable; unknown input fields
// are preserved under the single ExtraColumn key.
func (s *Schema) MigrateRow(in Row) (Row, error) {
	out := Row{}
	for _, f := range s.Fields {
		v, ok := in[f.Name]
		if !ok {
			switch {
			case f.HasDefault:
				out[f.Name] = f.Default
			case f.Nullable:
				out[f.Name] = Null()
			default:
				return nil, &FieldError{Field: f.Name, Err: fmt.Errorf("missing value and no default is defined")}
			}
			continue
		}
		if v.Kind == KindNull {
			if !f.Nullable {
				return nil, &FieldError{Field: f.Name, Err: fmt.Errorf("null in non-nullable field")}
			}
			out[f.Name] = Null()
			continue
		}
		w, err := Convert(v, f.Type)
		if err != nil {
			return nil, &FieldError{Field: f.Name, Err: err}
		}
		out[f.Name] = w
	}
	extra := Row{}
	for name, v := range in {
		if !s.has(name) {
			extra[name] = v
		}
	}
	if len(extra) > 0 {
		out[ExtraColumn] = RecordVal(extra)
	}
	return out, nil
}

func (s *Schema) has(name string) bool {
	for _, f := range s.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}
