package wiremsg

import "fmt"

// Type is the logical type of a known field.
type Type int

const (
	TypeUint64  Type = iota // wire: varint
	TypeFixed32             // wire: fixed32
	TypeFixed64             // wire: fixed64
	TypeBytes               // wire: length-delimited bytes
	TypeString              // wire: length-delimited UTF-8 bytes
	TypeMessage             // wire: length-delimited nested message
)

// wireType maps a logical type to its wire type.
func (t Type) wireType() int {
	switch t {
	case TypeUint64:
		return WireVarint
	case TypeFixed32:
		return WireFixed32
	case TypeFixed64:
		return WireFixed64
	case TypeBytes, TypeString, TypeMessage:
		return WireBytes
	}
	return -1
}

// FieldDef describes one known field in a Schema.
type FieldDef struct {
	Name     string
	Type     Type
	Repeated bool
	Sub      *Schema // required when Type == TypeMessage
}

// Schema maps field numbers to definitions. A nil Schema is valid and means
// "no known fields": everything is preserved as unknown.
type Schema struct {
	fields map[uint32]FieldDef
}

// NewSchema builds a Schema. It panics on programmer error: duplicate field
// numbers, invalid types, or a message field without a sub-schema.
func NewSchema(defs map[uint32]FieldDef) *Schema {
	s := &Schema{fields: make(map[uint32]FieldDef, len(defs))}
	for num, def := range defs {
		if num == 0 {
			panic("wiremsg: field number 0 is invalid")
		}
		if def.Type.wireType() < 0 {
			panic(fmt.Sprintf("wiremsg: field %d has invalid type", num))
		}
		if def.Type == TypeMessage && def.Sub == nil {
			panic(fmt.Sprintf("wiremsg: field %d is a message without sub-schema", num))
		}
		s.fields[num] = def
	}
	return s
}

func (s *Schema) lookup(num uint32) (FieldDef, bool) {
	if s == nil {
		return FieldDef{}, false
	}
	def, ok := s.fields[num]
	return def, ok
}

// Value is the typed value of a known field. Exactly one member is
// meaningful, selected by the field's Type.
type Value struct {
	Uint  uint64   // TypeUint64, TypeFixed32 (low 32 bits), TypeFixed64
	Bytes []byte   // TypeBytes
	Str   string   // TypeString
	Msg   *Message // TypeMessage
}

// check validates that v matches the field definition.
func (def FieldDef) check(v Value) error {
	switch def.Type {
	case TypeUint64, TypeFixed64:
		return nil
	case TypeFixed32:
		if v.Uint > 0xffffffff {
			return fmt.Errorf("wiremsg: field %q: value %d overflows fixed32", def.Name, v.Uint)
		}
	case TypeBytes, TypeString:
	case TypeMessage:
		if v.Msg == nil {
			return fmt.Errorf("wiremsg: field %q: nil message value", def.Name)
		}
		if v.Msg.schema != def.Sub {
			return fmt.Errorf("wiremsg: field %q: message parsed with a different schema", def.Name)
		}
	}
	return nil
}
