// Package schema is a reflection-style validation engine: it decodes a
// protobuf wire buffer against a declarative message descriptor,
// producing typed Go values plus a list of rule violations.
//
// Hard structural errors (truncation, bad tags) abort decoding with an
// error. Semantic problems (missing required fields, out-of-range
// values, wire-type mismatches, unknown fields, excessive nesting) are
// collected as Violations so a single pass reports every problem.
package schema

import (
	"fmt"
	"math"

	"pbufsim/message"
	"pbufsim/wire"
)

// Kind is the logical (schema-level) type of a field.
type Kind int

const (
	KindInt32 Kind = iota
	KindInt64
	KindUint32
	KindUint64
	KindSint32
	KindSint64
	KindBool
	KindEnum
	KindFixed32
	KindFixed64
	KindSfixed32
	KindSfixed64
	KindFloat
	KindDouble
	KindString
	KindBytes
	KindMessage
)

func (k Kind) String() string {
	names := [...]string{
		"int32", "int64", "uint32", "uint64", "sint32", "sint64",
		"bool", "enum", "fixed32", "fixed64", "sfixed32", "sfixed64",
		"float", "double", "string", "bytes", "message",
	}
	if int(k) < len(names) {
		return names[k]
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// wireType returns the canonical (unpacked) wire type of a kind.
func (k Kind) wireType() wire.Type {
	switch k {
	case KindFixed32, KindSfixed32, KindFloat:
		return wire.Fixed32
	case KindFixed64, KindSfixed64, KindDouble:
		return wire.Fixed64
	case KindString, KindBytes, KindMessage:
		return wire.Bytes
	default:
		return wire.Varint
	}
}

// packable reports whether the kind may appear in a packed repeated field.
func (k Kind) packable() bool {
	switch k {
	case KindString, KindBytes, KindMessage:
		return false
	default:
		return true
	}
}

// Field describes one protobuf field for validation.
type Field struct {
	Num      int
	Name     string
	Kind     Kind
	Repeated bool
	Packed   bool // repeated scalar encoded as one length-delimited blob
	Required bool
	Min      *int64   // optional inclusive lower bound (integer kinds)
	Max      *int64   // optional inclusive upper bound (integer kinds)
	MaxLen   int      // 0 = unlimited; caps string/bytes length and repeated count
	Msg      *Message // descriptor for KindMessage
}

// Message describes a protobuf message type.
type Message struct {
	Name   string
	Fields []Field
	byNum  map[int]*Field
}

// NewMessage builds a Message descriptor and indexes its fields.
func NewMessage(name string, fields ...Field) *Message {
	m := &Message{Name: name, Fields: fields, byNum: make(map[int]*Field, len(fields))}
	for i := range m.Fields {
		m.byNum[m.Fields[i].Num] = &m.Fields[i]
	}
	return m
}

// Field returns the descriptor for field number num, or nil.
func (m *Message) Field(num int) *Field { return m.byNum[num] }

// Record is a validated, fully decoded message.
type Record struct {
	Desc   *Message
	Values map[string]any // field name -> decoded value (slices for repeated)
}

// Get returns the decoded value of a field by name.
func (r *Record) Get(name string) (any, bool) {
	v, ok := r.Values[name]
	return v, ok
}

// Violation is a single semantic rule failure.
type Violation struct {
	Path   string // dotted message path, e.g. "Tick.orders[2]"
	Field  string // field name (or "#<num>" for unknown fields)
	Rule   string // short rule identifier
	Detail string // human-readable explanation
}

func (v Violation) String() string {
	return fmt.Sprintf("%s.%s: %s: %s", v.Path, v.Field, v.Rule, v.Detail)
}

// Engine decodes and validates buffers against descriptors.
type Engine struct {
	// MaxDepth caps nested-message recursion (default 64).
	MaxDepth int
	// AllowUnknownFields silently skips undeclared field numbers when
	// true; otherwise they are reported as violations.
	AllowUnknownFields bool
}

func (e *Engine) maxDepth() int {
	if e.MaxDepth <= 0 {
		return 64
	}
	return e.MaxDepth
}

// Decode parses data against desc. A non-nil error means the buffer is
// structurally undecodable (truncated, bad tag, ...). Violations are
// returned regardless.
func (e *Engine) Decode(desc *Message, data []byte) (*Record, []Violation, error) {
	var vs []Violation
	rec, err := e.decode(desc, data, desc.Name, 0, &vs)
	return rec, vs, err
}

func (e *Engine) decode(desc *Message, data []byte, path string, depth int, vs *[]Violation) (*Record, error) {
	if depth > e.maxDepth() {
		*vs = append(*vs, Violation{Path: path, Field: "*", Rule: "max-depth",
			Detail: fmt.Sprintf("nesting exceeds %d levels", e.maxDepth())})
		return &Record{Desc: desc, Values: map[string]any{}}, nil
	}
	gm, err := message.Parse(data)
	if err != nil {
		return nil, err
	}
	rec := &Record{Desc: desc, Values: make(map[string]any, len(desc.Fields))}
	seen := make(map[int]int) // field num -> occurrence count

	for _, gf := range gm.Fields {
		fd := desc.Field(gf.Num)
		if fd == nil {
			if !e.AllowUnknownFields {
				*vs = append(*vs, Violation{Path: path, Field: fmt.Sprintf("#%d", gf.Num),
					Rule: "unknown-field", Detail: "field not declared in descriptor"})
			}
			continue
		}
		seen[gf.Num]++

		if fd.Repeated && fd.Packed && fd.Kind.packable() && gf.Type == wire.Bytes {
			vals, verr := e.decodePacked(fd, gf.Raw)
			if verr != nil {
				return nil, fmt.Errorf("schema: %s.%s: %w", path, fd.Name, verr)
			}
			rec.Values[fd.Name] = appendValue(fd.Kind, rec.Values[fd.Name], vals)
			continue
		}

		if gf.Type != fd.Kind.wireType() {
			*vs = append(*vs, Violation{Path: path, Field: fd.Name, Rule: "wire-type",
				Detail: fmt.Sprintf("got %s, want %s for kind %s", gf.Type, fd.Kind.wireType(), fd.Kind)})
			continue
		}

		val, verr := e.decodeScalar(fd, gf, path, depth, vs)
		if verr != nil {
			return nil, verr
		}
		if fd.Repeated {
			rec.Values[fd.Name] = appendValue(fd.Kind, rec.Values[fd.Name], val)
		} else {
			rec.Values[fd.Name] = val
		}
	}

	for i := range desc.Fields {
		fd := &desc.Fields[i]
		if fd.Required && seen[fd.Num] == 0 {
			*vs = append(*vs, Violation{Path: path, Field: fd.Name, Rule: "required",
				Detail: "required field missing"})
		}
		if fd.Repeated && fd.MaxLen > 0 {
			if n := lenAny(rec.Values[fd.Name]); n > fd.MaxLen {
				*vs = append(*vs, Violation{Path: path, Field: fd.Name, Rule: "max-len",
					Detail: fmt.Sprintf("repeated count %d exceeds %d", n, fd.MaxLen)})
			}
		}
	}
	return rec, nil
}

// decodeScalar converts one generic field into a typed Go value and
// applies per-value rules (range, length).
func (e *Engine) decodeScalar(fd *Field, gf message.Field, path string, depth int, vs *[]Violation) (any, error) {
	switch fd.Kind {
	case KindInt32, KindEnum:
		v := int64(int32(gf.Varint))
		e.checkRange(fd, v, path, vs)
		return v, nil
	case KindInt64:
		v := int64(gf.Varint)
		e.checkRange(fd, v, path, vs)
		return v, nil
	case KindUint32:
		return uint64(uint32(gf.Varint)), nil
	case KindUint64:
		return gf.Varint, nil
	case KindSint32:
		v := int32(wire.DecodeZigZag(gf.Varint))
		e.checkRange(fd, int64(v), path, vs)
		return int64(v), nil
	case KindSint64:
		v := wire.DecodeZigZag(gf.Varint)
		e.checkRange(fd, v, path, vs)
		return v, nil
	case KindBool:
		return gf.Varint != 0, nil
	case KindFixed32:
		return gf.Fixed32, nil
	case KindSfixed32:
		return int32(gf.Fixed32), nil
	case KindFloat:
		return math.Float32frombits(gf.Fixed32), nil
	case KindFixed64:
		return gf.Fixed64, nil
	case KindSfixed64:
		return int64(gf.Fixed64), nil
	case KindDouble:
		return math.Float64frombits(gf.Fixed64), nil
	case KindString:
		s := string(gf.Raw)
		if fd.MaxLen > 0 && len(s) > fd.MaxLen {
			*vs = append(*vs, Violation{Path: path, Field: fd.Name, Rule: "max-len",
				Detail: fmt.Sprintf("string length %d exceeds %d", len(s), fd.MaxLen)})
		}
		return s, nil
	case KindBytes:
		if fd.MaxLen > 0 && len(gf.Raw) > fd.MaxLen {
			*vs = append(*vs, Violation{Path: path, Field: fd.Name, Rule: "max-len",
				Detail: fmt.Sprintf("bytes length %d exceeds %d", len(gf.Raw), fd.MaxLen)})
		}
		b := make([]byte, len(gf.Raw))
		copy(b, gf.Raw)
		return b, nil
	case KindMessage:
		if fd.Msg == nil {
			return nil, fmt.Errorf("schema: %s.%s: message kind without nested descriptor", path, fd.Name)
		}
		sub, err := e.decode(fd.Msg, gf.Raw, path+"."+fd.Name, depth+1, vs)
		if err != nil {
			return nil, fmt.Errorf("schema: %s.%s: %w", path, fd.Name, err)
		}
		return sub, nil
	default:
		return nil, fmt.Errorf("schema: %s.%s: unsupported kind %s", path, fd.Name, fd.Kind)
	}
}

// decodePacked expands one packed length-delimited blob into typed values.
func (e *Engine) decodePacked(fd *Field, raw []byte) (any, error) {
	switch fd.Kind.wireType() {
	case wire.Varint:
		if fd.Kind == KindSint32 || fd.Kind == KindSint64 {
			vs, err := message.UnpackZigZags(raw)
			if err != nil {
				return nil, err
			}
			out := make([]int64, len(vs))
			copy(out, vs)
			return out, nil
		}
		vs, err := message.UnpackVarints(raw)
		if err != nil {
			return nil, err
		}
		switch fd.Kind {
		case KindBool:
			out := make([]bool, len(vs))
			for i, v := range vs {
				out[i] = v != 0
			}
			return out, nil
		case KindInt32, KindEnum:
			out := make([]int64, len(vs))
			for i, v := range vs {
				out[i] = int64(int32(v))
			}
			return out, nil
		case KindInt64:
			out := make([]int64, len(vs))
			for i, v := range vs {
				out[i] = int64(v)
			}
			return out, nil
		case KindUint32:
			out := make([]uint64, len(vs))
			for i, v := range vs {
				out[i] = uint64(uint32(v))
			}
			return out, nil
		default:
			out := make([]uint64, len(vs))
			copy(out, vs)
			return out, nil
		}
	case wire.Fixed32:
		vs, err := message.UnpackFixed32s(raw)
		if err != nil {
			return nil, err
		}
		switch fd.Kind {
		case KindFloat:
			out := make([]float32, len(vs))
			for i, v := range vs {
				out[i] = math.Float32frombits(v)
			}
			return out, nil
		case KindSfixed32:
			out := make([]int32, len(vs))
			for i, v := range vs {
				out[i] = int32(v)
			}
			return out, nil
		default:
			out := make([]uint32, len(vs))
			copy(out, vs)
			return out, nil
		}
	case wire.Fixed64:
		vs, err := message.UnpackFixed64s(raw)
		if err != nil {
			return nil, err
		}
		switch fd.Kind {
		case KindDouble:
			out := make([]float64, len(vs))
			for i, v := range vs {
				out[i] = math.Float64frombits(v)
			}
			return out, nil
		case KindSfixed64:
			out := make([]int64, len(vs))
			for i, v := range vs {
				out[i] = int64(v)
			}
			return out, nil
		default:
			out := make([]uint64, len(vs))
			copy(out, vs)
			return out, nil
		}
	default:
		return nil, fmt.Errorf("kind %s is not packable", fd.Kind)
	}
}

func (e *Engine) checkRange(fd *Field, v int64, path string, vs *[]Violation) {
	if fd.Min != nil && v < *fd.Min {
		*vs = append(*vs, Violation{Path: path, Field: fd.Name, Rule: "range",
			Detail: fmt.Sprintf("value %d below minimum %d", v, *fd.Min)})
	}
	if fd.Max != nil && v > *fd.Max {
		*vs = append(*vs, Violation{Path: path, Field: fd.Name, Rule: "range",
			Detail: fmt.Sprintf("value %d above maximum %d", v, *fd.Max)})
	}
}

// Int64 returns a *int64 helper for building Min/Max bounds.
func Int64(v int64) *int64 { return &v }

// appendValue accumulates a repeated field value. val is either a
// single decoded scalar or a typed slice produced by packed decoding;
// the accumulator type is determined by the field kind.
func appendValue(k Kind, existing any, val any) any {
	switch k {
	case KindInt32, KindInt64, KindSint32, KindSint64, KindEnum:
		s, _ := existing.([]int64)
		if sl, ok := val.([]int64); ok {
			return append(s, sl...)
		}
		return append(s, val.(int64))
	case KindUint32, KindUint64:
		s, _ := existing.([]uint64)
		if sl, ok := val.([]uint64); ok {
			return append(s, sl...)
		}
		return append(s, val.(uint64))
	case KindBool:
		s, _ := existing.([]bool)
		if sl, ok := val.([]bool); ok {
			return append(s, sl...)
		}
		return append(s, val.(bool))
	case KindFixed32:
		s, _ := existing.([]uint32)
		if sl, ok := val.([]uint32); ok {
			return append(s, sl...)
		}
		return append(s, val.(uint32))
	case KindSfixed32:
		s, _ := existing.([]int32)
		if sl, ok := val.([]int32); ok {
			return append(s, sl...)
		}
		return append(s, val.(int32))
	case KindFloat:
		s, _ := existing.([]float32)
		if sl, ok := val.([]float32); ok {
			return append(s, sl...)
		}
		return append(s, val.(float32))
	case KindFixed64:
		s, _ := existing.([]uint64)
		if sl, ok := val.([]uint64); ok {
			return append(s, sl...)
		}
		return append(s, val.(uint64))
	case KindSfixed64:
		s, _ := existing.([]int64)
		if sl, ok := val.([]int64); ok {
			return append(s, sl...)
		}
		return append(s, val.(int64))
	case KindDouble:
		s, _ := existing.([]float64)
		if sl, ok := val.([]float64); ok {
			return append(s, sl...)
		}
		return append(s, val.(float64))
	case KindString:
		s, _ := existing.([]string)
		return append(s, val.(string))
	case KindBytes:
		s, _ := existing.([][]byte)
		return append(s, val.([]byte))
	case KindMessage:
		s, _ := existing.([]*Record)
		return append(s, val.(*Record))
	default:
		return val
	}
}

func lenAny(v any) int {
	switch t := v.(type) {
	case nil:
		return 0
	case []any:
		return len(t)
	case []int64:
		return len(t)
	case []uint64:
		return len(t)
	case []uint32:
		return len(t)
	case []bool:
		return len(t)
	case []float32:
		return len(t)
	case []float64:
		return len(t)
	case []int32:
		return len(t)
	case []string:
		return len(t)
	case [][]byte:
		return len(t)
	case []*Record:
		return len(t)
	default:
		return 1
	}
}
