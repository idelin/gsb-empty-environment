package wire

// Message is a parsed message: its fields in the exact order they
// appeared on the wire, including repeated and unknown fields.
type Message struct {
	Fields []Field
}

// Field is a single parsed field. Raw always holds the value bytes
// exactly as they appeared on the wire (the payload for TypeBytes), so
// unknown fields re-encode losslessly. Nested and Packed are set when
// the schema interprets a length-delimited field.
type Field struct {
	Number uint32
	Type   int
	Value  uint64   // decoded value for TypeVarint, TypeFixed64, TypeFixed32
	Raw    []byte   // raw value bytes as they appeared on the wire
	Nested *Message // non-nil when the schema declares a nested message
	Packed *Packed  // non-nil when the schema declares a packed array
}

// Packed is a decoded packed array.
type Packed struct {
	ElemType int      // TypeVarint, TypeFixed64 or TypeFixed32
	Elems    []uint64 // fixed32 values are zero-extended
}

// AppendTo re-encodes the message onto dst and returns the result.
// Fields are emitted in their original order; unknown fields are
// reproduced byte-for-byte from Raw, so decoding and re-encoding a
// message never drops or reorders unknown data.
func (m *Message) AppendTo(dst []byte) []byte {
	for i := range m.Fields {
		dst = m.Fields[i].AppendTo(dst)
	}
	return dst
}

// Marshal re-encodes the message.
func (m *Message) Marshal() []byte {
	return m.AppendTo(nil)
}

// AppendTo re-encodes the field onto dst.
func (f *Field) AppendTo(dst []byte) []byte {
	dst = appendVarint(dst, keyOf(f.Number, f.Type))
	switch {
	case f.Nested != nil:
		body := f.Nested.AppendTo(nil)
		dst = appendVarint(dst, uint64(len(body)))
		dst = append(dst, body...)
	case f.Packed != nil:
		body := f.Packed.AppendTo(nil)
		dst = appendVarint(dst, uint64(len(body)))
		dst = append(dst, body...)
	case f.Type == TypeBytes:
		dst = appendVarint(dst, uint64(len(f.Raw)))
		dst = append(dst, f.Raw...)
	default:
		dst = append(dst, f.Raw...)
	}
	return dst
}

// AppendTo re-encodes the packed elements onto dst. Varint elements are
// re-encoded in canonical form.
func (p *Packed) AppendTo(dst []byte) []byte {
	switch p.ElemType {
	case TypeVarint:
		for _, v := range p.Elems {
			dst = appendVarint(dst, v)
		}
	case TypeFixed64:
		for _, v := range p.Elems {
			dst = appendFixed64(dst, v)
		}
	case TypeFixed32:
		for _, v := range p.Elems {
			dst = appendFixed32(dst, uint32(v))
		}
	}
	return dst
}
