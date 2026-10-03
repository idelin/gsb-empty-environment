package wire

import "fmt"

// Field is one decoded protobuf field. Exactly one of the value slots is
// meaningful, selected by Type.
type Field struct {
	Number  int
	Type    WireType
	Varint  uint64 // WireVarint
	Fixed32 uint32 // WireFixed32
	Fixed64 uint64 // WireFixed64
	Bytes   []byte // WireBytes (aliases the input buffer)
}

// ZigZag64 interprets the varint payload as sint64.
func (f Field) ZigZag64() int64 { return DecodeZigZag64(f.Varint) }

// ZigZag32 interprets the varint payload as sint32.
func (f Field) ZigZag32() int32 { return DecodeZigZag32(f.Varint) }

// ParseMessage decodes a whole protobuf message body into its fields.
// Unknown field numbers are preserved; group wire types (3/4) and reserved
// types (6/7) are rejected. Every read is bounds-checked: a truncated
// stream yields ErrTruncated (wrapped with field context), never a panic.
func ParseMessage(buf []byte) ([]Field, error) {
	var fields []Field
	for len(buf) > 0 {
		tag, n, err := ConsumeVarint(buf)
		if err != nil {
			return nil, &Error{Op: "read tag", Err: err}
		}
		buf = buf[n:]

		num := int(tag >> 3)
		wt := WireType(tag & 7)
		if num < 1 || num > MaxFieldNumber {
			return nil, &Error{Op: "read tag", Field: num, Err: ErrBadFieldNumber}
		}

		f := Field{Number: num, Type: wt}
		switch wt {
		case WireVarint:
			v, n, err := ConsumeVarint(buf)
			if err != nil {
				return nil, &Error{Op: "read varint", Field: num, Err: err}
			}
			f.Varint = v
			buf = buf[n:]
		case WireFixed64:
			v, n, err := ConsumeFixed64(buf)
			if err != nil {
				return nil, &Error{Op: "read fixed64", Field: num, Err: err}
			}
			f.Fixed64 = v
			buf = buf[n:]
		case WireBytes:
			l, n, err := ConsumeVarint(buf)
			if err != nil {
				return nil, &Error{Op: "read length", Field: num, Err: err}
			}
			buf = buf[n:]
			if l > uint64(len(buf)) {
				return nil, &Error{Op: "read bytes", Field: num,
					Err: &TruncatedError{Detail: fmt.Sprintf("length prefix wants %d bytes, only %d remain", l, len(buf))}}
			}
			f.Bytes = buf[:l]
			buf = buf[l:]
		case WireFixed32:
			v, n, err := ConsumeFixed32(buf)
			if err != nil {
				return nil, &Error{Op: "read fixed32", Field: num, Err: err}
			}
			f.Fixed32 = v
			buf = buf[n:]
		default:
			return nil, &Error{Op: "read tag", Field: num,
				Err: &AnnotatedError{Err: ErrBadWireType, Detail: fmt.Sprintf("type %d", int(wt))}}
		}
		fields = append(fields, f)
	}
	return fields, nil
}
