package bridge

import (
	"errors"
	"fmt"
	"sort"
)

// FieldType is the wire-level type tag of a field.
type FieldType byte

const (
	TyInt FieldType = iota + 1
	TyBool
	TyString
	TyBytes
)

func (t FieldType) String() string {
	switch t {
	case TyInt:
		return "int"
	case TyBool:
		return "bool"
	case TyString:
		return "string"
	case TyBytes:
		return "bytes"
	}
	return fmt.Sprintf("type(%d)", byte(t))
}

// ExtFieldID is reserved for the extension blob that carries fields the
// peer schema cannot represent. Schemas must not define it.
const ExtFieldID = 0xFFFFFFFF

// ErrBadData marks malformed input, as opposed to merely incomplete input.
var ErrBadData = errors.New("bridge: bad data")

// Field describes one known field of a schema. Renaming a field across
// versions keeps the same ID; only IDs and types are significant on the wire.
type Field struct {
	ID       uint32
	Name     string
	Type     FieldType
	Required bool
}

// Schema is the input description of one protocol version.
type Schema struct {
	Name    string
	Version uint32
	Fields  map[uint32]Field
	// Defaults is the migration rule set: when the peer schema cannot
	// provide a field, the default value is injected during conversion.
	Defaults map[uint32]Value
}

// Validate checks schema consistency, including the reserved extension ID.
func (s *Schema) Validate() error {
	if s.Name == "" {
		return errors.New("bridge: schema name required")
	}
	for id, f := range s.Fields {
		if id == ExtFieldID {
			return fmt.Errorf("bridge: field id %#x is reserved for the extension blob", id)
		}
		if f.ID != id {
			return fmt.Errorf("bridge: field %q: map key %d != field id %d", f.Name, id, f.ID)
		}
		if f.Type < TyInt || f.Type > TyBytes {
			return fmt.Errorf("bridge: field %q: unknown type %d", f.Name, f.Type)
		}
	}
	for id, v := range s.Defaults {
		f, ok := s.Fields[id]
		if !ok {
			return fmt.Errorf("bridge: default rule for unknown field %d", id)
		}
		if v.Type != f.Type {
			return fmt.Errorf("bridge: default rule for field %d has type %s, want %s", id, v.Type, f.Type)
		}
	}
	return nil
}

func (s *Schema) sortedFields() []Field {
	out := make([]Field, 0, len(s.Fields))
	for _, f := range s.Fields {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
