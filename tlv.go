// Package tlv implements a streaming Type-Length-Value codec with
// container nesting, unknown-type passthrough and canonical encoding.
//
// Wire format per field: 2-byte big-endian type, 4-byte big-endian
// length, then length bytes of value. Types with the ContainerFlag bit
// set hold a nested sequence of TLV fields as their value.
package tlv

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

const (
	// HeaderLen is the wire size of a field header: 2-byte type + 4-byte length.
	HeaderLen = 6
	// ContainerFlag marks a type whose value is a nested TLV sequence.
	ContainerFlag uint16 = 0x8000
	// DefaultMaxValueLen caps any single value length; checked before allocation.
	DefaultMaxValueLen = 16 << 20
)

// ErrNeedMoreData reports truncated input. It is distinct from malformed
// input: callers may append more bytes and retry. Test with errors.Is.
var ErrNeedMoreData = errors.New("tlv: need more data")

// Kind classifies a decode Error.
type Kind int

const (
	KindNeedMoreData   Kind = iota // truncated input, retry with more bytes
	KindLengthOverflow             // child length exceeds its parent's boundary
	KindQuotaExceeded              // length exceeds the configured quota
)

// Error is a decode failure at an absolute stream offset.
type Error struct {
	Kind   Kind
	Offset int64
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("tlv: offset %d: %s", e.Offset, e.Detail)
}

// Unwrap lets errors.Is(err, ErrNeedMoreData) match truncated input.
func (e *Error) Unwrap() error {
	if e.Kind == KindNeedMoreData {
		return ErrNeedMoreData
	}
	return nil
}

// IsContainer reports whether t is a container type.
func IsContainer(t uint16) bool { return t&ContainerFlag != 0 }

// Field is one TLV element. Containers use Children; all other types
// (known or unknown) keep their raw Value bytes for lossless passthrough.
type Field struct {
	Type     uint16
	Value    []byte
	Children []*Field
}

// NewPrimitive builds a non-container field.
func NewPrimitive(t uint16, value []byte) *Field {
	return &Field{Type: t, Value: value}
}

// NewContainer builds a container field from child fields.
func NewContainer(t uint16, children ...*Field) *Field {
	return &Field{Type: t | ContainerFlag, Children: children}
}

// valueLen is the encoded value size in bytes.
func (f *Field) valueLen() int {
	if !IsContainer(f.Type) {
		return len(f.Value)
	}
	n := 0
	for _, c := range f.Children {
		n += HeaderLen + c.valueLen()
	}
	return n
}

// Encode appends the field's wire encoding to dst.
func (f *Field) Encode(dst []byte) []byte {
	var hdr [HeaderLen]byte
	binary.BigEndian.PutUint16(hdr[0:2], f.Type)
	binary.BigEndian.PutUint32(hdr[2:6], uint32(f.valueLen()))
	dst = append(dst, hdr[:]...)
	if !IsContainer(f.Type) {
		return append(dst, f.Value...)
	}
	for _, c := range f.Children {
		dst = c.Encode(dst)
	}
	return dst
}

// Encode serializes a field sequence in its current order.
func Encode(fields []*Field) []byte {
	var out []byte
	for _, f := range fields {
		out = f.Encode(out)
	}
	return out
}

// EncodeCanonical serializes fields sorted by type. The sort is stable,
// so the relative order of duplicate types is preserved. Container
// children are canonicalized recursively.
func EncodeCanonical(fields []*Field) []byte {
	sorted := make([]*Field, len(fields))
	copy(sorted, fields)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Type < sorted[j].Type })
	var out []byte
	for _, f := range sorted {
		c := *f
		if IsContainer(c.Type) {
			c.Value = nil
			inner := EncodeCanonical(c.Children)
			c.Children = nil
			var hdr [HeaderLen]byte
			binary.BigEndian.PutUint16(hdr[0:2], c.Type)
			binary.BigEndian.PutUint32(hdr[2:6], uint32(len(inner)))
			out = append(out, hdr[:]...)
			out = append(out, inner...)
			continue
		}
		c.Children = nil
		out = c.Encode(out)
	}
	return out
}

// Parse decodes a complete buffer as a top-level field sequence.
// Truncated input yields an error wrapping ErrNeedMoreData.
func Parse(data []byte) ([]*Field, error) {
	return parseSeq(data, 0, DefaultMaxValueLen)
}

// parseSeq decodes fields from data, where data[0] sits at absolute
// stream offset base. quota caps any single value length.
func parseSeq(data []byte, base int64, quota uint32) ([]*Field, error) {
	var fields []*Field
	pos := 0
	for pos < len(data) {
		f, n, err := parseField(data[pos:], base+int64(pos), quota)
		if err != nil {
			return nil, err
		}
		fields = append(fields, f)
		pos += n
	}
	return fields, nil
}

// parseField decodes one field at the head of data (absolute offset abs)
// and returns it with the consumed byte count.
func parseField(data []byte, abs int64, quota uint32) (*Field, int, error) {
	if len(data) < HeaderLen {
		return nil, 0, &Error{Kind: KindNeedMoreData, Offset: abs,
			Detail: fmt.Sprintf("truncated header: have %d of %d bytes", len(data), HeaderLen)}
	}
	typ := binary.BigEndian.Uint16(data[0:2])
	length := binary.BigEndian.Uint32(data[2:6])
	// Quota is enforced before any length-driven allocation.
	if length > quota {
		return nil, 0, &Error{Kind: KindQuotaExceeded, Offset: abs,
			Detail: fmt.Sprintf("type 0x%04x claims %d bytes, quota is %d", typ, length, quota)}
	}
	if uint64(length) > uint64(len(data)-HeaderLen) {
		return nil, 0, &Error{Kind: KindNeedMoreData, Offset: abs,
			Detail: fmt.Sprintf("type 0x%04x claims %d value bytes, only %d available",
				typ, length, len(data)-HeaderLen)}
	}
	raw := data[HeaderLen : HeaderLen+int(length)]
	f := &Field{Type: typ}
	if IsContainer(typ) {
		// The parent boundary is fixed here, so a child running past it
		// is malformed, not truncated: report its absolute offset.
		children, err := parseContainer(raw, abs+HeaderLen, quota)
		if err != nil {
			return nil, 0, err
		}
		f.Children = children
	} else {
		f.Value = append([]byte(nil), raw...)
	}
	return f, HeaderLen + int(length), nil
}

// parseContainer decodes a container's value, converting child truncation
// into a boundary violation with the child's absolute offset.
func parseContainer(data []byte, base int64, quota uint32) ([]*Field, error) {
	children := []*Field{}
	pos := 0
	for pos < len(data) {
		f, n, err := parseField(data[pos:], base+int64(pos), quota)
		if err != nil {
			var de *Error
			if errors.As(err, &de) && de.Kind == KindNeedMoreData {
				return nil, &Error{Kind: KindLengthOverflow, Offset: de.Offset,
					Detail: "child overruns parent container: " + de.Detail}
			}
			return nil, err
		}
		children = append(children, f)
		pos += n
	}
	return children, nil
}
