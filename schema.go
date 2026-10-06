package migrate

import "fmt"

// Type is a schema-level field type.
type Type int

const (
	TInt Type = iota
	TRat
	TText
)

func (t Type) String() string {
	switch t {
	case TInt:
		return "int"
	case TRat:
		return "rational"
	case TText:
		return "text"
	}
	return "unknown"
}

// Field describes one column of the target schema.
type Field struct {
	Name     string
	Type     Type
	Nullable bool
	// Default is used when the source row lacks the field. It is only
	// consulted when HasDefault is true; there is no implicit zero default.
	Default    Value
	HasDefault bool
}

// Schema is the target schema rows are migrated into.
type Schema struct {
	Fields []Field
	// UnknownColumn, when non-empty, names a single text column that
	// preserves source fields not present in Fields, encoded as JSON.
	UnknownColumn string

	index map[string]int
}

// NewSchema validates fields and builds a Schema. unknownColumn may be empty,
// in which case rows carrying unknown fields are rejected.
func NewSchema(unknownColumn string, fields ...Field) (*Schema, error) {
	s := &Schema{UnknownColumn: unknownColumn, index: map[string]int{}}
	for i, f := range fields {
		if f.Name == "" {
			return nil, fmt.Errorf("field %d: empty name", i)
		}
		if _, dup := s.index[f.Name]; dup {
			return nil, fmt.Errorf("duplicate field %q", f.Name)
		}
		if f.Name == unknownColumn && unknownColumn != "" {
			return nil, fmt.Errorf("field %q collides with unknown column", f.Name)
		}
		if f.HasDefault {
			if err := checkDefault(f); err != nil {
				return nil, fmt.Errorf("field %q: %v", f.Name, err)
			}
		}
		s.index[f.Name] = i
		s.Fields = append(s.Fields, f)
	}
	return s, nil
}

func checkDefault(f Field) error {
	d := f.Default
	if d.IsNull() {
		if !f.Nullable {
			return fmt.Errorf("null default on non-nullable field")
		}
		return nil
	}
	if typeOfKind(d.Kind()) != f.Type {
		return fmt.Errorf("default %s has kind %s, want %s", d, d.Kind(), f.Type)
	}
	return nil
}

func (s *Schema) has(name string) bool {
	_, ok := s.index[name]
	return ok
}

func typeOfKind(k Kind) Type {
	switch k {
	case Int:
		return TInt
	case Rat:
		return TRat
	case Text:
		return TText
	}
	return -1
}
