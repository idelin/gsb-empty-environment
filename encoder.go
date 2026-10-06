package binparse

// Encoder builds files in the binparse format. It is the reference
// generator: anything it emits parses cleanly, and tests derive corrupt
// and truncated inputs from its output.
type Encoder struct {
	order   ByteOrder
	version uint8
	records []*RecordEncoder
}

// NewEncoder returns an Encoder writing files in the given byte order.
func NewEncoder(order ByteOrder) *Encoder {
	return &Encoder{order: order, version: Version}
}

// Record appends a new record and returns its builder.
func (e *Encoder) Record() *RecordEncoder {
	r := &RecordEncoder{enc: e}
	e.records = append(e.records, r)
	return r
}

// Bytes renders the whole file.
func (e *Encoder) Bytes() []byte {
	bo := e.order.bin()
	out := make([]byte, 0, 64)
	out = append(out, Magic...)
	orderByte := byte(LittleEndian)
	if e.order == BigEndian {
		orderByte = byte(BigEndian)
	}
	out = append(out, orderByte, e.version)
	var tmp [4]byte
	bo.PutUint32(tmp[:], uint32(len(e.records)))
	out = append(out, tmp[:]...)
	for _, r := range e.records {
		body := r.encodeBody()
		bo.PutUint32(tmp[:], uint32(len(body)))
		out = append(out, tmp[:]...)
		out = append(out, body...)
	}
	return out
}

type fieldEnc struct {
	tag   uint8
	value []byte
}

// RecordEncoder accumulates the fields of one record in emission order.
type RecordEncoder struct {
	enc    *Encoder
	fields []fieldEnc
}

// ID appends a TagID field.
func (r *RecordEncoder) ID(v uint32) *RecordEncoder {
	var b [4]byte
	r.enc.order.bin().PutUint32(b[:], v)
	return r.append(TagID, b[:])
}

// Name appends a TagName field. Values longer than 65535 bytes are
// truncated to the encodable length.
func (r *RecordEncoder) Name(s string) *RecordEncoder {
	if len(s) > 0xFFFF {
		s = s[:0xFFFF]
	}
	return r.append(TagName, []byte(s))
}

// Flags appends a TagFlags field.
func (r *RecordEncoder) Flags(v uint16) *RecordEncoder {
	var b [2]byte
	r.enc.order.bin().PutUint16(b[:], v)
	return r.append(TagFlags, b[:])
}

// Nested appends a TagNested field whose value is the body built by
// fn, in the same byte order as the enclosing file.
func (r *RecordEncoder) Nested(fn func(*RecordEncoder)) *RecordEncoder {
	child := &RecordEncoder{enc: r.enc}
	fn(child)
	return r.append(TagNested, child.encodeBody())
}

// Unknown appends a field with an unrecognised tag; parsers skip it by
// length.
func (r *RecordEncoder) Unknown(tag uint8, payload []byte) *RecordEncoder {
	return r.append(tag, payload)
}

func (r *RecordEncoder) append(tag uint8, value []byte) *RecordEncoder {
	cp := make([]byte, len(value))
	copy(cp, value)
	r.fields = append(r.fields, fieldEnc{tag: tag, value: cp})
	return r
}

func (r *RecordEncoder) encodeBody() []byte {
	bo := r.enc.order.bin()
	var bitmap uint32
	for _, f := range r.fields {
		if bit, ok := bitForTag(f.tag); ok {
			bitmap |= bit
		}
	}
	out := make([]byte, 0, 16)
	var tmp [4]byte
	bo.PutUint32(tmp[:], bitmap)
	out = append(out, tmp[:]...)
	for _, f := range r.fields {
		out = append(out, f.tag)
		bo.PutUint16(tmp[:2], uint16(len(f.value)))
		out = append(out, tmp[:2]...)
		out = append(out, f.value...)
	}
	return out
}
