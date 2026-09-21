// Package tlv implements a Type-Length-Value wire format with nested
// containers, unknown-type passthrough, canonical serialization, and
// streaming decoding over arbitrarily chunked input.
//
// Wire format (all integers big-endian):
//
//	+------------+------------+===================+
//	| type (4B)  | len  (4B)  | payload (len B)   |
//	+------------+------------+===================+
//
// A type with ContainerFlag set has a payload consisting of concatenated
// child TLVs that must exactly fill the payload. Any other type is opaque:
// its payload is preserved verbatim, so unknown types round-trip unchanged.
package tlv

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// HeaderSize is the fixed header size: 4-byte type + 4-byte length.
	HeaderSize = 8

	// ContainerFlag marks a type whose payload is a sequence of child TLVs.
	ContainerFlag uint32 = 1 << 31

	// MaxLength is the largest accepted payload length. A declared length
	// above this quota is rejected as corrupt before any allocation.
	MaxLength = 1 << 30
)

// ErrIncomplete indicates the input is well-formed so far but more bytes
// are needed. It is never returned for malformed data; see CorruptError.
var ErrIncomplete = errors.New("tlv: incomplete input")

// CorruptError reports malformed input. Offset is the absolute offset in
// the stream (or buffer passed to Parse) of the offending TLV header.
type CorruptError struct {
	Offset int
	Reason string
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("tlv: corrupt data at offset %d: %s", e.Offset, e.Reason)
}

// Value is a decoded TLV. Containers hold Children (in wire order, with
// duplicates preserved); all other types hold the Raw payload verbatim.
type Value struct {
	Type     uint32
	Raw      []byte
	Children []*Value
}

// IsContainer reports whether the type carries child TLVs.
func (v *Value) IsContainer() bool { return v.Type&ContainerFlag != 0 }

// EncodedLen returns the canonical encoded size in bytes.
func (v *Value) EncodedLen() int {
	n := HeaderSize
	if v.IsContainer() {
		for _, c := range v.Children {
			n += c.EncodedLen()
		}
		return n
	}
	return n + len(v.Raw)
}

// AppendTo appends the canonical encoding of v to dst. The declared length
// always equals the exact payload size; container children are emitted in
// their original order.
func (v *Value) AppendTo(dst []byte) []byte {
	payload := len(v.Raw)
	if v.IsContainer() {
		payload = 0
		for _, c := range v.Children {
			payload += c.EncodedLen()
		}
	}
	var hdr [HeaderSize]byte
	binary.BigEndian.PutUint32(hdr[0:4], v.Type)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(payload))
	dst = append(dst, hdr[:]...)
	if v.IsContainer() {
		for _, c := range v.Children {
			dst = c.AppendTo(dst)
		}
		return dst
	}
	return append(dst, v.Raw...)
}

// Encode returns the canonical encoding of v.
func (v *Value) Encode() []byte { return v.AppendTo(nil) }

// Parse decodes exactly one TLV from the front of data, returning the value
// and the number of bytes consumed. A well-formed prefix that is simply too
// short yields an error wrapping ErrIncomplete; malformed data yields a
// *CorruptError with an absolute (data-relative) offset.
func Parse(data []byte) (*Value, int, error) {
	return parse(data, 0, true)
}

// parse decodes one TLV from data, where base is the absolute offset of
// data[0]. At the top level a short payload means "need more data"; inside
// a container the parent boundary is fixed, so a child whose declared
// length overruns it is corrupt.
func parse(data []byte, base int, topLevel bool) (*Value, int, error) {
	if len(data) < HeaderSize {
		if topLevel {
			return nil, 0, fmt.Errorf("%w: need %d more byte(s) for header", ErrIncomplete, HeaderSize-len(data))
		}
		return nil, 0, &CorruptError{
			Offset: base,
			Reason: fmt.Sprintf("%d trailing byte(s) cannot form a header", len(data)),
		}
	}
	typ := binary.BigEndian.Uint32(data[0:4])
	length := binary.BigEndian.Uint32(data[4:8])

	// Quota check before any allocation or recursion.
	if length > MaxLength {
		return nil, 0, &CorruptError{
			Offset: base,
			Reason: fmt.Sprintf("declared length %d exceeds quota %d", length, MaxLength),
		}
	}
	avail := len(data) - HeaderSize
	if uint64(length) > uint64(avail) {
		if topLevel {
			return nil, 0, fmt.Errorf("%w: need %d more byte(s) for payload", ErrIncomplete, int(length)-avail)
		}
		return nil, 0, &CorruptError{
			Offset: base,
			Reason: fmt.Sprintf("declared length %d overruns parent boundary by %d byte(s)", length, int(length)-avail),
		}
	}

	payload := data[HeaderSize : HeaderSize+int(length)]
	v := &Value{Type: typ}
	if typ&ContainerFlag != 0 {
		v.Children = []*Value{} // empty container is legal
		rest := payload
		childBase := base + HeaderSize
		for len(rest) > 0 {
			child, n, err := parse(rest, childBase, false)
			if err != nil {
				return nil, 0, err
			}
			v.Children = append(v.Children, child)
			rest = rest[n:]
			childBase += n
		}
	} else {
		// Copy so the Value does not alias the caller's buffer.
		v.Raw = append([]byte(nil), payload...)
	}
	return v, HeaderSize + int(length), nil
}

// Decoder incrementally decodes a stream of TLVs. Input may be written in
// arbitrary chunks; Next distinguishes "need more data" (ErrIncomplete)
// from "bad data" (*CorruptError with absolute stream offset).
type Decoder struct {
	buf []byte
	off int // absolute stream offset of buf[0]
}

// Write appends a chunk of input.
func (d *Decoder) Write(p []byte) { d.buf = append(d.buf, p...) }

// Next returns the next complete Value. If the buffered bytes are a
// well-formed prefix, the error wraps ErrIncomplete; if they are malformed,
// the error is a *CorruptError carrying the absolute stream offset.
func (d *Decoder) Next() (*Value, error) {
	v, n, err := parse(d.buf, d.off, true)
	if err != nil {
		return nil, err
	}
	d.buf = d.buf[n:]
	d.off += n
	if len(d.buf) == 0 {
		d.buf = nil
	}
	return v, nil
}
