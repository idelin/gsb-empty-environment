package wirefmt

// EncodeZigZag32 maps a signed int32 to an unsigned varint-friendly
// value: 0->0, -1->1, 1->2, -2->3, ...
func EncodeZigZag32(v int32) uint32 { return uint32(v<<1) ^ uint32(v>>31) }

// EncodeZigZag64 maps a signed int64 to an unsigned varint-friendly value.
func EncodeZigZag64(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

// DecodeZigZag32 is the inverse of EncodeZigZag32.
func DecodeZigZag32(v uint32) int32 { return int32(v>>1) ^ -int32(v&1) }

// DecodeZigZag64 is the inverse of EncodeZigZag64.
func DecodeZigZag64(v uint64) int64 { return int64(v>>1) ^ -int64(v&1) }

// Encoder builds a protobuf wire-format buffer. It exists so tests and
// demos can construct streams for the hand-written parser; the parser
// itself never depends on it.
type Encoder struct {
	buf []byte
}

// NewEncoder returns an empty Encoder.
func NewEncoder() *Encoder { return &Encoder{} }

// Bytes returns the encoded buffer.
func (e *Encoder) Bytes() []byte { return e.buf }

// AppendVarint appends v using 7-bit groups, LSB first.
func (e *Encoder) AppendVarint(v uint64) {
	for v >= 0x80 {
		e.buf = append(e.buf, byte(v)|0x80)
		v >>= 7
	}
	e.buf = append(e.buf, byte(v))
}

// AppendTag appends a field key.
func (e *Encoder) AppendTag(fieldNum int, wt WireType) {
	e.AppendVarint(uint64(fieldNum)<<3 | uint64(wt))
}

// AppendUint64 appends a varint field.
func (e *Encoder) AppendUint64(fieldNum int, v uint64) {
	e.AppendTag(fieldNum, WireVarint)
	e.AppendVarint(v)
}

// AppendSint32 appends a ZigZag-encoded varint field.
func (e *Encoder) AppendSint32(fieldNum int, v int32) {
	e.AppendTag(fieldNum, WireVarint)
	e.AppendVarint(uint64(EncodeZigZag32(v)))
}

// AppendSint64 appends a ZigZag-encoded varint field.
func (e *Encoder) AppendSint64(fieldNum int, v int64) {
	e.AppendTag(fieldNum, WireVarint)
	e.AppendVarint(EncodeZigZag64(v))
}

// AppendFixed32 appends a 4-byte little-endian field.
func (e *Encoder) AppendFixed32(fieldNum int, v uint32) {
	e.AppendTag(fieldNum, WireFixed32)
	e.buf = append(e.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// AppendFixed64 appends an 8-byte little-endian field.
func (e *Encoder) AppendFixed64(fieldNum int, v uint64) {
	e.AppendTag(fieldNum, WireFixed64)
	for i := uint(0); i < 8; i++ {
		e.buf = append(e.buf, byte(v>>(8*i)))
	}
}

// AppendBytes appends a length-delimited field.
func (e *Encoder) AppendBytes(fieldNum int, b []byte) {
	e.AppendTag(fieldNum, WireLengthDelimited)
	e.AppendVarint(uint64(len(b)))
	e.buf = append(e.buf, b...)
}

// AppendString appends a length-delimited string field.
func (e *Encoder) AppendString(fieldNum int, s string) {
	e.AppendBytes(fieldNum, []byte(s))
}

// AppendMessage appends a nested length-delimited message built by fn.
func (e *Encoder) AppendMessage(fieldNum int, fn func(sub *Encoder)) {
	sub := NewEncoder()
	fn(sub)
	e.AppendBytes(fieldNum, sub.Bytes())
}

// AppendPackedVarints appends a packed repeated varint field.
func (e *Encoder) AppendPackedVarints(fieldNum int, vs []uint64) {
	e.AppendMessage(fieldNum, func(sub *Encoder) {
		for _, v := range vs {
			sub.AppendVarint(v)
		}
	})
}

// AppendPackedSint32s appends a packed repeated sint32 field.
func (e *Encoder) AppendPackedSint32s(fieldNum int, vs []int32) {
	e.AppendMessage(fieldNum, func(sub *Encoder) {
		for _, v := range vs {
			sub.AppendVarint(uint64(EncodeZigZag32(v)))
		}
	})
}

// AppendPackedFixed32s appends a packed repeated fixed32 field.
func (e *Encoder) AppendPackedFixed32s(fieldNum int, vs []uint32) {
	e.AppendMessage(fieldNum, func(sub *Encoder) {
		for _, v := range vs {
			sub.buf = append(sub.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
		}
	})
}

// AppendPackedFixed64s appends a packed repeated fixed64 field.
func (e *Encoder) AppendPackedFixed64s(fieldNum int, vs []uint64) {
	e.AppendMessage(fieldNum, func(sub *Encoder) {
		for _, v := range vs {
			for i := uint(0); i < 8; i++ {
				sub.buf = append(sub.buf, byte(v>>(8*i)))
			}
		}
	})
}
