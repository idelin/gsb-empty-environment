package wire

// Encoder is a minimal append-only wire-format writer. It exists so tests
// and examples can build fixtures without external dependencies; the
// parser itself never needs it.
type Encoder struct {
	buf []byte
}

// NewEncoder returns an empty Encoder.
func NewEncoder() *Encoder { return &Encoder{} }

// Bytes returns the accumulated output.
func (e *Encoder) Bytes() []byte { return e.buf }

// AppendVarint appends v in base-128 varint form.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// EncodeZigZag32 maps a signed value to its ZigZag varint payload.
func EncodeZigZag32(v int32) uint64 { return uint64(uint32(v<<1) ^ uint32(v>>31)) }

// EncodeZigZag64 maps a signed value to its ZigZag varint payload.
func EncodeZigZag64(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

// AppendFixed32 appends 4 little-endian bytes.
func AppendFixed32(dst []byte, v uint32) []byte {
	return append(dst, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// AppendFixed64 appends 8 little-endian bytes.
func AppendFixed64(dst []byte, v uint64) []byte {
	return append(dst,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// AppendKey appends the key for a field number and wire type.
func AppendKey(dst []byte, fieldNum int, wt WireType) []byte {
	return AppendVarint(dst, uint64(fieldNum)<<3|uint64(wt))
}

// Key writes a field key.
func (e *Encoder) Key(fieldNum int, wt WireType) *Encoder {
	e.buf = AppendKey(e.buf, fieldNum, wt)
	return e
}

// Varint writes a varint field.
func (e *Encoder) Varint(fieldNum int, v uint64) *Encoder {
	e.Key(fieldNum, WireVarint)
	e.buf = AppendVarint(e.buf, v)
	return e
}

// ZigZag writes a sint field.
func (e *Encoder) ZigZag(fieldNum int, v int64) *Encoder {
	return e.Varint(fieldNum, EncodeZigZag64(v))
}

// Fixed32 writes a fixed32 field.
func (e *Encoder) Fixed32(fieldNum int, v uint32) *Encoder {
	e.Key(fieldNum, WireFixed32)
	e.buf = AppendFixed32(e.buf, v)
	return e
}

// Fixed64 writes a fixed64 field.
func (e *Encoder) Fixed64(fieldNum int, v uint64) *Encoder {
	e.Key(fieldNum, WireFixed64)
	e.buf = AppendFixed64(e.buf, v)
	return e
}

// ByteField writes a length-delimited field.
func (e *Encoder) ByteField(fieldNum int, b []byte) *Encoder {
	e.Key(fieldNum, WireLengthDelimited)
	e.buf = AppendVarint(e.buf, uint64(len(b)))
	e.buf = append(e.buf, b...)
	return e
}

// String writes a length-delimited UTF-8 field.
func (e *Encoder) String(fieldNum int, s string) *Encoder {
	return e.ByteField(fieldNum, []byte(s))
}

// Message writes an embedded message field.
func (e *Encoder) Message(fieldNum int, inner *Encoder) *Encoder {
	return e.ByteField(fieldNum, inner.Bytes())
}

// PackedVarints writes a packed repeated varint field.
func (e *Encoder) PackedVarints(fieldNum int, vs ...uint64) *Encoder {
	var body []byte
	for _, v := range vs {
		body = AppendVarint(body, v)
	}
	return e.ByteField(fieldNum, body)
}

// PackedFixed32 writes a packed repeated fixed32 field.
func (e *Encoder) PackedFixed32(fieldNum int, vs ...uint32) *Encoder {
	var body []byte
	for _, v := range vs {
		body = AppendFixed32(body, v)
	}
	return e.ByteField(fieldNum, body)
}
