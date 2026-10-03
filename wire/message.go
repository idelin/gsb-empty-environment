package wire

// Field is one decoded protobuf field: its tag and exactly one payload slot.
type Field struct {
	Number  int
	Type    Type
	Varint  uint64 // VarintType
	Fixed32 uint32 // Fixed32Type
	Fixed64 uint64 // Fixed64Type
	Bytes   []byte // BytesType (aliases the input buffer)
}

// ParseMessage decodes every field of a length-delimited message body.
// It is fully bounds-safe: any truncation surfaces as ErrTruncated (or a
// more specific sentinel) rather than an out-of-range read.
func ParseMessage(buf []byte) ([]Field, error) {
	var fields []Field
	for len(buf) > 0 {
		num, wt, n, err := ConsumeTag(buf)
		if err != nil {
			return nil, err
		}
		buf = buf[n:]
		f := Field{Number: num, Type: wt}
		switch wt {
		case VarintType:
			v, m, err := ConsumeVarint(buf)
			if err != nil {
				return nil, err
			}
			f.Varint = v
			buf = buf[m:]
		case Fixed64Type:
			v, m, err := ConsumeFixed64(buf)
			if err != nil {
				return nil, err
			}
			f.Fixed64 = v
			buf = buf[m:]
		case BytesType:
			b, m, err := ConsumeBytes(buf)
			if err != nil {
				return nil, err
			}
			f.Bytes = b
			buf = buf[m:]
		case Fixed32Type:
			v, m, err := ConsumeFixed32(buf)
			if err != nil {
				return nil, err
			}
			f.Fixed32 = v
			buf = buf[m:]
		default:
			return nil, ErrInvalidWireType
		}
		fields = append(fields, f)
	}
	return fields, nil
}

// ConsumePackedVarints decodes a packed repeated varint payload (the Bytes
// of a BytesType field) into individual values.
func ConsumePackedVarints(buf []byte) ([]uint64, error) {
	var out []uint64
	for len(buf) > 0 {
		v, n, err := ConsumeVarint(buf)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		buf = buf[n:]
	}
	return out, nil
}

// ConsumePackedFixed32 decodes a packed repeated fixed32 payload.
func ConsumePackedFixed32(buf []byte) ([]uint32, error) {
	if len(buf)%4 != 0 {
		return nil, ErrTruncated
	}
	out := make([]uint32, 0, len(buf)/4)
	for len(buf) > 0 {
		v, n, _ := ConsumeFixed32(buf)
		out = append(out, v)
		buf = buf[n:]
	}
	return out, nil
}

// ConsumePackedFixed64 decodes a packed repeated fixed64 payload.
func ConsumePackedFixed64(buf []byte) ([]uint64, error) {
	if len(buf)%8 != 0 {
		return nil, ErrTruncated
	}
	out := make([]uint64, 0, len(buf)/8)
	for len(buf) > 0 {
		v, n, _ := ConsumeFixed64(buf)
		out = append(out, v)
		buf = buf[n:]
	}
	return out, nil
}
