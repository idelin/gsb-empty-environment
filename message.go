package wirebuf

import "sync"

// Field is a single decoded protobuf field. The active value depends
// on Wire: Varint for WireVarint, Fixed32/Fixed64 for the fixed types,
// Bytes for WireBytes. Nested messages and packed arrays are decoded
// on demand from Bytes.
type Field struct {
	Number  int
	Wire    WireType
	Varint  uint64
	Fixed32 uint32
	Fixed64 uint64
	Bytes   []byte   // zero-copy view into the source buffer
	Packed  []uint64 // populated by DecodePacked; pooled, released with the message
	Msg     *Message // populated by DecodeNested; pooled, released with the message

	packedBox *U64s // pool handle backing Packed
}

// ZigZag interprets Varint as a ZigZag-encoded signed integer.
func (f *Field) ZigZag() int64 {
	return int64(f.Varint>>1) ^ -int64(f.Varint&1)
}

// Message is a decoded protobuf message: an ordered list of fields.
// Obtain instances via AcquireMessage and return them with Release so
// the memory (including nested messages and packed slices) is reused.
type Message struct {
	Fields []Field
}

var messagePool = sync.Pool{New: func() interface{} {
	return &Message{Fields: make([]Field, 0, 8)}
}}

// AcquireMessage returns a pooled, empty Message.
func AcquireMessage() *Message {
	add(&Stats.MessageAcquire, 1)
	m := messagePool.Get().(*Message)
	m.Fields = m.Fields[:0]
	return m
}

// Release resets the message and returns it — recursively with all
// nested messages and packed slices — to the pools.
func (m *Message) Release() {
	if m == nil {
		return
	}
	for i := range m.Fields {
		f := &m.Fields[i]
		if f.Msg != nil {
			f.Msg.Release()
			f.Msg = nil
		}
		if f.packedBox != nil {
			ReleaseU64s(f.packedBox)
			f.packedBox = nil
			f.Packed = nil
		}
		f.Bytes = nil
	}
	m.Fields = m.Fields[:0]
	messagePool.Put(m)
	add(&Stats.MessageRelease, 1)
}

// Field returns the first field with the given number, or nil.
func (m *Message) Field(num int) *Field {
	for i := range m.Fields {
		if m.Fields[i].Number == num {
			return &m.Fields[i]
		}
	}
	return nil
}

// FieldsByNumber returns all fields with the given number (repeated).
func (m *Message) FieldsByNumber(num int) []*Field {
	var out []*Field
	for i := range m.Fields {
		if m.Fields[i].Number == num {
			out = append(out, &m.Fields[i])
		}
	}
	return out
}

// ParseMessage decodes wire-format data from r into m, which keeps
// ownership of m. Unknown wire types, truncation and overflow are
// reported; the reader position on error is unspecified but never
// out of bounds.
func ParseMessage(r *Reader, m *Message) error {
	for !r.Done() {
		num, wt, err := r.ReadTag()
		if err != nil {
			return err
		}
		m.Fields = append(m.Fields, Field{Number: num, Wire: wt})
		f := &m.Fields[len(m.Fields)-1]
		switch wt {
		case WireVarint:
			if f.Varint, err = r.ReadVarint(); err != nil {
				return err
			}
		case WireFixed64:
			if f.Fixed64, err = r.ReadFixed64(); err != nil {
				return err
			}
		case WireFixed32:
			if f.Fixed32, err = r.ReadFixed32(); err != nil {
				return err
			}
		case WireBytes:
			if f.Bytes, err = r.ReadBytes(); err != nil {
				return err
			}
		default:
			return ErrInvalidWireType
		}
	}
	return nil
}

// Decode parses buf into m in one call.
func Decode(buf []byte, m *Message) error {
	r := AcquireReader(buf)
	defer r.Release()
	return ParseMessage(r, m)
}

// DecodeNested parses the field's Bytes payload as an embedded message
// and stores it in f.Msg (pooled; released with the parent message).
func (f *Field) DecodeNested() (*Message, error) {
	if f.Wire != WireBytes {
		return nil, ErrInvalidWireType
	}
	if f.Msg != nil {
		return f.Msg, nil
	}
	m := AcquireMessage()
	r := AcquireReader(f.Bytes)
	err := ParseMessage(r, m)
	r.Release()
	if err != nil {
		m.Release()
		return nil, err
	}
	f.Msg = m
	return m, nil
}

// DecodePacked parses the field's Bytes payload as a packed repeated
// varint array and stores it in f.Packed (pooled; released with the
// parent message).
func (f *Field) DecodePacked() ([]uint64, error) {
	if f.Wire != WireBytes {
		return nil, ErrInvalidWireType
	}
	if f.Packed != nil {
		return f.Packed, nil
	}
	u := AcquireU64s()
	out, err := ReadPackedVarints(f.Bytes, u.V)
	if err != nil {
		ReleaseU64s(u)
		return nil, err
	}
	u.V = out
	f.packedBox = u
	f.Packed = out
	return out, nil
}
