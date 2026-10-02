package wire

// Field is one decoded protobuf field. Exactly one of Varint/Fixed32/
// Fixed64/Bytes is meaningful, selected by Type.
type Field struct {
	Number  int
	Type    WireType
	Varint  uint64
	Fixed32 uint32
	Fixed64 uint64
	Bytes   []byte
}

// ParseMessage decodes every field of a wire-format message. Parsing is
// purely manual and bounds-checked: any truncated tag, value or payload
// aborts with ErrTruncated instead of reading out of range.
func ParseMessage(buf []byte) ([]Field, error) {
	var fields []Field
	for len(buf) > 0 {
		tag, n, err := ConsumeVarint(buf)
		if err != nil {
			return nil, err
		}
		buf = buf[n:]

		num, wt, err := SplitTag(tag)
		if err != nil {
			return nil, err
		}

		f := Field{Number: num, Type: wt}
		switch wt {
		case WireVarint:
			v, n, err := ConsumeVarint(buf)
			if err != nil {
				return nil, err
			}
			f.Varint = v
			buf = buf[n:]
		case WireFixed64:
			v, n, err := ConsumeFixed64(buf)
			if err != nil {
				return nil, err
			}
			f.Fixed64 = v
			buf = buf[n:]
		case WireLengthDelimited:
			b, n, err := ConsumeBytes(buf)
			if err != nil {
				return nil, err
			}
			f.Bytes = b
			buf = buf[n:]
		case WireFixed32:
			v, n, err := ConsumeFixed32(buf)
			if err != nil {
				return nil, err
			}
			f.Fixed32 = v
			buf = buf[n:]
		}
		fields = append(fields, f)
	}
	return fields, nil
}

// Nested interprets a length-delimited field payload as an embedded message.
func (f Field) Nested() ([]Field, error) {
	if f.Type != WireLengthDelimited {
		return nil, ErrInvalidWireType
	}
	return ParseMessage(f.Bytes)
}

// ByNumber returns all fields with the given field number (repeated fields
// may appear multiple times on the wire).
func ByNumber(fields []Field, num int) []Field {
	var out []Field
	for _, f := range fields {
		if f.Number == num {
			out = append(out, f)
		}
	}
	return out
}

// DecodePackedVarints decodes a packed repeated varint payload.
func DecodePackedVarints(b []byte) ([]uint64, error) {
	var out []uint64
	for len(b) > 0 {
		v, n, err := ConsumeVarint(b)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		b = b[n:]
	}
	return out, nil
}

// DecodePackedZigZags decodes a packed repeated sint payload.
func DecodePackedZigZags(b []byte) ([]int64, error) {
	raw, err := DecodePackedVarints(b)
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(raw))
	for i, v := range raw {
		out[i] = DecodeZigZag(v)
	}
	return out, nil
}

// DecodePackedFixed32 decodes a packed repeated fixed32 payload.
func DecodePackedFixed32(b []byte) ([]uint32, error) {
	if len(b)%4 != 0 {
		return nil, ErrInvalidPackedLength
	}
	out := make([]uint32, 0, len(b)/4)
	for len(b) > 0 {
		v, n, _ := ConsumeFixed32(b)
		out = append(out, v)
		b = b[n:]
	}
	return out, nil
}

// DecodePackedFixed64 decodes a packed repeated fixed64 payload.
func DecodePackedFixed64(b []byte) ([]uint64, error) {
	if len(b)%8 != 0 {
		return nil, ErrInvalidPackedLength
	}
	out := make([]uint64, 0, len(b)/8)
	for len(b) > 0 {
		v, n, _ := ConsumeFixed64(b)
		out = append(out, v)
		b = b[n:]
	}
	return out, nil
}
