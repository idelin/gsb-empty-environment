// Package wire implements a zero-dependency, hand-written Protocol Buffers
// binary wire-format parser. It supports varint (7-bit groups), ZigZag
// signed integers, fixed32/fixed64 and nested length-delimited messages,
// with strict bounds checking against truncated or malformed input.
package wire

import (
	"errors"
	"fmt"
)

// WireType is the 3-bit wire type field of a protobuf tag.
type WireType int

const (
	WireVarint          WireType = 0 // int32, int64, uint32, uint64, sint32, sint64, bool, enum
	WireFixed64         WireType = 1 // fixed64, sfixed64, double
	WireLengthDelimited WireType = 2 // string, bytes, embedded messages, packed repeated
	WireStartGroup      WireType = 3 // deprecated, rejected
	WireEndGroup        WireType = 4 // deprecated, rejected
	WireFixed32         WireType = 5 // fixed32, sfixed32, float
)

// Sentinel errors. Compare with == (Go 1.11 compatible, no errors.Is).
var (
	ErrTruncated   = errors.New("wire: truncated input, safe bounds guard triggered")
	ErrOverflow    = errors.New("wire: varint overflows 64 bits")
	ErrBadWireType = errors.New("wire: unsupported wire type")
	ErrBadTag      = errors.New("wire: invalid field tag")
)

// MaxVarintBytes is the maximum encoded length of a 64-bit varint.
const MaxVarintBytes = 10

// Decoder is a cursor over a protobuf binary buffer. All reads are
// bounds-checked; a short buffer yields ErrTruncated instead of panicking.
type Decoder struct {
	buf []byte
	pos int
}

// NewDecoder returns a Decoder over buf. The buffer is not copied.
func NewDecoder(buf []byte) *Decoder { return &Decoder{buf: buf} }

// EOF reports whether all bytes have been consumed.
func (d *Decoder) EOF() bool { return d.pos >= len(d.buf) }

// Remaining returns the number of unread bytes.
func (d *Decoder) Remaining() int { return len(d.buf) - d.pos }

// Pos returns the current read offset (useful for error reporting).
func (d *Decoder) Pos() int { return d.pos }

// ReadVarint reads a base-128 varint: 7 payload bits per byte, MSB is the
// continuation flag. Returns ErrOverflow if more than 10 bytes are used or
// the 10th byte carries bits beyond 64.
func (d *Decoder) ReadVarint() (uint64, error) {
	var v uint64
	for i := 0; i < MaxVarintBytes; i++ {
		if d.pos >= len(d.buf) {
			return 0, ErrTruncated
		}
		b := d.buf[d.pos]
		d.pos++
		if i == MaxVarintBytes-1 {
			// 10th byte: only 1 payload bit is legal (bit 63).
			if b > 1 {
				return 0, ErrOverflow
			}
		}
		v |= uint64(b&0x7f) << uint(7*i)
		if b < 0x80 {
			return v, nil
		}
	}
	return 0, ErrOverflow
}

// ReadZigZag reads a varint and undoes the ZigZag mapping used by sint32/sint64:
// 0->0, 1->-1, 2->1, 3->-2, ...
func (d *Decoder) ReadZigZag() (int64, error) {
	u, err := d.ReadVarint()
	if err != nil {
		return 0, err
	}
	return int64(u>>1) ^ -int64(u&1), nil
}

// ReadFixed32 reads 4 bytes little-endian.
func (d *Decoder) ReadFixed32() (uint32, error) {
	if d.Remaining() < 4 {
		d.pos = len(d.buf)
		return 0, ErrTruncated
	}
	b := d.buf[d.pos:]
	d.pos += 4
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, nil
}

// ReadFixed64 reads 8 bytes little-endian.
func (d *Decoder) ReadFixed64() (uint64, error) {
	if d.Remaining() < 8 {
		d.pos = len(d.buf)
		return 0, ErrTruncated
	}
	b := d.buf[d.pos:]
	d.pos += 8
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56, nil
}

// ReadBytes reads a length-delimited field: a varint length prefix followed
// by that many bytes. The returned slice aliases the input buffer.
func (d *Decoder) ReadBytes() ([]byte, error) {
	n, err := d.ReadVarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(d.Remaining()) {
		d.pos = len(d.buf)
		return nil, ErrTruncated
	}
	out := d.buf[d.pos : d.pos+int(n)]
	d.pos += int(n)
	return out, nil
}

// ReadTag reads a field tag and returns the field number and wire type.
func (d *Decoder) ReadTag() (int, WireType, error) {
	v, err := d.ReadVarint()
	if err != nil {
		return 0, 0, err
	}
	wt := WireType(v & 0x7)
	num := int(v >> 3)
	if num <= 0 {
		return 0, 0, ErrBadTag
	}
	switch wt {
	case WireVarint, WireFixed64, WireLengthDelimited, WireFixed32:
		return num, wt, nil
	default:
		return 0, 0, fmt.Errorf("wire: tag field %d: %v (got %d)", num, ErrBadWireType, wt)
	}
}

// Skip discards one value of the given wire type.
func (d *Decoder) Skip(wt WireType) error {
	switch wt {
	case WireVarint:
		_, err := d.ReadVarint()
		return err
	case WireFixed64:
		_, err := d.ReadFixed64()
		return err
	case WireLengthDelimited:
		_, err := d.ReadBytes()
		return err
	case WireFixed32:
		_, err := d.ReadFixed32()
		return err
	default:
		return ErrBadWireType
	}
}
