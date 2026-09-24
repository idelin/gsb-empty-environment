package wiremsg

import (
	"errors"
	"fmt"
)

// ErrConflict is returned when the wire data contradicts the schema: a
// non-repeated known field appearing twice, or a known field carrying a wire
// type that does not match its definition (for example after a schema
// upgrade). Conflicts are never resolved by silently keeping the last value.
var ErrConflict = errors.New("wiremsg: schema conflict")

// ErrUnknownField is returned by Set/Append for field numbers the schema
// does not know; typed mutation of unknown fields is not offered.
var ErrUnknownField = errors.New("wiremsg: unknown field number")

// Field is one entry of a Message. Unknown fields keep their complete raw
// encoding (tag + payload) as a sub-slice of the original parse input:
// zero-copy, shared with the caller's buffer, byte-for-byte preserved.
type Field struct {
	Number uint32
	Known  bool

	// Known fields.
	Def   FieldDef
	Value Value

	// Unknown fields: raw encoding, shared with the parse input buffer.
	Raw []byte
}

// Message is an ordered field list, not a map: repeated and unknown fields
// are never overwritten or reordered.
//
// Field order policy (explicit and total):
//   - Parse records fields in exact wire order.
//   - Marshal emits fields in list order; unknown fields emit Raw verbatim.
//   - Set replaces the first matching known field in place; if none exists
//     the new field is appended at the end.
//   - Append always adds at the end.
//
// Because Marshal re-serializes from this list, offsets of unknown fields
// are recomputed automatically when a known field's encoded length changes.
type Message struct {
	Fields []*Field
	schema *Schema
}

// Parse decodes data according to schema (nil schema = all fields unknown).
// It is atomic: on any error it returns a nil Message and no partial result.
// Unknown fields alias data; callers must not mutate data while the Message
// is in use.
func Parse(schema *Schema, data []byte, b *Budget) (*Message, error) {
	if b == nil {
		b = NewBudget(DefaultLimits)
	}
	return parse(schema, data, b, 0)
}

func parse(schema *Schema, data []byte, b *Budget, depth int) (*Message, error) {
	if depth > b.limits.MaxDepth {
		return nil, fmt.Errorf("wiremsg: nesting depth %d: %w", depth, ErrBudget)
	}
	if err := b.chargeBytes(len(data)); err != nil {
		return nil, err
	}
	m := &Message{schema: schema}
	seen := map[uint32]bool{} // non-repeated known fields already decoded
	off := 0
	for off < len(data) {
		start := off
		num, wt, n, err := readTag(data, off)
		if err != nil {
			return nil, err
		}
		off += n
		def, known := schema.lookup(num)
		switch {
		case known && def.Type.wireType() != wt:
			return nil, fmt.Errorf("wiremsg: field %d (%s): wire type %d, schema wants %d: %w",
				num, def.Name, wt, def.Type.wireType(), ErrConflict)
		case known && !def.Repeated && seen[num]:
			return nil, fmt.Errorf("wiremsg: field %d (%s) is not repeated but appears twice: %w",
				num, def.Name, ErrConflict)
		case known:
			seen[num] = true
			v, n, err := decodeValue(def, wt, data, off, b, depth)
			if err != nil {
				return nil, err
			}
			off += n
			m.Fields = append(m.Fields, &Field{Number: num, Known: true, Def: def, Value: v})
		default:
			// Unknown field: validate the payload length first, then keep
			// the raw bytes (tag included) as a shared sub-slice.
			n, err := payloadLen(wt, data, off)
			if err != nil {
				return nil, err
			}
			off += n
			m.Fields = append(m.Fields, &Field{Number: num, Raw: data[start:off]})
		}
		if err := b.chargeField(); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// decodeValue decodes one known field payload at data[off:]. Length prefixes
// are validated against the remaining input before any slice or allocation.
func decodeValue(def FieldDef, wt int, data []byte, off int, b *Budget, depth int) (Value, int, error) {
	switch def.Type {
	case TypeUint64:
		v, n, err := readVarint(data, off)
		return Value{Uint: v}, n, err
	case TypeFixed32:
		if len(data)-off < 4 {
			return Value{}, 0, ErrTruncated
		}
		v := uint64(data[off]) | uint64(data[off+1])<<8 | uint64(data[off+2])<<16 | uint64(data[off+3])<<24
		return Value{Uint: v}, 4, nil
	case TypeFixed64:
		if len(data)-off < 8 {
			return Value{}, 0, ErrTruncated
		}
		var v uint64
		for i := 0; i < 8; i++ {
			v |= uint64(data[off+i]) << (8 * i)
		}
		return Value{Uint: v}, 8, nil
	case TypeBytes, TypeString, TypeMessage:
		l, n, err := readVarint(data, off)
		if err != nil {
			return Value{}, 0, err
		}
		if l > uint64(len(data)-off-n) {
			return Value{}, 0, fmt.Errorf("wiremsg: field %s: length %d exceeds remaining %d bytes: %w",
				def.Name, l, len(data)-off-n, ErrTruncated)
		}
		body := data[off+n : off+n+int(l)]
		switch def.Type {
		case TypeBytes:
			return Value{Bytes: body}, n + int(l), nil
		case TypeString:
			return Value{Str: string(body)}, n + int(l), nil
		default:
			sub, err := parse(def.Sub, body, b, depth+1)
			if err != nil {
				return Value{}, 0, err
			}
			return Value{Msg: sub}, n + int(l), nil
		}
	}
	return Value{}, 0, fmt.Errorf("wiremsg: field %s: unsupported type", def.Name)
}

// Get returns the typed values of all known fields with the given number, in
// wire order. Unknown fields are not returned.
func (m *Message) Get(num uint32) []Value {
	var out []Value
	for _, f := range m.Fields {
		if f.Known && f.Number == num {
			out = append(out, f.Value)
		}
	}
	return out
}

// Set replaces the value of the first known field with the given number, or
// appends a new field at the end if none exists. It validates before
// mutating: on error the Message is unchanged.
func (m *Message) Set(num uint32, v Value) error {
	def, ok := m.schema.lookup(num)
	if !ok {
		return fmt.Errorf("wiremsg: Set(%d): %w", num, ErrUnknownField)
	}
	if err := def.check(v); err != nil {
		return err
	}
	for _, f := range m.Fields {
		if f.Known && f.Number == num {
			f.Value = v
			return nil
		}
	}
	m.Fields = append(m.Fields, &Field{Number: num, Known: true, Def: def, Value: v})
	return nil
}

// Append adds a known field at the end. It is the only way to add repeated
// fields beyond the first. It validates before mutating.
func (m *Message) Append(num uint32, v Value) error {
	def, ok := m.schema.lookup(num)
	if !ok {
		return fmt.Errorf("wiremsg: Append(%d): %w", num, ErrUnknownField)
	}
	if err := def.check(v); err != nil {
		return err
	}
	if !def.Repeated {
		for _, f := range m.Fields {
			if f.Known && f.Number == num {
				return fmt.Errorf("wiremsg: field %d (%s) is not repeated: %w", num, def.Name, ErrConflict)
			}
		}
	}
	m.Fields = append(m.Fields, &Field{Number: num, Known: true, Def: def, Value: v})
	return nil
}

// Marshal re-serializes the message. Known fields are encoded from their
// typed values; unknown fields are copied verbatim from their shared raw
// slices. It is atomic: on error it returns nil and no partial output.
func (m *Message) Marshal() ([]byte, error) {
	var buf []byte
	for _, f := range m.Fields {
		if !f.Known {
			buf = append(buf, f.Raw...)
			continue
		}
		var err error
		buf, err = appendKnown(buf, f)
		if err != nil {
			return nil, err
		}
	}
	return buf, nil
}

func appendKnown(dst []byte, f *Field) ([]byte, error) {
	if err := f.Def.check(f.Value); err != nil {
		return nil, err
	}
	dst = appendTag(dst, f.Number, f.Def.Type.wireType())
	switch f.Def.Type {
	case TypeUint64:
		return appendVarint(dst, f.Value.Uint), nil
	case TypeFixed32:
		return appendFixed32(dst, uint32(f.Value.Uint)), nil
	case TypeFixed64:
		return appendFixed64(dst, f.Value.Uint), nil
	case TypeBytes:
		dst = appendVarint(dst, uint64(len(f.Value.Bytes)))
		return append(dst, f.Value.Bytes...), nil
	case TypeString:
		dst = appendVarint(dst, uint64(len(f.Value.Str)))
		return append(dst, f.Value.Str...), nil
	case TypeMessage:
		body, err := f.Value.Msg.Marshal()
		if err != nil {
			return nil, err
		}
		dst = appendVarint(dst, uint64(len(body)))
		return append(dst, body...), nil
	}
	return nil, fmt.Errorf("wiremsg: field %s: unsupported type", f.Def.Name)
}
