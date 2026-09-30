package codec

import "fmt"

// FieldType is the declared serialization type of a field.
type FieldType int

const (
	FVarint  FieldType = iota // uint64/int64/bool/enum
	FFixed64                  // float64 bits or fixed uint64
	FFixed32                  // float32 bits or fixed uint32
	FBytes                    // string, []byte, nested message
)

func (t FieldType) wireType() int {
	switch t {
	case FVarint:
		return wireVarint
	case FFixed64:
		return wireFixed64
	case FFixed32:
		return wireFixed32
	case FBytes:
		return wireBytes
	}
	return -1
}

func (t FieldType) String() string {
	switch t {
	case FVarint:
		return "varint"
	case FFixed64:
		return "fixed64"
	case FFixed32:
		return "fixed32"
	case FBytes:
		return "bytes"
	}
	return fmt.Sprintf("FieldType(%d)", int(t))
}

// FieldDef declares one field. Number is the stable on-the-wire identity;
// it must never be repurposed for an unrelated type.
type FieldDef struct {
	Number   int
	Name     string
	Type     FieldType
	Required bool
	Repeated bool
}

// Schema is a versioned set of field definitions plus a tombstone list
// of deleted (reserved) field numbers.
type Schema struct {
	name     string
	version  uint32
	fields   map[int]FieldDef
	reserved map[int]string
}

func NewSchema(name string, version uint32) *Schema {
	return &Schema{
		name:     name,
		version:  version,
		fields:   make(map[int]FieldDef),
		reserved: make(map[int]string),
	}
}

func (s *Schema) Name() string    { return s.name }
func (s *Schema) Version() uint32 { return s.version }

// AddField registers a field. It rejects:
//   - numbers outside [1, MaxFieldNumber]
//   - numbers reserved by ReserveDeleted (deleted numbers must not be
//     reused for unrelated types)
//   - numbers already defined (duplicate/conflicting definitions)
//   - duplicate names
func (s *Schema) AddField(def FieldDef) error {
	if def.Number < 1 || def.Number > MaxFieldNumber {
		return fmt.Errorf("codec: field number %d out of range [1, %d]", def.Number, MaxFieldNumber)
	}
	if def.Name == "" {
		return fmt.Errorf("codec: field %d: empty name", def.Number)
	}
	if def.Type < FVarint || def.Type > FBytes {
		return fmt.Errorf("codec: field %d (%s): unknown type %d", def.Number, def.Name, int(def.Type))
	}
	if reason, ok := s.reserved[def.Number]; ok {
		return fmt.Errorf("codec: field number %d is reserved (deleted: %s); deleted numbers must not be reused for an unrelated type", def.Number, reason)
	}
	if prev, ok := s.fields[def.Number]; ok {
		return fmt.Errorf("codec: field number %d already defined as %q (%s); conflicting definition %q (%s) rejected",
			def.Number, prev.Name, prev.Type, def.Name, def.Type)
	}
	for _, f := range s.fields {
		if f.Name == def.Name {
			return fmt.Errorf("codec: field name %q already used by number %d", def.Name, f.Number)
		}
	}
	s.fields[def.Number] = def
	return nil
}

// ReserveDeleted tombstones a field number so a future schema version
// cannot silently reuse it for an unrelated type.
func (s *Schema) ReserveDeleted(number int, reason string) error {
	if _, ok := s.fields[number]; ok {
		return fmt.Errorf("codec: cannot reserve field number %d: still in use", number)
	}
	s.reserved[number] = reason
	return nil
}

// Field returns the definition for a number, if any.
func (s *Schema) Field(number int) (FieldDef, bool) {
	def, ok := s.fields[number]
	return def, ok
}
