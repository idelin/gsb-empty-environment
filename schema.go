package wiremsg

// Kind is the schema-level type of a known field.
type Kind int

const (
	KindVarint Kind = iota
	KindFixed64
	KindFixed32
	KindBytes
	KindString
	KindMessage // nested message, length-delimited
)

func (k Kind) wireType() int {
	switch k {
	case KindVarint:
		return WireVarint
	case KindFixed64:
		return WireFixed64
	case KindFixed32:
		return WireFixed32
	default: // Bytes, String, Message
		return WireBytes
	}
}

// FieldSchema describes one known field.
type FieldSchema struct {
	Kind     Kind
	Repeated bool    // if false, a second occurrence is a conflict, never last-wins
	Nested   *Schema // required for KindMessage
}

// Schema maps field numbers to their known types. Any field number not
// present is unknown and preserved as a raw segment.
type Schema struct {
	Fields map[uint32]FieldSchema
}

func (s *Schema) lookup(num uint32) (FieldSchema, bool) {
	if s == nil {
		return FieldSchema{}, false
	}
	fs, ok := s.Fields[num]
	return fs, ok
}
