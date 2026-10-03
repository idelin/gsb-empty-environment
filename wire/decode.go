package wire

import (
	"encoding/binary"
	"fmt"
)

// maxVarintBytes is the longest legal varint: ceil(64/7) = 10 bytes.
const maxVarintBytes = 10

// Decoder is a zero-allocation cursor over a protobuf wire-format buffer.
// It never reads out of bounds; any short read yields *TruncatedError.
type Decoder struct {
	buf []byte
	pos int
}

// NewDecoder returns a Decoder over b. The buffer is not copied.
func NewDecoder(b []byte) *Decoder { return &Decoder{buf: b} }

// Remaining reports how many unread bytes are left.
func (d *Decoder) Remaining() int { return len(d.buf) - d.pos }

// EOF reports whether the buffer is fully consumed.
func (d *Decoder) EOF() bool { return d.pos >= len(d.buf) }

// Offset returns the current read position.
func (d *Decoder) Offset() int { return d.pos }

// Varint decodes a base-128 varint (7 payload bits per byte).
// It rejects encodings longer than 10 bytes or overflowing 64 bits.
func (d *Decoder) Varint() (uint64, error) {
	var v uint64
	for i := 0; i < maxVarintBytes; i++ {
		if d.pos >= len(d.buf) {
			return 0, &TruncatedError{Op: "varint", Need: 1, Have: 0}
		}
		c := d.buf[d.pos]
		d.pos++
		if i == maxVarintBytes-1 {
			// 10th byte carries a single payload bit (bit 63).
			if c > 1 {
				return 0, ErrOverflow
			}
			return v | uint64(c)<<63, nil
		}
		v |= uint64(c&0x7f) << (7 * uint(i))
		if c < 0x80 {
			return v, nil
		}
	}
	panic("unreachable")
}

// ZigZag64 decodes a varint and applies the ZigZag inverse transform.
func (d *Decoder) ZigZag64() (int64, error) {
	v, err := d.Varint()
	if err != nil {
		return 0, err
	}
	return DecodeZigZag64(v), nil
}

// Fixed32 decodes 4 little-endian bytes.
func (d *Decoder) Fixed32() (uint32, error) {
	if d.Remaining() < 4 {
		return 0, &TruncatedError{Op: "fixed32", Need: 4 - d.Remaining(), Have: d.Remaining()}
	}
	v := binary.LittleEndian.Uint32(d.buf[d.pos:])
	d.pos += 4
	return v, nil
}

// Fixed64 decodes 8 little-endian bytes.
func (d *Decoder) Fixed64() (uint64, error) {
	if d.Remaining() < 8 {
		return 0, &TruncatedError{Op: "fixed64", Need: 8 - d.Remaining(), Have: d.Remaining()}
	}
	v := binary.LittleEndian.Uint64(d.buf[d.pos:])
	d.pos += 8
	return v, nil
}

// Bytes decodes a varint length prefix and returns that many bytes.
// The result aliases the input buffer.
func (d *Decoder) Bytes() ([]byte, error) {
	n, err := d.Varint()
	if err != nil {
		return nil, err
	}
	if n > uint64(d.Remaining()) {
		return nil, &TruncatedError{Op: "bytes", Need: int(n) - d.Remaining(), Have: d.Remaining()}
	}
	p := d.buf[d.pos : d.pos+int(n)]
	d.pos += int(n)
	return p, nil
}

// String decodes a length-delimited UTF-8 string.
func (d *Decoder) String() (string, error) {
	p, err := d.Bytes()
	if err != nil {
		return "", err
	}
	return string(p), nil
}

// Tag decodes a field tag: field number + wire type.
func (d *Decoder) Tag() (field int, t Type, err error) {
	v, err := d.Varint()
	if err != nil {
		return 0, 0, err
	}
	t = Type(v & 7)
	field = int(v >> 3)
	if field <= 0 {
		return 0, 0, fmt.Errorf("wire: invalid field number %d", field)
	}
	switch t {
	case Varint, Fixed64, Bytes, Fixed32:
		return field, t, nil
	default:
		return 0, 0, fmt.Errorf("%w: %d", ErrBadWireType, int(t))
	}
}

// Skip discards one value of the given wire type.
func (d *Decoder) Skip(t Type) error {
	switch t {
	case Varint:
		_, err := d.Varint()
		return err
	case Fixed64:
		_, err := d.Fixed64()
		return err
	case Bytes:
		_, err := d.Bytes()
		return err
	case Fixed32:
		_, err := d.Fixed32()
		return err
	default:
		return fmt.Errorf("%w: %d", ErrBadWireType, int(t))
	}
}

// PackedVarints decodes a length-delimited packed repeated varint field.
func (d *Decoder) PackedVarints() ([]uint64, error) {
	p, err := d.Bytes()
	if err != nil {
		return nil, err
	}
	sub := NewDecoder(p)
	var out []uint64
	for !sub.EOF() {
		v, err := sub.Varint()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// PackedZigZag64 decodes a packed repeated sint64 field.
func (d *Decoder) PackedZigZag64() ([]int64, error) {
	us, err := d.PackedVarints()
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(us))
	for i, u := range us {
		out[i] = DecodeZigZag64(u)
	}
	return out, nil
}
