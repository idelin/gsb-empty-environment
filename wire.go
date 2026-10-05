// Package wire implements a streaming parser for an explicit subset of a
// protobuf-style binary wire format. It does not use any existing message
// codec library.
//
// Keys are (field<<3)|wireType varints with field numbers in 1..2^29-1.
// Supported wire types: 0 (uint64 varint, at most 10 bytes), 1 (little
// endian fixed64), 2 (varint length + raw bytes), 5 (little endian
// fixed32). All other wire types are rejected.
package wire

import "encoding/binary"

// Wire types.
const (
	WireVarint  = 0
	WireFixed64 = 1
	WireBytes   = 2
	WireFixed32 = 5
)

// MaxFieldNumber is the largest legal field number: 2^29-1.
const MaxFieldNumber = (1 << 29) - 1

// FieldKind tells the decoder how to interpret a length-delimited
// (wire type 2) field that is declared in the Schema.
type FieldKind int

const (
	// KindRaw keeps the payload as uninterpreted bytes (the default for
	// fields absent from the schema).
	KindRaw FieldKind = iota
	// KindMessage parses the payload as a nested message.
	KindMessage
	// KindPacked parses the payload as a packed array whose element wire
	// type is given by LengthField.ElemWire (0, 1 or 5).
	KindPacked
)

// LengthField declares how one length-delimited field is interpreted.
type LengthField struct {
	Kind     FieldKind
	ElemWire int // element wire type for KindPacked: WireVarint, WireFixed64 or WireFixed32
}

// Schema maps field numbers to the interpretation of their
// length-delimited payload. Fields not present stay raw bytes.
type Schema map[int]LengthField

// Limits bounds a single parse. A zero value means "no limit" for that
// dimension. All limits are checked before any allocation that depends
// on them.
type Limits struct {
	MaxTotalBytes int64 // total bytes accepted across all Feed calls
	MaxFieldLen   int64 // maximum length of a single wire-type-2 payload
	MaxFields     int   // maximum number of fields in the whole message tree
	MaxDepth      int   // maximum nesting depth of nested messages
}

// Field is one decoded field. Which member holds the value depends on
// Wire and, for WireBytes, on Kind.
type Field struct {
	Num  int
	Wire int

	U64 uint64 // WireVarint
	F64 uint64 // WireFixed64
	F32 uint32 // WireFixed32

	Kind       FieldKind // WireBytes only
	Raw        []byte    // WireBytes + KindRaw (unknown fields kept verbatim)
	Msg        *Message  // WireBytes + KindMessage
	Packed     []uint64  // WireBytes + KindPacked
	PackedWire int       // element wire type for KindPacked
}

// Message is an ordered list of fields. Unknown fields are kept in their
// original order so re-encoding never drops them.
type Message struct {
	Fields []Field
}

// AppendVarint appends the canonical varint encoding of v to dst.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// AppendKey appends the key varint for (field, wireType) to dst.
func AppendKey(dst []byte, field, wireType int) []byte {
	return AppendVarint(dst, uint64(field)<<3|uint64(wireType))
}

// Encode re-encodes the message. Values are written in canonical form;
// unknown raw fields are emitted byte-for-byte, so nothing is dropped.
func (m *Message) Encode() []byte {
	return m.appendEncode(nil)
}

func (m *Message) appendEncode(dst []byte) []byte {
	for i := range m.Fields {
		f := &m.Fields[i]
		dst = AppendKey(dst, f.Num, f.Wire)
		switch f.Wire {
		case WireVarint:
			dst = AppendVarint(dst, f.U64)
		case WireFixed64:
			var b [8]byte
			binary.LittleEndian.PutUint64(b[:], f.F64)
			dst = append(dst, b[:]...)
		case WireFixed32:
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], f.F32)
			dst = append(dst, b[:]...)
		case WireBytes:
			switch f.Kind {
			case KindMessage:
				inner := f.Msg.appendEncode(nil)
				dst = AppendVarint(dst, uint64(len(inner)))
				dst = append(dst, inner...)
			case KindPacked:
				inner := appendPacked(nil, f.Packed, f.PackedWire)
				dst = AppendVarint(dst, uint64(len(inner)))
				dst = append(dst, inner...)
			default:
				dst = AppendVarint(dst, uint64(len(f.Raw)))
				dst = append(dst, f.Raw...)
			}
		}
	}
	return dst
}

func appendPacked(dst []byte, vals []uint64, elemWire int) []byte {
	switch elemWire {
	case WireFixed64:
		var b [8]byte
		for _, v := range vals {
			binary.LittleEndian.PutUint64(b[:], v)
			dst = append(dst, b[:]...)
		}
	case WireFixed32:
		var b [4]byte
		for _, v := range vals {
			binary.LittleEndian.PutUint32(b[:], uint32(v))
			dst = append(dst, b[:]...)
		}
	default:
		for _, v := range vals {
			dst = AppendVarint(dst, v)
		}
	}
	return dst
}
