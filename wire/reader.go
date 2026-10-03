package wire

import "fmt"

// TruncatedError describes a read that ran past the end of the buffer.
// It unwraps to ErrTruncated via == comparison on the sentinel.
type TruncatedError struct {
	Offset   int // byte offset where the read started
	Needed   int // bytes required
	Remain   int // bytes actually available
	Context  string
}

func (e *TruncatedError) Error() string {
	return fmt.Sprintf("wire: truncated %s at offset %d: need %d bytes, have %d",
		e.Context, e.Offset, e.Needed, e.Remain)
}

// Is reports whether target is ErrTruncated (kept for forward compatibility).
func (e *TruncatedError) Is(target error) bool { return target == ErrTruncated }

// Reader is a bounds-checked cursor over a wire-format buffer. Every method
// is safe against malformed input: a short buffer yields *TruncatedError
// (matching ErrTruncated) rather than a panic.
type Reader struct {
	buf []byte
	pos int
}

// NewReader returns a Reader over buf.
func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

// Len returns the number of unread bytes.
func (r *Reader) Len() int { return len(r.buf) - r.pos }

// Offset returns the current read offset.
func (r *Reader) Offset() int { return r.pos }

// Done reports whether the buffer is fully consumed.
func (r *Reader) Done() bool { return r.pos >= len(r.buf) }

func (r *Reader) truncated(needed int, ctx string) error {
	return &TruncatedError{Offset: r.pos, Needed: needed, Remain: r.Len(), Context: ctx}
}

// ReadVarint reads a varint-encoded uint64.
func (r *Reader) ReadVarint() (uint64, error) {
	v, n, err := DecodeVarint(r.buf[r.pos:])
	if err != nil {
		if err == ErrTruncated {
			return 0, r.truncated(1, "varint")
		}
		return 0, err
	}
	r.pos += n
	return v, nil
}

// ReadSVarint reads a ZigZag-encoded signed 64-bit integer.
func (r *Reader) ReadSVarint() (int64, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return ZigZagDecode64(v), nil
}

// ReadFixed32 reads a 4-byte little-endian value.
func (r *Reader) ReadFixed32() (uint32, error) {
	v, n, err := DecodeFixed32(r.buf[r.pos:])
	if err != nil {
		return 0, r.truncated(4, "fixed32")
	}
	r.pos += n
	return v, nil
}

// ReadFixed64 reads an 8-byte little-endian value.
func (r *Reader) ReadFixed64() (uint64, error) {
	v, n, err := DecodeFixed64(r.buf[r.pos:])
	if err != nil {
		return 0, r.truncated(8, "fixed64")
	}
	r.pos += n
	return v, nil
}

// ReadBytes reads a length-prefixed byte slice. The returned slice aliases
// the underlying buffer.
func (r *Reader) ReadBytes() ([]byte, error) {
	l, err := r.ReadVarint()
	if err != nil {
		return nil, err
	}
	if l > uint64(r.Len()) {
		return nil, r.truncated(int(l), "length-delimited payload")
	}
	out := r.buf[r.pos : r.pos+int(l)]
	r.pos += int(l)
	return out, nil
}

// ReadTag reads a field tag.
func (r *Reader) ReadTag() (fieldNum int, wt WireType, err error) {
	fieldNum, wt, n, err := DecodeTag(r.buf[r.pos:])
	if err != nil {
		if err == ErrTruncated {
			return 0, 0, r.truncated(1, "tag")
		}
		return 0, 0, err
	}
	r.pos += n
	return fieldNum, wt, nil
}

// ---------------------------------------------------------------------------
// Generic message model
// ---------------------------------------------------------------------------

// Field is one decoded field of a message. Exactly one of the value slots is
// meaningful, selected by WireType.
type Field struct {
	Number  int
	Wire    WireType
	Varint  uint64 // WireVarint
	Fixed32 uint32 // WireFixed32
	Fixed64 uint64 // WireFixed64
	Bytes   []byte // WireLengthDelimited (aliases input)
}

// Sint returns the ZigZag-decoded signed value of a WireVarint field.
func (f Field) Sint() int64 { return ZigZagDecode64(f.Varint) }

// Message is a decoded sequence of fields, preserving wire order.
type Message []Field

// ParseMessage decodes a complete buffer into fields. It fails on the first
// malformed or truncated field.
func ParseMessage(buf []byte) (Message, error) {
	r := NewReader(buf)
	var msg Message
	for !r.Done() {
		num, wt, err := r.ReadTag()
		if err != nil {
			return nil, err
		}
		f := Field{Number: num, Wire: wt}
		switch wt {
		case WireVarint:
			if f.Varint, err = r.ReadVarint(); err != nil {
				return nil, err
			}
		case WireFixed64:
			if f.Fixed64, err = r.ReadFixed64(); err != nil {
				return nil, err
			}
		case WireFixed32:
			if f.Fixed32, err = r.ReadFixed32(); err != nil {
				return nil, err
			}
		case WireLengthDelimited:
			if f.Bytes, err = r.ReadBytes(); err != nil {
				return nil, err
			}
		}
		msg = append(msg, f)
	}
	return msg, nil
}

// ByNumber returns all fields with the given field number, in wire order.
func (m Message) ByNumber(num int) []Field {
	var out []Field
	for _, f := range m {
		if f.Number == num {
			out = append(out, f)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Packed repeated helpers (length-delimited payloads)
// ---------------------------------------------------------------------------

// DecodePackedVarints decodes a packed repeated varint payload.
func DecodePackedVarints(payload []byte) ([]uint64, error) {
	r := NewReader(payload)
	var out []uint64
	for !r.Done() {
		v, err := r.ReadVarint()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// DecodePackedSvarints decodes a packed repeated ZigZag payload.
func DecodePackedSvarints(payload []byte) ([]int64, error) {
	us, err := DecodePackedVarints(payload)
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(us))
	for i, u := range us {
		out[i] = ZigZagDecode64(u)
	}
	return out, nil
}

// DecodePackedFixed32 decodes a packed repeated fixed32 payload.
func DecodePackedFixed32(payload []byte) ([]uint32, error) {
	if len(payload)%4 != 0 {
		return nil, &TruncatedError{Offset: len(payload) - len(payload)%4, Needed: 4, Remain: len(payload) % 4, Context: "packed fixed32"}
	}
	out := make([]uint32, 0, len(payload)/4)
	r := NewReader(payload)
	for !r.Done() {
		v, err := r.ReadFixed32()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// DecodePackedFixed64 decodes a packed repeated fixed64 payload.
func DecodePackedFixed64(payload []byte) ([]uint64, error) {
	if len(payload)%8 != 0 {
		return nil, &TruncatedError{Offset: len(payload) - len(payload)%8, Needed: 8, Remain: len(payload) % 8, Context: "packed fixed64"}
	}
	out := make([]uint64, 0, len(payload)/8)
	r := NewReader(payload)
	for !r.Done() {
		v, err := r.ReadFixed64()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
