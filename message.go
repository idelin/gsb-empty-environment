package wiremsg

import (
	"errors"
	"fmt"
	"slices"
)

// Budget bounds decode/reschema work. A zero field means "unlimited".
type Budget struct {
	MaxFields     int   // total segments across all nesting levels
	MaxDepth      int   // maximum KindMessage nesting depth (top level = 1)
	MaxValueBytes int64 // cumulative bytes of decoded String/Bytes values
}

// Usage reports what a decode actually consumed.
type Usage struct {
	Fields     int
	Depth      int
	ValueBytes int64
}

var (
	ErrBudgetFields = errors.New("wiremsg: budget exceeded: too many fields")
	ErrBudgetDepth  = errors.New("wiremsg: budget exceeded: nesting too deep")
	ErrBudgetBytes  = errors.New("wiremsg: budget exceeded: value bytes")
)

// ConflictError is returned when a singular known field occurs more than
// once. It is never resolved by silently keeping the last occurrence.
type ConflictError struct{ Num uint32 }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("wiremsg: field %d occurs more than once but is singular", e.Num)
}

// WireMismatchError is returned when a known field's wire type differs from
// its schema kind.
type WireMismatchError struct {
	Num  uint32
	Want Kind
	Got  int
}

func (e *WireMismatchError) Error() string {
	return fmt.Sprintf("wiremsg: field %d: wire type %d does not match kind %d", e.Num, e.Got, e.Want)
}

// Value is a typed, decoded known-field value.
type Value struct {
	Kind  Kind
	U64   uint64   // KindVarint, KindFixed64
	U32   uint32   // KindFixed32
	Bytes []byte   // KindBytes: shared subslice of the decode input
	Str   string   // KindString
	Msg   *Message // KindMessage
}

// Segment is one field occurrence, in wire order. Unknown segments keep
// their complete raw encoding (tag + payload) as a shared, zero-copy
// subslice of the decode input.
type Segment struct {
	Num   uint32
	Known bool
	Value Value  // valid when Known
	Raw   []byte // valid when !Known; shares backing memory with the input
}

// Message is an immutable decoded document. All mutating methods return a
// new Message; on error the receiver is guaranteed to be untouched
// (failure atomicity).
type Message struct {
	schema *Schema
	segs   []Segment
}

// Schema returns the schema the message was decoded with.
func (m *Message) Schema() *Schema { return m.schema }

// Segments returns a copy of the segment list (segment Raw/Bytes slices
// still share the decode input).
func (m *Message) Segments() []Segment { return slices.Clone(m.segs) }

// Decode parses data according to schema under budget b. Unknown fields are
// kept as raw shared subslices; nested KindMessage payloads are decoded
// recursively at depth+1.
func Decode(data []byte, schema *Schema, b Budget) (*Message, Usage, error) {
	u := &Usage{}
	m, err := decode(data, schema, b, u, 1)
	if err != nil {
		return nil, *u, err
	}
	return m, *u, nil
}

func decode(data []byte, schema *Schema, b Budget, u *Usage, depth int) (*Message, error) {
	if b.MaxDepth != 0 && depth > b.MaxDepth {
		return nil, ErrBudgetDepth
	}
	if depth > u.Depth {
		u.Depth = depth
	}
	m := &Message{schema: schema}
	seen := map[uint32]bool{}
	off := 0
	for off < len(data) {
		if b.MaxFields != 0 && u.Fields >= b.MaxFields {
			return nil, ErrBudgetFields
		}
		start := off
		tag, n, err := consumeVarint(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		num := uint32(tag >> 3)
		wt := int(tag & 7)
		if num == 0 {
			return nil, ErrBadTag
		}
		fs, known := schema.lookup(num)
		if !known {
			end, err := skipValue(data, off, wt)
			if err != nil {
				return nil, err
			}
			m.segs = append(m.segs, Segment{Num: num, Raw: data[start:end]})
			off = end
		} else {
			if wt != fs.Kind.wireType() {
				return nil, &WireMismatchError{Num: num, Want: fs.Kind, Got: wt}
			}
			if !fs.Repeated {
				if seen[num] {
					return nil, &ConflictError{Num: num}
				}
				seen[num] = true
			}
			v, n2, err := parseValue(data, off, fs, b, u, depth)
			if err != nil {
				return nil, err
			}
			off += n2
			m.segs = append(m.segs, Segment{Num: num, Known: true, Value: v})
		}
		u.Fields++
	}
	return m, nil
}

// parseValue decodes one known value at off. Every declared length is
// checked against the remaining input before any slice or allocation.
func parseValue(data []byte, off int, fs FieldSchema, b Budget, u *Usage, depth int) (Value, int, error) {
	switch fs.Kind {
	case KindVarint:
		v, n, err := consumeVarint(data[off:])
		return Value{Kind: KindVarint, U64: v}, n, err
	case KindFixed64:
		if len(data)-off < 8 {
			return Value{}, 0, ErrTruncated
		}
		v := uint64(data[off]) | uint64(data[off+1])<<8 | uint64(data[off+2])<<16 | uint64(data[off+3])<<24 |
			uint64(data[off+4])<<32 | uint64(data[off+5])<<40 | uint64(data[off+6])<<48 | uint64(data[off+7])<<56
		return Value{Kind: KindFixed64, U64: v}, 8, nil
	case KindFixed32:
		if len(data)-off < 4 {
			return Value{}, 0, ErrTruncated
		}
		v := uint32(data[off]) | uint32(data[off+1])<<8 | uint32(data[off+2])<<16 | uint32(data[off+3])<<24
		return Value{Kind: KindFixed32, U32: v}, 4, nil
	case KindBytes, KindString, KindMessage:
		l, n, err := consumeVarint(data[off:])
		if err != nil {
			return Value{}, 0, err
		}
		if l > uint64(len(data)-off-n) {
			return Value{}, 0, ErrLength
		}
		payload := data[off+n : off+n+int(l)]
		if b.MaxValueBytes != 0 {
			if u.ValueBytes+int64(l) > b.MaxValueBytes {
				return Value{}, 0, ErrBudgetBytes
			}
		}
		u.ValueBytes += int64(l)
		switch fs.Kind {
		case KindBytes:
			return Value{Kind: KindBytes, Bytes: payload}, n + int(l), nil
		case KindString:
			return Value{Kind: KindString, Str: string(payload)}, n + int(l), nil
		default:
			sub, err := decode(payload, fs.Nested, b, u, depth+1)
			if err != nil {
				return Value{}, 0, err
			}
			return Value{Kind: KindMessage, Msg: sub}, n + int(l), nil
		}
	}
	return Value{}, 0, ErrBadWireType
}

// Encode serializes the message. Unknown segments are emitted verbatim;
// known segments are re-encoded from their typed values, so offsets of
// following segments are recomputed naturally.
func (m *Message) Encode() []byte {
	out := make([]byte, 0, m.encodedLen())
	for _, s := range m.segs {
		out = appendSegment(out, s)
	}
	return out
}

func (m *Message) encodedLen() int {
	n := 0
	for _, s := range m.segs {
		n += segmentLen(s)
	}
	return n
}

func segmentLen(s Segment) int {
	if !s.Known {
		return len(s.Raw)
	}
	return len(appendSegment(nil, s))
}

func appendSegment(out []byte, s Segment) []byte {
	if !s.Known {
		return append(out, s.Raw...)
	}
	out = appendVarint(out, uint64(s.Num)<<3|uint64(s.Value.Kind.wireType()))
	switch s.Value.Kind {
	case KindVarint:
		return appendVarint(out, s.Value.U64)
	case KindFixed64:
		v := s.Value.U64
		return append(out, byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
			byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
	case KindFixed32:
		v := s.Value.U32
		return append(out, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
	case KindBytes:
		out = appendVarint(out, uint64(len(s.Value.Bytes)))
		return append(out, s.Value.Bytes...)
	case KindString:
		out = appendVarint(out, uint64(len(s.Value.Str)))
		return append(out, s.Value.Str...)
	case KindMessage:
		body := s.Value.Msg.Encode()
		out = appendVarint(out, uint64(len(body)))
		return append(out, body...)
	}
	return out
}

// Offsets returns the byte offset of each segment in Encode() output.
// After a known field changes length, later (unknown) offsets shift; this
// is the recomputed source of truth.
func (m *Message) Offsets() []int {
	offs := make([]int, len(m.segs))
	pos := 0
	for i, s := range m.segs {
		offs[i] = pos
		pos += segmentLen(s)
	}
	return offs
}

// Get returns the first known value for num.
func (m *Message) Get(num uint32) (Value, bool) {
	for _, s := range m.segs {
		if s.Known && s.Num == num {
			return s.Value, true
		}
	}
	return Value{}, false
}

// Set replaces the first known occurrence of num in place (position
// preserved) or appends a new segment at the end if absent. The field must
// be known in the message schema and the value kind must match.
func (m *Message) Set(num uint32, v Value) (*Message, error) {
	fs, ok := m.schema.lookup(num)
	if !ok {
		return nil, fmt.Errorf("wiremsg: field %d is not known in this schema", num)
	}
	if v.Kind != fs.Kind {
		return nil, fmt.Errorf("wiremsg: field %d: value kind %d does not match schema kind %d", num, v.Kind, fs.Kind)
	}
	segs := slices.Clone(m.segs)
	for i := range segs {
		if segs[i].Known && segs[i].Num == num {
			segs[i].Value = v
			return &Message{schema: m.schema, segs: segs}, nil
		}
	}
	segs = append(segs, Segment{Num: num, Known: true, Value: v})
	return &Message{schema: m.schema, segs: segs}, nil
}

// Append adds one occurrence of a repeated known field at the end.
func (m *Message) Append(num uint32, v Value) (*Message, error) {
	fs, ok := m.schema.lookup(num)
	if !ok {
		return nil, fmt.Errorf("wiremsg: field %d is not known in this schema", num)
	}
	if !fs.Repeated {
		return nil, fmt.Errorf("wiremsg: field %d is singular; use Set", num)
	}
	if v.Kind != fs.Kind {
		return nil, fmt.Errorf("wiremsg: field %d: value kind %d does not match schema kind %d", num, v.Kind, fs.Kind)
	}
	segs := append(slices.Clone(m.segs), Segment{Num: num, Known: true, Value: v})
	return &Message{schema: m.schema, segs: segs}, nil
}

// Delete removes all known occurrences of num. Unknown segments are never
// touched.
func (m *Message) Delete(num uint32) *Message {
	segs := slices.DeleteFunc(slices.Clone(m.segs), func(s Segment) bool {
		return s.Known && s.Num == num
	})
	return &Message{schema: m.schema, segs: segs}
}

// Reschema re-interprets the message under ns, atomically:
//
//   - known -> known:    value kept (kind change is an error)
//   - unknown -> known:  raw bytes are parsed; multiple occurrences of a
//     singular field are a ConflictError, never last-wins
//   - known -> unknown:  value is re-encoded to a raw segment in place
//   - unknown -> unknown: raw segment kept verbatim
//
// On any error the receiver is unchanged and nil is returned.
func (m *Message) Reschema(ns *Schema, b Budget) (*Message, error) {
	return m.reschema(ns, b, &Usage{}, 1)
}

func (m *Message) reschema(ns *Schema, b Budget, u *Usage, depth int) (*Message, error) {
	if b.MaxDepth != 0 && depth > b.MaxDepth {
		return nil, ErrBudgetDepth
	}
	if depth > u.Depth {
		u.Depth = depth
	}
	// First pass: detect singular conflicts among newly-known fields.
	count := map[uint32]int{}
	for _, s := range m.segs {
		if fs, nowKnown := ns.lookup(s.Num); nowKnown && !fs.Repeated {
			count[s.Num]++
			if count[s.Num] > 1 {
				return nil, &ConflictError{Num: s.Num}
			}
		}
	}
	out := &Message{schema: ns}
	for _, s := range m.segs {
		if b.MaxFields != 0 && u.Fields >= b.MaxFields {
			return nil, ErrBudgetFields
		}
		u.Fields++
		nfs, nowKnown := ns.lookup(s.Num)
		switch {
		case s.Known && nowKnown:
			if s.Value.Kind != nfs.Kind {
				return nil, fmt.Errorf("wiremsg: field %d: kind changed from %d to %d", s.Num, s.Value.Kind, nfs.Kind)
			}
			if s.Value.Kind == KindMessage && nfs.Nested != nil {
				sub, err := s.Value.Msg.reschema(nfs.Nested, b, u, depth+1)
				if err != nil {
					return nil, err
				}
				s.Value.Msg = sub
			}
			out.segs = append(out.segs, s)
		case s.Known && !nowKnown:
			raw := appendSegment(nil, Segment{Num: s.Num, Known: true, Value: s.Value})
			out.segs = append(out.segs, Segment{Num: s.Num, Raw: raw})
		case !s.Known && nowKnown:
			segs, err := parseRawAs(s.Num, s.Raw, nfs, b, u, depth)
			if err != nil {
				return nil, err
			}
			out.segs = append(out.segs, segs)
		default:
			out.segs = append(out.segs, s)
		}
	}
	return out, nil
}

// parseRawAs re-decodes one raw unknown segment as a now-known field.
func parseRawAs(num uint32, raw []byte, fs FieldSchema, b Budget, u *Usage, depth int) (Segment, error) {
	tag, n, err := consumeVarint(raw)
	if err != nil {
		return Segment{}, err
	}
	if uint32(tag>>3) != num || int(tag&7) != fs.Kind.wireType() {
		return Segment{}, &WireMismatchError{Num: num, Want: fs.Kind, Got: int(tag & 7)}
	}
	v, used, err := parseValue(raw, n, fs, b, u, depth)
	if err != nil {
		return Segment{}, err
	}
	if n+used != len(raw) {
		return Segment{}, fmt.Errorf("wiremsg: field %d: trailing bytes in raw segment", num)
	}
	return Segment{Num: num, Known: true, Value: v}, nil
}

// Upgrade is Reschema with the intent that ns adds known fields.
func (m *Message) Upgrade(ns *Schema, b Budget) (*Message, error) { return m.Reschema(ns, b) }

// Downgrade is Reschema with the intent that ns drops known fields.
func (m *Message) Downgrade(ns *Schema, b Budget) (*Message, error) { return m.Reschema(ns, b) }

// Compact returns a copy whose raw segments and byte values are freshly
// allocated, detaching the message from the (possibly large) decode input.
func (m *Message) Compact() *Message {
	segs := slices.Clone(m.segs)
	for i := range segs {
		segs[i] = compactSegment(segs[i])
	}
	return &Message{schema: m.schema, segs: segs}
}

func compactSegment(s Segment) Segment {
	if !s.Known {
		s.Raw = slices.Clone(s.Raw)
		return s
	}
	switch s.Value.Kind {
	case KindBytes:
		s.Value.Bytes = slices.Clone(s.Value.Bytes)
	case KindMessage:
		s.Value.Msg = s.Value.Msg.Compact()
	}
	return s
}
