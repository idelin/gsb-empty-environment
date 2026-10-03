package wire

import "encoding/binary"

// Encoder builds protobuf wire-format buffers by hand. It is the write-side
// counterpart of Reader and exists so tests and demos can construct streams
// without any generated code.
type Encoder struct {
	buf []byte
}

// NewEncoder returns an empty Encoder.
func NewEncoder() *Encoder { return &Encoder{} }

// Bytes returns the encoded buffer.
func (e *Encoder) Bytes() []byte { return e.buf }

// Len returns the current encoded length.
func (e *Encoder) Len() int { return len(e.buf) }

// AppendVarint appends v in 7-bit-compressed varint form.
func (e *Encoder) AppendVarint(v uint64) {
	var tmp [10]byte
	n := binary.PutUvarint(tmp[:], v)
	e.buf = append(e.buf, tmp[:n]...)
}

// AppendTag appends a field key (field number + wire type).
func (e *Encoder) AppendTag(fieldNum int, wt WireType) {
	e.AppendVarint(uint64(fieldNum)<<3 | uint64(wt))
}

// AppendZigZag64 appends a sint64 field value (without tag).
func (e *Encoder) AppendZigZag64(v int64) {
	e.AppendVarint(ZigZagEncode64(v))
}

// AppendZigZag32 appends a sint32 field value (without tag).
func (e *Encoder) AppendZigZag32(v int32) {
	e.AppendVarint(uint64(ZigZagEncode32(v)))
}

// AppendFixed32 appends a little-endian 32-bit value.
func (e *Encoder) AppendFixed32(v uint32) {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	e.buf = append(e.buf, tmp[:]...)
}

// AppendFixed64 appends a little-endian 64-bit value.
func (e *Encoder) AppendFixed64(v uint64) {
	var tmp [8]byte
	binary.LittleEndian.PutUint64(tmp[:], v)
	e.buf = append(e.buf, tmp[:]...)
}

// AppendBytes appends a length-delimited blob.
func (e *Encoder) AppendBytes(b []byte) {
	e.AppendVarint(uint64(len(b)))
	e.buf = append(e.buf, b...)
}

// AppendString appends a length-delimited string.
func (e *Encoder) AppendString(s string) {
	e.AppendBytes([]byte(s))
}

// AppendRaw appends pre-encoded bytes verbatim.
func (e *Encoder) AppendRaw(b []byte) {
	e.buf = append(e.buf, b...)
}
