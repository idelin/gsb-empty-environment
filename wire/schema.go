package wire

// Schema declares how length-delimited (type 2) fields are interpreted.
// Fields not mentioned in the schema are kept as raw bytes, in their
// original order, and are re-encoded verbatim.
type Schema struct {
	// Nested maps a field number to the schema of the nested message
	// carried in that field. A nil value means an empty schema: the
	// nested message is parsed and validated but every field inside it
	// is kept raw.
	Nested map[uint32]*Schema
	// Packed maps a field number to the wire type of the packed array
	// elements: TypeVarint, TypeFixed64 or TypeFixed32.
	Packed map[uint32]int
}

func (s *Schema) nestedFor(field uint32) (*Schema, bool) {
	if s == nil {
		return nil, false
	}
	sub, ok := s.Nested[field]
	return sub, ok
}

func (s *Schema) packedFor(field uint32) (int, bool) {
	if s == nil {
		return 0, false
	}
	t, ok := s.Packed[field]
	return t, ok
}
