package wire

import "fmt"

// Safety limits applied by the reflection-mode validation engine.
const (
	DefaultMaxDepth = 100       // max nested embedded-message depth
	DefaultMaxLen   = 64 << 20  // max length-delimited payload (64 MiB)
)

// Value is one decoded occurrence of a field.
type Value struct {
	WireType WireType
	Num      uint64 // Varint payload
	Fixed32  uint32
	Fixed64  uint64
	Bytes    []byte // length-delimited payload (sub-slice of the input)
}

// AsZigzag interprets a varint value as a zigzag-encoded sint64.
func (v Value) AsZigzag() int64 { return ZigzagDecode(v.Num) }

// AsBool interprets a varint value as a boolean.
func (v Value) AsBool() bool { return v.Num != 0 }

// Message is a generically decoded protobuf message: every field
// number maps to its occurrences in wire order.
type Message struct {
	Fields map[uint32][]Value
}

// Get returns all occurrences of a field, or nil.
func (m *Message) Get(field uint32) []Value { return m.Fields[field] }

// Last returns the last occurrence of a field (proto3 semantics).
func (m *Message) Last(field uint32) (Value, bool) {
	vs := m.Fields[field]
	if len(vs) == 0 {
		return Value{}, false
	}
	return vs[len(vs)-1], true
}

// Decoder is a reflection-mode validation engine: it parses and
// structurally validates protobuf wire streams without any schema,
// enforcing depth and size limits so hostile input cannot exhaust
// memory or the stack.
type Decoder struct {
	MaxDepth int // max embedded-message nesting depth
	MaxLen   int // max length-delimited payload size in bytes
}

// NewDecoder returns a Decoder with the default safety limits.
func NewDecoder() *Decoder {
	return &Decoder{MaxDepth: DefaultMaxDepth, MaxLen: DefaultMaxLen}
}

// Parse decodes buf as a complete top-level message.
func (d *Decoder) Parse(buf []byte) (*Message, error) {
	return d.parse(buf, 0)
}

// Nested parses a length-delimited value as an embedded message.
// depth is the caller's nesting level (0 for a top-level message);
// the depth limit is enforced against it.
func (d *Decoder) Nested(v Value, depth int) (*Message, error) {
	if v.WireType != Bytes {
		return nil, &FieldError{Err: ErrBadWireType}
	}
	return d.parse(v.Bytes, depth+1)
}

func (d *Decoder) parse(buf []byte, depth int) (*Message, error) {
	maxDepth := d.MaxDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	maxLen := d.MaxLen
	if maxLen <= 0 {
		maxLen = DefaultMaxLen
	}
	if depth > maxDepth {
		return nil, fmt.Errorf("wire: nesting depth exceeds limit %d", maxDepth)
	}
	m := &Message{Fields: make(map[uint32][]Value)}
	for len(buf) > 0 {
		fieldNum, wt, n, err := ConsumeTag(buf)
		if err != nil {
			return nil, err
		}
		buf = buf[n:]
		val := Value{WireType: wt}
		switch wt {
		case Varint:
			v, n, err := ConsumeVarint(buf)
			if err != nil {
				return nil, &FieldError{Field: fieldNum, Err: err}
			}
			val.Num = v
			buf = buf[n:]
		case Fixed64:
			v, n, err := ConsumeFixed64(buf)
			if err != nil {
				return nil, &FieldError{Field: fieldNum, Err: err}
			}
			val.Fixed64 = v
			buf = buf[n:]
		case Fixed32:
			v, n, err := ConsumeFixed32(buf)
			if err != nil {
				return nil, &FieldError{Field: fieldNum, Err: err}
			}
			val.Fixed32 = v
			buf = buf[n:]
		case Bytes:
			l, n, err := ConsumeVarint(buf)
			if err != nil {
				return nil, &FieldError{Field: fieldNum, Err: err}
			}
			buf = buf[n:]
			if l > uint64(maxLen) {
				return nil, &FieldError{Field: fieldNum, Err: ErrBadLength}
			}
			if uint64(len(buf)) < l {
				return nil, &FieldError{Field: fieldNum, Err: &TruncatedError{
					What:  "length-delimited payload",
					Field: fieldNum,
					Need:  int(l) - len(buf),
					Have:  len(buf),
				}}
			}
			val.Bytes = buf[:l:l]
			buf = buf[l:]
		default:
			return nil, &FieldError{Field: fieldNum, Err: ErrBadWireType}
		}
		m.Fields[fieldNum] = append(m.Fields[fieldNum], val)
	}
	return m, nil
}

// DecodePackedVarint decodes a packed repeated varint payload.
func DecodePackedVarint(b []byte) ([]uint64, error) {
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

// DecodePackedZigzag decodes a packed repeated sint64 payload.
func DecodePackedZigzag(b []byte) ([]int64, error) {
	us, err := DecodePackedVarint(b)
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(us))
	for i, u := range us {
		out[i] = ZigzagDecode(u)
	}
	return out, nil
}

// DecodePackedFixed32 decodes a packed repeated fixed32 payload.
func DecodePackedFixed32(b []byte) ([]uint32, error) {
	if len(b)%4 != 0 {
		return nil, &TruncatedError{What: "packed fixed32", Need: len(b) % 4, Have: 0}
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
		return nil, &TruncatedError{What: "packed fixed64", Need: len(b) % 8, Have: 0}
	}
	out := make([]uint64, 0, len(b)/8)
	for len(b) > 0 {
		v, n, _ := ConsumeFixed64(b)
		out = append(out, v)
		b = b[n:]
	}
	return out, nil
}
