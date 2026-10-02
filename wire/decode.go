package wire

// DefaultMaxDepth bounds recursive decoding of embedded messages so a
// hostile or corrupt stream cannot exhaust the stack.
const DefaultMaxDepth = 100

// Field is one decoded field of a generic message tree. Exactly one of
// the typed accessors is meaningful, selected by WireType.
type Field struct {
	Number   int
	WireType WireType
	Offset   int // byte offset of the field key within its parent buffer

	U64   uint64 // WireVarint payload (also ZigZag source)
	U32   uint32 // WireFixed32 payload
	U64F  uint64 // WireFixed64 payload
	Bytes []byte // WireLengthDelimited raw payload

	// Message is non-nil when a length-delimited payload parsed cleanly
	// as an embedded message (see DecodeMessage).
	Message *Message
}

// Message is a generically decoded protobuf message: an ordered list of
// fields, preserving duplicates for non-packed repeated fields.
type Message struct {
	Fields []*Field
}

// ByNumber returns all fields with the given number, in wire order.
func (m *Message) ByNumber(num int) []*Field {
	var out []*Field
	for _, f := range m.Fields {
		if f.Number == num {
			out = append(out, f)
		}
	}
	return out
}

// PackedVarints decodes the payload of a length-delimited field as a
// packed repeated varint array.
func (f *Field) PackedVarints() ([]uint64, error) {
	if f.WireType != WireLengthDelimited {
		return nil, newError(ErrTypeMismatch, f.Offset, f.Number,
			"packed varints require length-delimited payload, got %s", f.WireType)
	}
	r := NewReader(f.Bytes)
	var out []uint64
	for !r.EOF() {
		v, err := r.ReadVarint()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// PackedFixed32 decodes the payload as a packed repeated fixed32 array.
func (f *Field) PackedFixed32() ([]uint32, error) {
	if f.WireType != WireLengthDelimited {
		return nil, newError(ErrTypeMismatch, f.Offset, f.Number,
			"packed fixed32 require length-delimited payload, got %s", f.WireType)
	}
	if len(f.Bytes)%4 != 0 {
		return nil, newError(ErrTruncated, f.Offset, f.Number,
			"packed fixed32 payload %d bytes not a multiple of 4", len(f.Bytes))
	}
	r := NewReader(f.Bytes)
	var out []uint32
	for !r.EOF() {
		v, err := r.ReadFixed32()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// DecodeMessage parses buf into a generic Message tree. Length-delimited
// payloads that themselves parse as a complete, well-formed message are
// exposed via Field.Message (heuristic nested detection); the raw bytes
// are always retained in Field.Bytes. maxDepth bounds nesting; pass
// DefaultMaxDepth when unsure.
func DecodeMessage(buf []byte, maxDepth int) (*Message, error) {
	if maxDepth < 0 {
		return nil, newError(ErrDepthLimit, 0, 0, "nesting exceeds maximum depth")
	}
	m := &Message{}
	r := NewReader(buf)
	for !r.EOF() {
		off := r.Offset()
		num, wt, err := r.ReadKey()
		if err != nil {
			return nil, err
		}
		f := &Field{Number: num, WireType: wt, Offset: off}
		switch wt {
		case WireVarint:
			if f.U64, err = r.ReadVarint(); err != nil {
				return nil, err
			}
		case WireFixed32:
			if f.U32, err = r.ReadFixed32(); err != nil {
				return nil, err
			}
		case WireFixed64:
			if f.U64F, err = r.ReadFixed64(); err != nil {
				return nil, err
			}
		case WireLengthDelimited:
			if f.Bytes, err = r.ReadLengthDelimited(); err != nil {
				return nil, err
			}
			if len(f.Bytes) > 0 {
				sub, serr := DecodeMessage(f.Bytes, maxDepth-1)
				switch {
				case serr == nil:
					f.Message = sub
				case CodeOf(serr) == ErrDepthLimit:
					// Depth violations are structural and must
					// propagate, not be absorbed by the
					// nested-message heuristic.
					return nil, serr
				}
			}
		}
		m.Fields = append(m.Fields, f)
	}
	return m, nil
}
