package wire

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// Kind is the logical protobuf scalar kind declared in a struct tag.
type Kind string

const (
	KindVarint  Kind = "varint"  // uint32/uint64/int32/int64/bool/enum
	KindSint    Kind = "sint"    // ZigZag-encoded int32/int64
	KindFixed32 Kind = "fixed32" // uint32 / float32
	KindFixed64 Kind = "fixed64" // uint64 / float64
	KindBytes   Kind = "bytes"   // []byte
	KindString  Kind = "string"  // string
	KindMessage Kind = "message" // nested struct / *struct
)

// Struct tag format:
//   `pb:"<fieldNumber>,<kind>[,packed][,repeated]"`
//
// Examples:
//   ID     uint64   `pb:"1,varint"`
//   Score  int64    `pb:"2,sint"`
//   Name   string   `pb:"3,string"`
//   Tags   []int64  `pb:"4,sint,packed"`
//   Child  *Node    `pb:"5,message"`
//   Values []uint32 `pb:"6,varint,repeated"` (non-packed, one tag per element)

type fieldSpec struct {
	num      int
	kind     Kind
	packed   bool
	repeated bool
	index    int // struct field index
	goType   reflect.Type
}

// ValidationError reports a schema/reflect mismatch discovered by Validate
// or Unmarshal.
type ValidationError struct {
	Type   string
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("wire: validation: %s.%s: %s", e.Type, e.Field, e.Reason)
}

// WireTypeError reports a field whose wire type does not match its schema.
type WireTypeError struct {
	FieldNum int
	Want     WireType
	Got      WireType
}

func (e *WireTypeError) Error() string {
	return fmt.Sprintf("wire: field %d: wire type mismatch: want %d, got %d", e.FieldNum, e.Want, e.Got)
}

func parseTag(sf reflect.StructField) (*fieldSpec, error) {
	tag, ok := sf.Tag.Lookup("pb")
	if !ok || tag == "" || tag == "-" {
		return nil, nil
	}
	parts := strings.Split(tag, ",")
	if len(parts) < 2 {
		return nil, &ValidationError{Type: sf.Type.String(), Field: sf.Name, Reason: "tag must be \"<num>,<kind>\""}
	}
	num, err := strconv.Atoi(parts[0])
	if err != nil || num <= 0 {
		return nil, &ValidationError{Type: sf.Type.String(), Field: sf.Name, Reason: "invalid field number"}
	}
	spec := &fieldSpec{num: num, kind: Kind(parts[1]), index: sf.Index[0], goType: sf.Type}
	for _, opt := range parts[2:] {
		switch opt {
		case "packed":
			spec.packed = true
			spec.repeated = true
		case "repeated":
			spec.repeated = true
		default:
			return nil, &ValidationError{Type: sf.Type.String(), Field: sf.Name, Reason: "unknown option " + strconv.Quote(opt)}
		}
	}
	return spec, nil
}

// compileSpecs inspects a struct type and returns its field specs, verifying
// that Go types are compatible with the declared protobuf kinds.
func compileSpecs(t reflect.Type) ([]*fieldSpec, error) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, &ValidationError{Type: t.String(), Field: "-", Reason: "target must be a struct or *struct"}
	}
	seen := map[int]string{}
	var specs []*fieldSpec
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		spec, err := parseTag(sf)
		if err != nil {
			return nil, err
		}
		if spec == nil {
			continue
		}
		if prev, dup := seen[spec.num]; dup {
			return nil, &ValidationError{Type: t.Name(), Field: sf.Name,
				Reason: fmt.Sprintf("field number %d already used by %s", spec.num, prev)}
		}
		seen[spec.num] = sf.Name
		if err := checkGoType(t, sf, spec); err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func checkGoType(t reflect.Type, sf reflect.StructField, spec *fieldSpec) error {
	gt := sf.Type
	if spec.repeated {
		if gt.Kind() != reflect.Slice {
			return &ValidationError{Type: t.Name(), Field: sf.Name, Reason: "repeated field must be a slice"}
		}
		gt = gt.Elem()
	}
	ok := false
	switch spec.kind {
	case KindVarint:
		switch gt.Kind() {
		case reflect.Int32, reflect.Int64, reflect.Uint32, reflect.Uint64, reflect.Bool, reflect.Int, reflect.Uint:
			ok = true
		}
	case KindSint:
		ok = gt.Kind() == reflect.Int32 || gt.Kind() == reflect.Int64 || gt.Kind() == reflect.Int
	case KindFixed32:
		ok = gt.Kind() == reflect.Uint32 || gt.Kind() == reflect.Float32
	case KindFixed64:
		ok = gt.Kind() == reflect.Uint64 || gt.Kind() == reflect.Float64
	case KindBytes:
		ok = gt.Kind() == reflect.Slice && gt.Elem().Kind() == reflect.Uint8
	case KindString:
		ok = gt.Kind() == reflect.String
	case KindMessage:
		ok = gt.Kind() == reflect.Struct || (gt.Kind() == reflect.Ptr && gt.Elem().Kind() == reflect.Struct)
	}
	if !ok {
		return &ValidationError{Type: t.Name(), Field: sf.Name,
			Reason: fmt.Sprintf("go type %s incompatible with kind %s", gt, spec.kind)}
	}
	if spec.packed {
		switch spec.kind {
		case KindVarint, KindSint, KindFixed32, KindFixed64:
		default:
			return &ValidationError{Type: t.Name(), Field: sf.Name, Reason: "only numeric kinds can be packed"}
		}
	}
	return nil
}

// Validate checks that v's struct tags form a coherent protobuf schema.
func Validate(v interface{}) error {
	t := reflect.TypeOf(v)
	if t == nil {
		return &ValidationError{Type: "<nil>", Field: "-", Reason: "nil value"}
	}
	_, err := compileSpecs(t)
	return err
}

func expectedWire(k Kind) WireType {
	switch k {
	case KindVarint, KindSint:
		return WireVarint
	case KindFixed32:
		return WireFixed32
	case KindFixed64:
		return WireFixed64
	default:
		return WireLengthDelimited
	}
}

// Unmarshal decodes a wire-format buffer into v, which must be a pointer to
// a struct carrying `pb` tags. Unknown fields are skipped safely. Truncated
// input yields *TruncatedError; schema mismatches yield *ValidationError or
// *WireTypeError.
func Unmarshal(data []byte, v interface{}) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return &ValidationError{Type: fmt.Sprintf("%T", v), Field: "-", Reason: "need non-nil pointer to struct"}
	}
	return unmarshalMessage(data, rv.Elem())
}

func unmarshalMessage(data []byte, sv reflect.Value) error {
	specs, err := compileSpecs(sv.Type())
	if err != nil {
		return err
	}
	byNum := make(map[int]*fieldSpec, len(specs))
	for _, s := range specs {
		byNum[s.num] = s
	}
	r := NewReader(data)
	for !r.Done() {
		num, wt, err := r.ReadTag()
		if err != nil {
			return err
		}
		spec, known := byNum[num]
		if !known {
			if err := skipField(r, wt); err != nil {
				return err
			}
			continue
		}
		if spec.packed && wt == WireLengthDelimited {
			payload, err := r.ReadBytes()
			if err != nil {
				return err
			}
			if err := decodePacked(spec, sv.Field(spec.index), payload); err != nil {
				return err
			}
			continue
		}
		if want := expectedWire(spec.kind); wt != want {
			return &WireTypeError{FieldNum: num, Want: want, Got: wt}
		}
		if err := decodeOne(spec, r, sv.Field(spec.index)); err != nil {
			return err
		}
	}
	return nil
}

func skipField(r *Reader, wt WireType) error {
	switch wt {
	case WireVarint:
		_, err := r.ReadVarint()
		return err
	case WireFixed32:
		_, err := r.ReadFixed32()
		return err
	case WireFixed64:
		_, err := r.ReadFixed64()
		return err
	case WireLengthDelimited:
		_, err := r.ReadBytes()
		return err
	}
	return ErrInvalidWireType
}

func decodeOne(spec *fieldSpec, r *Reader, dst reflect.Value) error {
	if spec.repeated {
		elem := reflect.New(dst.Type().Elem()).Elem()
		if err := decodeScalar(spec, r, elem); err != nil {
			return err
		}
		dst.Set(reflect.Append(dst, elem))
		return nil
	}
	return decodeScalar(spec, r, dst)
}

func decodeScalar(spec *fieldSpec, r *Reader, dst reflect.Value) error {
	switch spec.kind {
	case KindVarint:
		v, err := r.ReadVarint()
		if err != nil {
			return err
		}
		switch dst.Kind() {
		case reflect.Bool:
			dst.SetBool(v != 0)
		case reflect.Int, reflect.Int32, reflect.Int64:
			dst.SetInt(int64(v))
		default:
			dst.SetUint(v)
		}
	case KindSint:
		v, err := r.ReadSVarint()
		if err != nil {
			return err
		}
		dst.SetInt(v)
	case KindFixed32:
		v, err := r.ReadFixed32()
		if err != nil {
			return err
		}
		if dst.Kind() == reflect.Float32 {
			dst.SetFloat(float64(math.Float32frombits(v)))
		} else {
			dst.SetUint(uint64(v))
		}
	case KindFixed64:
		v, err := r.ReadFixed64()
		if err != nil {
			return err
		}
		if dst.Kind() == reflect.Float64 {
			dst.SetFloat(math.Float64frombits(v))
		} else {
			dst.SetUint(v)
		}
	case KindString:
		b, err := r.ReadBytes()
		if err != nil {
			return err
		}
		dst.SetString(string(b))
	case KindBytes:
		b, err := r.ReadBytes()
		if err != nil {
			return err
		}
		cp := make([]byte, len(b))
		copy(cp, b)
		dst.SetBytes(cp)
	case KindMessage:
		b, err := r.ReadBytes()
		if err != nil {
			return err
		}
		target := dst
		if dst.Kind() == reflect.Ptr {
			target = reflect.New(dst.Type().Elem())
			dst.Set(target)
			target = target.Elem()
		}
		return unmarshalMessage(b, target)
	}
	return nil
}

func decodePacked(spec *fieldSpec, dst reflect.Value, payload []byte) error {
	pr := NewReader(payload)
	for !pr.Done() {
		elem := reflect.New(dst.Type().Elem()).Elem()
		if err := decodeScalar(spec, pr, elem); err != nil {
			return err
		}
		dst.Set(reflect.Append(dst, elem))
	}
	return nil
}
