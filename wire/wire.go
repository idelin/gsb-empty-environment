// Package wire implements a zero-dependency, hand-rolled Protocol Buffers
// binary wire-format reader/writer: varint (7-bit groups), zigzag, fixed32/64
// and length-delimited fields. Every read is bounds-checked; truncated input
// yields ErrTruncated instead of panicking.
package wire

import "errors"

// WireType is the 3-bit protobuf wire type.
type WireType int

const (
	Varint  WireType = 0 // int32, int64, uint32, uint64, sint, bool, enum
	Fixed64 WireType = 1 // fixed64, sfixed64, double
	Bytes   WireType = 2 // string, bytes, embedded messages, packed repeated
	Fixed32 WireType = 5 // fixed32, sfixed32, float
)

func (t WireType) valid() bool {
	return t == Varint || t == Fixed64 || t == Bytes || t == Fixed32
}

var (
	ErrTruncated      = errors.New("wire: truncated input")
	ErrVarintOverflow = errors.New("wire: varint exceeds 64 bits")
	ErrBadWireType    = errors.New("wire: unsupported wire type")
	ErrBadField       = errors.New("wire: invalid field number")
)

// maxVarintBytes is the maximum encoded length of a 64-bit varint.
const maxVarintBytes = 10

// maxFieldNumber is the largest legal protobuf field number (2^29 - 1).
const maxFieldNumber = 1<<29 - 1

// ZigZagEncode maps a signed integer to an unsigned one so small-magnitude
// negatives stay small: 0->0, -1->1, 1->2, -2->3, ...
func ZigZagEncode(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

// ZigZagDecode is the inverse of ZigZagEncode.
func ZigZagDecode(u uint64) int64 { return int64(u>>1) ^ -int64(u&1) }

// Reader is a bounds-checked cursor over an encoded buffer.
type Reader struct {
	buf []byte
	pos int
}

func NewReader(b []byte) *Reader { return &Reader{buf: b} }

// Remaining reports how many unread bytes are left.
func (r *Reader) Remaining() int { return len(r.buf) - r.pos }

// EOF reports whether the buffer is fully consumed.
func (r *Reader) EOF() bool { return r.pos >= len(r.buf) }

// Offset returns the current read position.
func (r *Reader) Offset() int { return r.pos }

// ReadVarint reads a base-128 varint (7 bits per byte, MSB = continuation).
func (r *Reader) ReadVarint() (uint64, error) {
	var x uint64
	for i := 0; i < maxVarintBytes; i++ {
		if r.pos >= len(r.buf) {
			return 0, ErrTruncated
		}
		b := r.buf[r.pos]
		r.pos++
		if i == maxVarintBytes-1 {
			// 10th byte may only carry a single payload bit and must
			// terminate the varint.
			if b > 1 {
				return 0, ErrVarintOverflow
			}
			x |= uint64(b) << 63
			return x, nil
		}
		x |= uint64(b&0x7f) << uint(7*i)
		if b&0x80 == 0 {
			return x, nil
		}
	}
	return 0, ErrVarintOverflow
}

// ReadZigZag reads a varint and zigzag-decodes it to a signed value.
func (r *Reader) ReadZigZag() (int64, error) {
	u, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return ZigZagDecode(u), nil
}

// ReadFixed32 reads 4 bytes little-endian.
func (r *Reader) ReadFixed32() (uint32, error) {
	if r.Remaining() < 4 {
		return 0, ErrTruncated
	}
	b := r.buf[r.pos:]
	r.pos += 4
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, nil
}

// ReadFixed64 reads 8 bytes little-endian.
func (r *Reader) ReadFixed64() (uint64, error) {
	if r.Remaining() < 8 {
		return 0, ErrTruncated
	}
	b := r.buf[r.pos:]
	r.pos += 8
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56, nil
}

// ReadTag reads a field key: (field number << 3) | wire type.
func (r *Reader) ReadTag() (int, WireType, error) {
	u, err := r.ReadVarint()
	if err != nil {
		return 0, 0, err
	}
	num := u >> 3
	wt := WireType(u & 7)
	if num == 0 || num > maxFieldNumber {
		return 0, 0, ErrBadField
	}
	if !wt.valid() {
		return 0, 0, ErrBadWireType
	}
	return int(num), wt, nil
}

// ReadBytes reads a length-delimited field. The returned slice aliases the
// input buffer; copy it if it must outlive the buffer.
func (r *Reader) ReadBytes() ([]byte, error) {
	n, err := r.ReadVarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Remaining()) {
		return nil, ErrTruncated
	}
	b := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return b, nil
}

// Skip discards one value of the given wire type.
func (r *Reader) Skip(wt WireType) error {
	switch wt {
	case Varint:
		_, err := r.ReadVarint()
		return err
	case Fixed64:
		_, err := r.ReadFixed64()
		return err
	case Fixed32:
		_, err := r.ReadFixed32()
		return err
	case Bytes:
		_, err := r.ReadBytes()
		return err
	default:
		return ErrBadWireType
	}
}

// Buffer accumulates encoded output.
type Buffer struct {
	buf []byte
}

func NewBuffer() *Buffer { return &Buffer{} }

// Bytes returns the encoded output so far.
func (b *Buffer) Bytes() []byte { return b.buf }

// Len returns the number of encoded bytes.
func (b *Buffer) Len() int { return len(b.buf) }

// Reset discards all encoded output.
func (b *Buffer) Reset() { b.buf = b.buf[:0] }

// AppendVarint encodes x as a base-128 varint.
func (b *Buffer) AppendVarint(x uint64) {
	for x >= 0x80 {
		b.buf = append(b.buf, byte(x&0x7f)|0x80)
		x >>= 7
	}
	b.buf = append(b.buf, byte(x))
}

// AppendZigZag encodes a signed integer via zigzag + varint.
func (b *Buffer) AppendZigZag(v int64) { b.AppendVarint(ZigZagEncode(v)) }

// AppendFixed32 appends 4 bytes little-endian.
func (b *Buffer) AppendFixed32(v uint32) {
	b.buf = append(b.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// AppendFixed64 appends 8 bytes little-endian.
func (b *Buffer) AppendFixed64(v uint64) {
	b.buf = append(b.buf,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// AppendTag writes a field key.
func (b *Buffer) AppendTag(num int, wt WireType) {
	b.AppendVarint(uint64(num)<<3 | uint64(wt))
}

// AppendBytes writes a length-delimited field.
func (b *Buffer) AppendBytes(p []byte) {
	b.AppendVarint(uint64(len(p)))
	b.buf = append(b.buf, p...)
}
