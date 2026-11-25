package displaycommit

import (
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"
)

// WireType is intentionally permissive: callers may supply the normalized
// string names or the standard protobuf numeric wire constants.
type WireType = any

const (
	WireVarint          = "varint"
	WireTypeVarint      = WireVarint
	WireFixed64         = "fixed64"
	WireTypeFixed64     = WireFixed64
	WireFixed32         = "fixed32"
	WireTypeFixed32     = WireFixed32
	WireBytes           = "bytes"
	WireTypeBytes       = WireBytes
	WireLengthDelimited = WireBytes
	WireString          = "string"
	WireTypeString      = WireString
	WireText            = "text"
	WireTypeText        = WireText
	WireAddress         = "address"
	WireTypeAddress     = WireAddress
	WireBigInt          = "bigint"
	WireTypeBigInt      = WireBigInt
	WireMessage         = "message"
	WireTypeMessage     = WireMessage
	WireList            = "list"
	WireTypeList        = WireList
)

// Standard protobuf numeric wire types.
const (
	ProtobufWireVarint  = 0
	ProtobufWireFixed64 = 1
	ProtobufWireBytes   = 2
	ProtobufWireFixed32 = 5
)

func NormalizeWire(wire any) string {
	switch w := wire.(type) {
	case nil:
		return "unspecified"
	case string:
		s := strings.ToLower(strings.TrimSpace(w))
		s = strings.ReplaceAll(s, "_", "")
		s = strings.ReplaceAll(s, "-", "")
		switch s {
		case "":
			return "unspecified"
		case "0", "varint", "int", "integer", "bigint", "uint", "uint64":
			if s == "bigint" {
				return WireBigInt
			}
			return WireVarint
		case "1", "fixed64", "uint64le":
			return WireFixed64
		case "2", "bytes", "lengthdelimited", "string", "text", "address":
			switch s {
			case "string", "text":
				return WireText
			case "address":
				return WireAddress
			default:
				return WireBytes
			}
		case "5", "fixed32":
			return WireFixed32
		case "message", "msg":
			return WireMessage
		case "list", "repeated", "array", "slice":
			return WireList
		default:
			return s
		}
	case int:
		return numericWire(w)
	case int8:
		return numericWire(int(w))
	case int16:
		return numericWire(int(w))
	case int32:
		return numericWire(int(w))
	case int64:
		return numericWire(int(w))
	case uint:
		return numericWire(int(w))
	case uint8:
		return numericWire(int(w))
	case uint16:
		return numericWire(int(w))
	case uint32:
		return numericWire(int(w))
	case uint64:
		return numericWire(int(w))
	default:
		v := reflect.ValueOf(wire)
		switch v.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return numericWire(int(v.Int()))
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return numericWire(int(v.Uint()))
		case reflect.String:
			return NormalizeWire(v.String())
		case reflect.Pointer:
			if v.IsNil() {
				return "unspecified"
			}
			return NormalizeWire(v.Elem().Interface())
		default:
			return fmt.Sprintf("%T", wire)
		}
	}
}

func numericWire(w int) string {
	switch w {
	case 0:
		return WireVarint
	case 1:
		return WireFixed64
	case 2:
		return WireBytes
	case 5:
		return WireFixed32
	default:
		return "wire-" + strconv.Itoa(w)
	}
}

// Kind is the normalized value kind after consulting the type table.
type Kind string

const (
	KindNull    Kind = "null"
	KindInt     Kind = "int"
	KindBool    Kind = "bool"
	KindText    Kind = "text"
	KindAddress Kind = "address"
	KindBytes   Kind = "bytes"
	KindMessage Kind = "message"
	KindList    Kind = "list"
)

// Address keeps the canonical binary identity separate from display text.
type Address struct {
	Chain  string `json:"chain,omitempty"`
	Format string `json:"format,omitempty"`
	Bytes  []byte `json:"bytes,omitempty"`
	Text   string `json:"text,omitempty"`
}

func NewAddress(text string) Address {
	return Address{Text: text}
}

func (a Address) canonical() string {
	if len(a.Bytes) > 0 {
		return strings.ToLower(a.Chain) + ":" + strings.ToLower(a.Format) + ":" + fmt.Sprintf("%x", a.Bytes)
	}
	return strings.ToLower(a.Chain) + ":" + a.Text
}

// Value is a normalized field value.
type Value struct {
	Kind     Kind     `json:"kind,omitempty"`
	Int      *big.Int `json:"int,omitempty"`
	Bool     bool     `json:"bool,omitempty"`
	Text     string   `json:"text,omitempty"`
	Address  Address  `json:"address,omitempty"`
	Bytes    []byte   `json:"bytes,omitempty"`
	Fields   []Field  `json:"fields,omitempty"`
	Elements []Value  `json:"elements,omitempty"`
}

func (v Value) effectiveKind() Kind {
	if v.Kind != "" {
		return v.Kind
	}
	switch {
	case v.Int != nil:
		return KindInt
	case v.Fields != nil:
		return KindMessage
	case v.Elements != nil:
		return KindList
	case v.Address.Text != "" || len(v.Address.Bytes) > 0 || v.Address.Chain != "":
		return KindAddress
	case v.Bytes != nil:
		return KindBytes
	case v.Text != "":
		return KindText
	case v.Bool:
		return KindBool
	default:
		return KindNull
	}
}

func IntValue(v any) Value {
	return Value{Kind: KindInt, Int: AsBigInt(v)}
}

func TextValue(s string) Value {
	return Value{Kind: KindText, Text: s}
}

func AddressValue(addr any) Value {
	switch a := addr.(type) {
	case Address:
		return Value{Kind: KindAddress, Address: a, Text: a.Text}
	case string:
		return Value{Kind: KindAddress, Address: NewAddress(a), Text: a}
	default:
		return Value{Kind: KindAddress, Address: Address{Text: fmt.Sprint(addr)}, Text: fmt.Sprint(addr)}
	}
}

func BoolValue(v bool) Value {
	return Value{Kind: KindBool, Bool: v}
}

func BytesValue(v []byte) Value {
	return Value{Kind: KindBytes, Bytes: append([]byte(nil), v...)}
}

func ListValue(items ...Value) Value {
	return Value{Kind: KindList, Elements: items}
}

func MessageValue(fields ...Field) Value {
	return Value{Kind: KindMessage, Fields: fields}
}

// AsBigInt converts common integer representations without floating point.
func AsBigInt(v any) *big.Int {
	switch x := v.(type) {
	case nil:
		return new(big.Int)
	case *big.Int:
		if x == nil {
			return new(big.Int)
		}
		return new(big.Int).Set(x)
	case big.Int:
		return new(big.Int).Set(&x)
	case string:
		n, ok := new(big.Int).SetString(strings.TrimSpace(x), 0)
		if !ok {
			return new(big.Int)
		}
		return n
	case int:
		return big.NewInt(int64(x))
	case int8:
		return big.NewInt(int64(x))
	case int16:
		return big.NewInt(int64(x))
	case int32:
		return big.NewInt(int64(x))
	case int64:
		return big.NewInt(x)
	case uint:
		return new(big.Int).SetUint64(uint64(x))
	case uint8:
		return new(big.Int).SetUint64(uint64(x))
	case uint16:
		return new(big.Int).SetUint64(uint64(x))
	case uint32:
		return new(big.Int).SetUint64(uint64(x))
	case uint64:
		return new(big.Int).SetUint64(x)
	default:
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return big.NewInt(rv.Int())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return new(big.Int).SetUint64(rv.Uint())
		case reflect.String:
			return AsBigInt(rv.String())
		case reflect.Pointer:
			if rv.IsNil() {
				return new(big.Int)
			}
			return AsBigInt(rv.Elem().Interface())
		default:
			n, _ := new(big.Int).SetString(fmt.Sprint(v), 0)
			if n == nil {
				return new(big.Int)
			}
			return n
		}
	}
}

// Field is one occurrence of a field. Repeated fields use the same Number and
// a dense zero-based Ordinal. A singular field uses Ordinal == -1.
type Field struct {
	Number      int    `json:"number,omitempty"`
	FieldNumber int    `json:"field_number,omitempty"`
	Name        string `json:"name,omitempty"`

	Wire     any `json:"wire,omitempty"`
	WireType any `json:"wire_type,omitempty"`

	Ordinal  int `json:"ordinal,omitempty"`
	Index    int `json:"index,omitempty"`
	Sequence int `json:"sequence,omitempty"`

	Value Value  `json:"value,omitempty"`
	Raw   []byte `json:"raw,omitempty"`

	Commitment      any `json:"commitment,omitempty"`
	ValueCommitment any `json:"value_commitment,omitempty"`
	RawCommitment   any `json:"raw_commitment,omitempty"`
}

func (f Field) number() int {
	if f.Number != 0 {
		return f.Number
	}
	return f.FieldNumber
}

func (f Field) wire() any {
	if f.Wire != nil {
		return f.Wire
	}
	if f.WireType != nil {
		return f.WireType
	}
	return nil
}

func (f Field) ordinal() int {
	if f.Ordinal > 0 || f.Ordinal == -1 {
		return f.Ordinal
	}
	if f.Sequence > 0 || f.Sequence == -1 {
		return f.Sequence
	}
	if f.Index > 0 || f.Index == -1 {
		return f.Index
	}
	return 0
}

func (f Field) commitmentValue() any {
	switch {
	case f.ValueCommitment != nil:
		return f.ValueCommitment
	case f.RawCommitment != nil:
		return f.RawCommitment
	case f.Commitment != nil:
		return f.Commitment
	default:
		return nil
	}
}

// Message is a normalized signed message tree.
type Message struct {
	Type        string  `json:"type,omitempty"`
	TypeName    string  `json:"type_name,omitempty"`
	MessageType string  `json:"message_type,omitempty"`
	Fields      []Field `json:"fields,omitempty"`

	Root     Hash `json:"root,omitempty"`
	RootHash Hash `json:"root_hash,omitempty"`
}

// MessageTree is an descriptive alias retained for callers using the prompt's
// terminology.
type MessageTree = Message

func (m Message) typeName() string {
	for _, s := range []string{m.Type, m.TypeName, m.MessageType} {
		if s != "" {
			return s
		}
	}
	return ""
}

func (m Message) rootOrComputed() Hash {
	if m.Root != ZeroHash {
		return m.Root
	}
	if m.RootHash != ZeroHash {
		return m.RootHash
	}
	return m.ComputeRoot()
}

// PathStep identifies one field occurrence. Index is -1 for a singular field.
type PathStep struct {
	Field int `json:"field"`
	Index int `json:"index"`
}

// Path is a full nested/repeated field locator.
type Path []PathStep

// FieldPath is an alias for Path.
type FieldPath = Path

// P builds a singular path from field numbers.
func P(fields ...int) Path {
	out := make(Path, 0, len(fields))
	for _, n := range fields {
		out = append(out, PathStep{Field: n, Index: -1})
	}
	return out
}

// PI builds a one-step repeated/list path.
func PI(fieldNumber, ordinal int) Path {
	return Path{{Field: fieldNumber, Index: ordinal}}
}

// ParsePath parses "1[0].2" or "1.2[3]" forms. Bare numeric segments are
// singular field numbers; bracket values are ordinals.
func ParsePath(s string) (Path, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("displaycommit: empty path")
	}
	var out Path
	segments := strings.Split(s, ".")
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		fieldPart := seg
		index := -1
		if b := strings.IndexByte(seg, '['); b >= 0 {
			if !strings.HasSuffix(seg, "]") {
				return nil, fmt.Errorf("displaycommit: invalid path segment %q", seg)
			}
			fieldPart = seg[:b]
			idx, err := strconv.Atoi(seg[b+1 : len(seg)-1])
			if err != nil || idx < 0 {
				return nil, fmt.Errorf("displaycommit: invalid ordinal in %q", seg)
			}
			index = idx
		}
		n, err := strconv.Atoi(fieldPart)
		if err != nil {
			return nil, fmt.Errorf("displaycommit: field number %q is not numeric", fieldPart)
		}
		out = append(out, PathStep{Field: n, Index: index})
	}
	return out, nil
}

func NormalizePath(v any) (Path, error) {
	switch p := v.(type) {
	case nil:
		return nil, fmt.Errorf("displaycommit: nil path")
	case Path:
		return append(Path(nil), p...), nil
	case []PathStep:
		return append(Path(nil), p...), nil
	case string:
		return ParsePath(p)
	case int:
		return P(p), nil
	case []int:
		return P(p...), nil
	case []any:
		out := make(Path, 0, len(p))
		for _, item := range p {
			switch x := item.(type) {
			case int:
				out = append(out, PathStep{Field: x, Index: -1})
			case PathStep:
				out = append(out, x)
			case string:
				q, err := ParsePath(x)
				if err != nil {
					return nil, err
				}
				out = append(out, q...)
			default:
				n, err := strconv.Atoi(fmt.Sprint(item))
				if err != nil {
					return nil, err
				}
				out = append(out, PathStep{Field: n, Index: -1})
			}
		}
		return out, nil
	default:
		return ParsePath(fmt.Sprint(v))
	}
}

func (p Path) Key() string {
	var b strings.Builder
	for i, s := range p {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(strconv.Itoa(s.Field))
		if s.Index >= 0 {
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(s.Index))
			b.WriteByte(']')
		}
	}
	return b.String()
}

func (p Path) String() string {
	return p.Key()
}

// Leaf is the compact identity requested by the prompt.
type Leaf struct {
	FieldNumber int `json:"field_number"`
	Number      int `json:"number,omitempty"`

	WireType any `json:"wire_type"`
	Wire     any `json:"wire,omitempty"`

	ValueCommitment    any `json:"value_commitment,omitempty"`
	RawCommitment      any `json:"raw_commitment,omitempty"`
	RawValueCommitment any `json:"raw_value_commitment,omitempty"`
	Commitment         any `json:"commitment,omitempty"`
	CommitmentID       any `json:"commitment_id,omitempty"`
	ValueID            any `json:"value_id,omitempty"`

	Ordinal  int  `json:"ordinal"`
	Index    int  `json:"index,omitempty"`
	Sequence int  `json:"sequence,omitempty"`
	Path     Path `json:"path"`
}

// ID returns the canonical field-number/wire-type/value-commitment tuple.
func (l Leaf) ID() string {
	n := l.FieldNumber
	if n == 0 {
		n = l.Number
	}
	wire := l.WireType
	if wire == nil {
		wire = l.Wire
	}
	return fmt.Sprintf("%d|%s|%s|%s", n, NormalizeWire(wire), l.CommitmentString(), l.Path.Key())
}

// CommitmentString normalizes all supported commitment identifier fields.
func (l Leaf) CommitmentString() string {
	for _, v := range []any{l.ValueCommitment, l.RawValueCommitment, l.RawCommitment, l.Commitment, l.CommitmentID, l.ValueID} {
		if s := suppliedCommitmentID(v); s != "" {
			return s
		}
	}
	return ""
}
