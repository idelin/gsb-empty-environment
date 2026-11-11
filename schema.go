package fieldbinding

import "fmt"

// Kind enumerates the logical column types understood by the writer.
type Kind int

const (
	Int32 Kind = iota
	Int64
	Float32
	Float64
	String
	Bool
	Struct
)

func (k Kind) String() string {
	switch k {
	case Int32:
		return "int32"
	case Int64:
		return "int64"
	case Float32:
		return "float32"
	case Float64:
		return "float64"
	case String:
		return "string"
	case Bool:
		return "bool"
	case Struct:
		return "struct"
	}
	return "unknown"
}

// Type describes a field's logical type. Struct types nest child fields.
type Type struct {
	Kind     Kind
	Nullable bool
	Fields   []Field
}

// Field binds a stable ID to a name and a type. Names may change across
// versions; the ID is the only binding key.
type Field struct {
	ID   int
	Name string
	Type Type
}

// Schema is the versioned description of a batch or table record.
type Schema struct {
	Fields []Field
}

// fieldRef locates a field inside a schema, keeping the ancestor chain.
type fieldRef struct {
	field Field
	path  []Field // ancestors from the root, ending with field itself
}

func indexFields(fields []Field, ancestors []Field, out map[int]fieldRef) {
	for _, f := range fields {
		chain := append(append([]Field(nil), ancestors...), f)
		out[f.ID] = fieldRef{field: f, path: chain}
		if f.Type.Kind == Struct {
			indexFields(f.Type.Fields, chain, out)
		}
	}
}

func (s *Schema) index() map[int]fieldRef {
	out := make(map[int]fieldRef)
	indexFields(s.Fields, nil, out)
	return out
}

func (s *Schema) validate() error {
	seen := make(map[int]struct{})
	var walk func(fs []Field) error
	walk = func(fs []Field) error {
		for _, f := range fs {
			if _, dup := seen[f.ID]; dup {
				return fmt.Errorf("field %d (%q): %w", f.ID, f.Name, ErrDuplicateID)
			}
			seen[f.ID] = struct{}{}
			if f.Type.Kind == Struct {
				if err := walk(f.Type.Fields); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(s.Fields)
}

// lossless reports whether values of kind from widen to kind to without
// any loss. int64 -> float64 is deliberately excluded: it loses precision
// beyond 2^53.
func lossless(from, to Kind) bool {
	if from == to {
		return true
	}
	switch from {
	case Int32:
		return to == Int64 || to == Float64
	case Float32:
		return to == Float64
	}
	return false
}

func pathIDs(chain []Field) []int {
	ids := make([]int, len(chain))
	for i, f := range chain {
		ids[i] = f.ID
	}
	return ids
}

func parentID(path []int) int {
	if len(path) < 2 {
		return -1
	}
	return path[len(path)-2]
}
