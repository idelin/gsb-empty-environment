package codec

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Value carries one field payload; which member is meaningful is
// determined by the field's declared FieldType.
type Value struct {
	Num     uint64
	Fixed64 uint64
	Fixed32 uint32
	Bytes   []byte
}

// rawField is an unknown field kept verbatim (payload bytes as they
// appeared on the wire) so re-encoding loses nothing.
type rawField struct {
	number int
	wire   int
	data   []byte
}

// Message is a decoded (or to-be-encoded) record bound to a Schema.
type Message struct {
	schema  *Schema
	values  map[int][]Value
	unknown []rawField
}

func (s *Schema) NewMessage() *Message {
	return &Message{schema: s, values: make(map[int][]Value)}
}

func (m *Message) check(num int, want FieldType) error {
	def, ok := m.schema.fields[num]
	if !ok {
		return fmt.Errorf("codec: field %d not defined in schema %q v%d", num, m.schema.name, m.schema.version)
	}
	if def.Type != want {
		return fmt.Errorf("codec: field %d (%s) is %s, not %s", num, def.Name, def.Type, want)
	}
	return nil
}

func (m *Message) put(num int, v Value, appendMode bool) error {
	def := m.schema.fields[num]
	if appendMode {
		if !def.Repeated {
			return fmt.Errorf("codec: field %d (%s) is not repeated", num, def.Name)
		}
		m.values[num] = append(m.values[num], v)
	} else {
		m.values[num] = []Value{v}
	}
	return nil
}

func (m *Message) SetUint64(num int, v uint64) error {
	if err := m.check(num, FVarint); err != nil {
		return err
	}
	return m.put(num, Value{Num: v}, false)
}

func (m *Message) SetInt64(num int, v int64) error {
	return m.SetUint64(num, uint64(v))
}

func (m *Message) SetBool(num int, v bool) error {
	var u uint64
	if v {
		u = 1
	}
	return m.SetUint64(num, u)
}

func (m *Message) SetFloat64(num int, v float64) error {
	if err := m.check(num, FFixed64); err != nil {
		return err
	}
	return m.put(num, Value{Fixed64: math.Float64bits(v)}, false)
}

func (m *Message) SetFloat32(num int, v float32) error {
	if err := m.check(num, FFixed32); err != nil {
		return err
	}
	return m.put(num, Value{Fixed32: math.Float32bits(v)}, false)
}

func (m *Message) SetBytes(num int, b []byte) error {
	if err := m.check(num, FBytes); err != nil {
		return err
	}
	if len(b) > MaxFieldSize {
		return ErrTooLarge
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	return m.put(num, Value{Bytes: cp}, false)
}

func (m *Message) SetString(num int, s string) error {
	return m.SetBytes(num, []byte(s))
}

// SetNested encodes child with its own schema and stores the bytes.
func (m *Message) SetNested(num int, child *Message) error {
	b, err := child.Encode()
	if err != nil {
		return err
	}
	return m.SetBytes(num, b)
}

// Append adds one occurrence to a repeated field.
func (m *Message) Append(num int, v Value) error {
	def, ok := m.schema.fields[num]
	if !ok {
		return fmt.Errorf("codec: field %d not defined in schema %q v%d", num, m.schema.name, m.schema.version)
	}
	switch def.Type {
	case FBytes:
		if len(v.Bytes) > MaxFieldSize {
			return ErrTooLarge
		}
		cp := make([]byte, len(v.Bytes))
		copy(cp, v.Bytes)
		v.Bytes = cp
	}
	return m.put(num, v, true)
}

func (m *Message) AppendString(num int, s string) error {
	if err := m.check(num, FBytes); err != nil {
		return err
	}
	return m.Append(num, Value{Bytes: []byte(s)})
}

func (m *Message) AppendUint64(num int, v uint64) error {
	if err := m.check(num, FVarint); err != nil {
		return err
	}
	return m.Append(num, Value{Num: v})
}

func (m *Message) first(num int) (Value, bool) {
	vs := m.values[num]
	if len(vs) == 0 {
		return Value{}, false
	}
	return vs[0], true
}

func (m *Message) GetUint64(num int) (uint64, bool) {
	v, ok := m.first(num)
	return v.Num, ok
}

func (m *Message) GetInt64(num int) (int64, bool) {
	v, ok := m.first(num)
	return int64(v.Num), ok
}

func (m *Message) GetBool(num int) (bool, bool) {
	v, ok := m.first(num)
	return v.Num != 0, ok
}

func (m *Message) GetFloat64(num int) (float64, bool) {
	v, ok := m.first(num)
	return math.Float64frombits(v.Fixed64), ok
}

func (m *Message) GetFloat32(num int) (float32, bool) {
	v, ok := m.first(num)
	return math.Float32frombits(v.Fixed32), ok
}

func (m *Message) GetBytes(num int) ([]byte, bool) {
	v, ok := m.first(num)
	if !ok {
		return nil, false
	}
	cp := make([]byte, len(v.Bytes))
	copy(cp, v.Bytes)
	return cp, true
}

func (m *Message) GetString(num int) (string, bool) {
	v, ok := m.first(num)
	if !ok {
		return "", false
	}
	return string(v.Bytes), true
}

// GetNested decodes a nested message field using childSchema.
func (m *Message) GetNested(num int, childSchema *Schema) (*Message, *Diagnostics, error) {
	b, ok := m.GetBytes(num)
	if !ok {
		return nil, nil, fmt.Errorf("codec: field %d not present", num)
	}
	return childSchema.Decode(b)
}

// GetAll returns every occurrence of a repeated field.
func (m *Message) GetAll(num int) []Value {
	vs := m.values[num]
	out := make([]Value, len(vs))
	copy(out, vs)
	return out
}

// UnknownFieldCount reports how many unrecognized fields were retained
// during decoding (they will be re-emitted verbatim on Encode).
func (m *Message) UnknownFieldCount() int { return len(m.unknown) }

// Encode serializes the message: version header, known fields sorted by
// number, then retained unknown fields in their original order.
func (m *Message) Encode() ([]byte, error) {
	out := binary.AppendUvarint(nil, uint64(m.schema.version))
	nums := make([]int, 0, len(m.values))
	for num := range m.values {
		nums = append(nums, num)
	}
	sort.Ints(nums)
	for _, num := range nums {
		def := m.schema.fields[num]
		for _, v := range m.values[num] {
			out = appendTag(out, num, def.Type.wireType())
			out = appendValue(out, def.Type, v)
		}
	}
	for _, u := range m.unknown {
		out = appendTag(out, u.number, u.wire)
		if u.wire == wireBytes {
			out = binary.AppendUvarint(out, uint64(len(u.data)))
		}
		out = append(out, u.data...)
	}
	if len(out) > MaxMessageSize {
		return nil, ErrTooLarge
	}
	return out, nil
}

func appendValue(dst []byte, ft FieldType, v Value) []byte {
	switch ft {
	case FVarint:
		return binary.AppendUvarint(dst, v.Num)
	case FFixed64:
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], v.Fixed64)
		return append(dst, b[:]...)
	case FFixed32:
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], v.Fixed32)
		return append(dst, b[:]...)
	case FBytes:
		dst = binary.AppendUvarint(dst, uint64(len(v.Bytes)))
		return append(dst, v.Bytes...)
	}
	return dst
}

// Diagnostics reports version and compatibility observations from a
// successful (or partially successful) decode.
type Diagnostics struct {
	EncodedVersion uint32
	SchemaVersion  uint32
	UnknownFields  int
	Notes          []string
}

func (d *Diagnostics) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "encoded=v%d local=v%d unknown_fields=%d", d.EncodedVersion, d.SchemaVersion, d.UnknownFields)
	for _, n := range d.Notes {
		fmt.Fprintf(&b, "\n  note: %s", n)
	}
	return b.String()
}

// MissingRequiredError names every required field that was absent,
// by stable number and name.
type MissingRequiredError struct {
	Fields []FieldDef
}

func (e *MissingRequiredError) Error() string {
	parts := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		parts[i] = fmt.Sprintf("#%d (%s)", f.Number, f.Name)
	}
	return "codec: missing required field(s): " + strings.Join(parts, ", ")
}

// Decode parses data according to the schema. It validates every
// declared length against the remaining input before allocating, caps
// field count and message size, retains unknown fields verbatim, and
// checks required fields at the end.
func (s *Schema) Decode(data []byte) (*Message, *Diagnostics, error) {
	diag := &Diagnostics{SchemaVersion: s.version}
	if len(data) > MaxMessageSize {
		return nil, diag, ErrTooLarge
	}
	off := 0
	ver, err := readUvarint(data, &off)
	if err != nil {
		return nil, diag, fmt.Errorf("codec: reading version header: %w", err)
	}
	diag.EncodedVersion = uint32(ver)
	switch {
	case ver > uint64(s.version):
		diag.Notes = append(diag.Notes, fmt.Sprintf(
			"data encoded by NEWER schema v%d (local v%d); unrecognized fields will be retained on re-encode",
			ver, s.version))
	case ver < uint64(s.version):
		diag.Notes = append(diag.Notes, fmt.Sprintf(
			"data encoded by older schema v%d (local v%d)", ver, s.version))
	}

	m := &Message{schema: s, values: make(map[int][]Value)}
	count := 0
	for off < len(data) {
		count++
		if count > MaxFields {
			return nil, diag, fmt.Errorf("codec: more than %d fields", MaxFields)
		}
		tag, err := readUvarint(data, &off)
		if err != nil {
			return nil, diag, err
		}
		num := int(tag >> 3)
		wire := int(tag & 7)
		if num < 1 || num > MaxFieldNumber {
			return nil, diag, fmt.Errorf("codec: field number %d out of range", num)
		}
		def, known := s.fields[num]
		if known {
			if def.Type.wireType() != wire {
				return nil, diag, fmt.Errorf(
					"codec: field %d (%s): wire type %d conflicts with declared type %s",
					num, def.Name, wire, def.Type)
			}
			v, err := readValue(data, &off, def.Type)
			if err != nil {
				return nil, diag, fmt.Errorf("codec: field %d (%s): %w", num, def.Name, err)
			}
			if def.Repeated {
				m.values[num] = append(m.values[num], v)
			} else {
				m.values[num] = []Value{v}
			}
		} else {
			raw, err := readRaw(data, &off, wire)
			if err != nil {
				return nil, diag, fmt.Errorf("codec: unknown field %d: %w", num, err)
			}
			m.unknown = append(m.unknown, rawField{number: num, wire: wire, data: raw})
		}
	}
	diag.UnknownFields = len(m.unknown)

	var missing []FieldDef
	for _, def := range s.fields {
		if def.Required {
			if _, ok := m.values[def.Number]; !ok {
				missing = append(missing, def)
			}
		}
	}
	if len(missing) > 0 {
		sort.Slice(missing, func(i, j int) bool { return missing[i].Number < missing[j].Number })
		return nil, diag, &MissingRequiredError{Fields: missing}
	}
	return m, diag, nil
}

func readValue(buf []byte, off *int, ft FieldType) (Value, error) {
	switch ft {
	case FVarint:
		v, err := readUvarint(buf, off)
		return Value{Num: v}, err
	case FFixed64:
		if len(buf)-*off < 8 {
			return Value{}, ErrTruncated
		}
		v := binary.LittleEndian.Uint64(buf[*off:])
		*off += 8
		return Value{Fixed64: v}, nil
	case FFixed32:
		if len(buf)-*off < 4 {
			return Value{}, ErrTruncated
		}
		v := binary.LittleEndian.Uint32(buf[*off:])
		*off += 4
		return Value{Fixed32: v}, nil
	case FBytes:
		n, err := readLen(buf, off)
		if err != nil {
			return Value{}, err
		}
		b := make([]byte, n)
		copy(b, buf[*off:*off+n])
		*off += n
		return Value{Bytes: b}, nil
	}
	return Value{}, ErrBadWireType
}

// readRaw consumes one field payload of the given wire type and returns
// its exact wire bytes (for varints, the varint itself; for bytes, the
// payload without its length prefix — the prefix is recomputed on
// re-encode, which is lossless because the length is unchanged).
func readRaw(buf []byte, off *int, wire int) ([]byte, error) {
	switch wire {
	case wireVarint:
		start := *off
		if _, err := readUvarint(buf, off); err != nil {
			return nil, err
		}
		return append([]byte(nil), buf[start:*off]...), nil
	case wireFixed64:
		if len(buf)-*off < 8 {
			return nil, ErrTruncated
		}
		b := append([]byte(nil), buf[*off:*off+8]...)
		*off += 8
		return b, nil
	case wireFixed32:
		if len(buf)-*off < 4 {
			return nil, ErrTruncated
		}
		b := append([]byte(nil), buf[*off:*off+4]...)
		*off += 4
		return b, nil
	case wireBytes:
		n, err := readLen(buf, off)
		if err != nil {
			return nil, err
		}
		b := append([]byte(nil), buf[*off:*off+n]...)
		*off += n
		return b, nil
	}
	return nil, ErrBadWireType
}
