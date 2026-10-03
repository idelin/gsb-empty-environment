package wire

// Encoder accumulates a protobuf wire-format message.
type Encoder struct {
	buf []byte
}

// NewEncoder returns an empty Encoder.
func NewEncoder() *Encoder { return &Encoder{} }

// Bytes returns the accumulated message.
func (e *Encoder) Bytes() []byte { return e.buf }

// Len returns the current message length.
func (e *Encoder) Len() int { return len(e.buf) }

// Tag writes a field tag.
func (e *Encoder) Tag(field int, t Type) *Encoder {
	e.buf = AppendTag(e.buf, field, t)
	return e
}

// Varint writes a varint field.
func (e *Encoder) Varint(field int, v uint64) *Encoder {
	e.buf = AppendTag(e.buf, field, Varint)
	e.buf = AppendVarint(e.buf, v)
	return e
}

// ZigZag64 writes a sint64 field.
func (e *Encoder) ZigZag64(field int, v int64) *Encoder {
	return e.Varint(field, EncodeZigZag64(v))
}

// Fixed32 writes a fixed32 field.
func (e *Encoder) Fixed32(field int, v uint32) *Encoder {
	e.buf = AppendTag(e.buf, field, Fixed32)
	e.buf = AppendFixed32(e.buf, v)
	return e
}

// Fixed64 writes a fixed64 field.
func (e *Encoder) Fixed64(field int, v uint64) *Encoder {
	e.buf = AppendTag(e.buf, field, Fixed64)
	e.buf = AppendFixed64(e.buf, v)
	return e
}

// Raw writes a length-delimited byte field.
func (e *Encoder) Raw(field int, p []byte) *Encoder {
	e.buf = AppendTag(e.buf, field, Bytes)
	e.buf = AppendBytes(e.buf, p)
	return e
}

// String writes a string field.
func (e *Encoder) String(field int, s string) *Encoder {
	e.buf = AppendTag(e.buf, field, Bytes)
	e.buf = AppendString(e.buf, s)
	return e
}

// Message writes an embedded (nested) message field.
func (e *Encoder) Message(field int, m []byte) *Encoder {
	return e.Raw(field, m)
}

// PackedVarints writes a packed repeated varint field.
func (e *Encoder) PackedVarints(field int, vs []uint64) *Encoder {
	e.buf = AppendTag(e.buf, field, Bytes)
	var p []byte
	for _, v := range vs {
		p = AppendVarint(p, v)
	}
	e.buf = AppendBytes(e.buf, p)
	return e
}

// PackedZigZag64 writes a packed repeated sint64 field.
func (e *Encoder) PackedZigZag64(field int, vs []int64) *Encoder {
	us := make([]uint64, len(vs))
	for i, v := range vs {
		us[i] = EncodeZigZag64(v)
	}
	return e.PackedVarints(field, us)
}
