package wire

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// Reflection-mode validation engine.
//
// Struct fields declare their wire mapping with a tag:
//
//	ID     uint64   `wire:"1,varint"`
//	Delta  int64    `wire:"2,sint"`
//	Zip    uint32   `wire:"3,fixed32"`
//	Name   string   `wire:"4,string"`
//	Raw    []byte   `wire:"5,bytes"`
//	Addr   *Address `wire:"6,message"`
//	Scores []int64  `wire:"7,packed,sint"`
//	Refs   []uint64 `wire:"8,varint"` // repeated, non-packed
//
// Supported kinds: varint, sint (ZigZag), fixed32, fixed64, string,
// bytes, message, packed. Validate checks a buffer against the schema
// without decoding; Unmarshal validates and decodes in one pass.

var (
	ErrBadTag           = errors.New("wire: bad struct tag")
	ErrWireTypeMismatch = errors.New("wire: wire type does not match schema")
	ErrBadTarget        = errors.New("wire: out must be a pointer to a struct")
	ErrBadFieldType     = errors.New("wire: struct field type does not fit tag kind")
)

type fieldSpec struct {
	num   int
	kind  string
	elem  string // element kind for packed
	index int
}

func parseTag(tag string) (fieldSpec, error) {
	parts := strings.Split(tag, ",")
	if len(parts) < 2 || len(parts) > 3 {
		return fieldSpec{}, fmt.Errorf("%w: %q", ErrBadTag, tag)
	}
	num, err := strconv.Atoi(parts[0])
	if err != nil || num <= 0 {
		return fieldSpec{}, fmt.Errorf("%w: %q", ErrBadTag, tag)
	}
	spec := fieldSpec{num: num, kind: parts[1]}
	switch spec.kind {
	case "varint", "sint", "fixed32", "fixed64", "string", "bytes", "message":
		if len(parts) != 2 {
			return fieldSpec{}, fmt.Errorf("%w: %q", ErrBadTag, tag)
		}
	case "packed":
		if len(parts) != 3 {
			return fieldSpec{}, fmt.Errorf("%w: %q", ErrBadTag, tag)
		}
		spec.elem = parts[2]
		switch spec.elem {
		case "varint", "sint", "fixed32", "fixed64":
		default:
			return fieldSpec{}, fmt.Errorf("%w: %q", ErrBadTag, tag)
		}
	default:
		return fieldSpec{}, fmt.Errorf("%w: %q", ErrBadTag, tag)
	}
	return spec, nil
}

// wantWire maps a kind to the wire type its payload must arrive on.
func wantWire(kind string) WireType {
	switch kind {
	case "varint", "sint":
		return Varint
	case "fixed32":
		return Fixed32
	case "fixed64":
		return Fixed64
	default: // string, bytes, message, packed
		return LengthDelimited
	}
}

func schemaOf(t reflect.Type) ([]fieldSpec, error) {
	var specs []fieldSpec
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("wire")
		if tag == "" || tag == "-" {
			continue
		}
		spec, err := parseTag(tag)
		if err != nil {
			return nil, err
		}
		spec.index = i
		specs = append(specs, spec)
	}
	return specs, nil
}

// Validate checks data against the schema of out (a pointer to struct)
// without storing anything: wire types must match, nested messages are
// recursed into with a depth limit, and packed fixed-width payloads must
// be element-aligned. Unknown fields are skipped.
func Validate(data []byte, out any) error {
	t := reflect.TypeOf(out)
	if t == nil || t.Kind() != reflect.Pointer || t.Elem().Kind() != reflect.Struct {
		return ErrBadTarget
	}
	return validateMessage(data, t.Elem(), 0)
}

func validateMessage(data []byte, st reflect.Type, depth int) error {
	if depth > MaxDepth {
		return ErrDepthLimit
	}
	fields, err := Parse(data)
	if err != nil {
		return err
	}
	specs, err := schemaOf(st)
	if err != nil {
		return err
	}
	byNum := make(map[int]fieldSpec, len(specs))
	for _, s := range specs {
		byNum[s.num] = s
	}
	for _, f := range fields {
		spec, ok := byNum[f.Number]
		if !ok {
			continue // unknown field: skipped
		}
		if f.Type != wantWire(spec.kind) {
			return &FieldError{Field: f.Number, Err: fmt.Errorf("%w: got wire type %d, want %d", ErrWireTypeMismatch, f.Type, wantWire(spec.kind))}
		}
		switch spec.kind {
		case "message":
			ft := st.Field(spec.index).Type
			if ft.Kind() != reflect.Pointer || ft.Elem().Kind() != reflect.Struct {
				return &FieldError{Field: f.Number, Err: ErrBadFieldType}
			}
			if err := validateMessage(f.Bytes, ft.Elem(), depth+1); err != nil {
				return err
			}
		case "packed":
			if err := checkPackedAligned(f, spec.elem); err != nil {
				return &FieldError{Field: f.Number, Err: err}
			}
		}
	}
	return nil
}

// checkPackedAligned verifies that a packed payload is well-formed:
// fixed-width payloads must be element-aligned, varint payloads must be
// fully consumed by whole varints.
func checkPackedAligned(f Field, elem string) error {
	switch elem {
	case "fixed32":
		if len(f.Bytes)%4 != 0 {
			return ErrTruncated
		}
	case "fixed64":
		if len(f.Bytes)%8 != 0 {
			return ErrTruncated
		}
	default:
		if _, err := DecodePackedVarint(f.Bytes); err != nil {
			return err
		}
	}
	return nil
}

// Unmarshal validates data against the schema of out and decodes every
// known field into it. Later occurrences of a scalar field win; repeated
// fields append.
func Unmarshal(data []byte, out any) error {
	rv := reflect.ValueOf(out)
	if !rv.IsValid() || rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return ErrBadTarget
	}
	return unmarshalMessage(data, rv.Elem(), 0)
}

func unmarshalMessage(data []byte, sv reflect.Value, depth int) error {
	if depth > MaxDepth {
		return ErrDepthLimit
	}
	fields, err := Parse(data)
	if err != nil {
		return err
	}
	specs, err := schemaOf(sv.Type())
	if err != nil {
		return err
	}
	byNum := make(map[int][]Field, len(fields))
	for _, f := range fields {
		byNum[f.Number] = append(byNum[f.Number], f)
	}
	for _, spec := range specs {
		vals := byNum[spec.num]
		if len(vals) == 0 {
			continue
		}
		fv := sv.Field(spec.index)
		if err := setValue(fv, spec, vals, depth); err != nil {
			return &FieldError{Field: spec.num, Err: err}
		}
	}
	return nil
}

func setValue(fv reflect.Value, spec fieldSpec, vals []Field, depth int) error {
	want := wantWire(spec.kind)
	for _, f := range vals {
		if f.Type != want {
			return fmt.Errorf("%w: got wire type %d, want %d", ErrWireTypeMismatch, f.Type, want)
		}
	}
	switch spec.kind {
	case "message":
		if fv.Kind() != reflect.Pointer || fv.Type().Elem().Kind() != reflect.Struct {
			return ErrBadFieldType
		}
		sub := reflect.New(fv.Type().Elem())
		if err := unmarshalMessage(vals[len(vals)-1].Bytes, sub.Elem(), depth+1); err != nil {
			return err
		}
		fv.Set(sub)
		return nil
	case "packed":
		if fv.Kind() != reflect.Slice {
			return ErrBadFieldType
		}
		for _, f := range vals {
			if err := appendPacked(fv, spec.elem, f.Bytes); err != nil {
				return err
			}
		}
		return nil
	default:
		if fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() != reflect.Uint8 {
			// repeated, non-packed
			for _, f := range vals {
				elem := reflect.New(fv.Type().Elem()).Elem()
				if err := setScalar(elem, spec.kind, f); err != nil {
					return err
				}
				fv.Set(reflect.Append(fv, elem))
			}
			return nil
		}
		return setScalar(fv, spec.kind, vals[len(vals)-1])
	}
}

func setScalar(fv reflect.Value, kind string, f Field) error {
	switch kind {
	case "varint":
		switch fv.Kind() {
		case reflect.Bool:
			fv.SetBool(f.Value != 0)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			fv.SetUint(f.Value)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			fv.SetInt(int64(f.Value))
		default:
			return ErrBadFieldType
		}
	case "sint":
		if fv.Kind() < reflect.Int || fv.Kind() > reflect.Int64 {
			return ErrBadFieldType
		}
		fv.SetInt(ZigZagDecode(f.Value))
	case "fixed32":
		v := uint32(f.Value)
		switch fv.Kind() {
		case reflect.Uint32, reflect.Uint, reflect.Uint64:
			fv.SetUint(uint64(v))
		case reflect.Int32, reflect.Int, reflect.Int64:
			fv.SetInt(int64(int32(v)))
		case reflect.Float32:
			fv.SetFloat(float64(math.Float32frombits(v)))
		default:
			return ErrBadFieldType
		}
	case "fixed64":
		switch fv.Kind() {
		case reflect.Uint, reflect.Uint64:
			fv.SetUint(f.Value)
		case reflect.Int, reflect.Int64:
			fv.SetInt(int64(f.Value))
		case reflect.Float64:
			fv.SetFloat(math.Float64frombits(f.Value))
		default:
			return ErrBadFieldType
		}
	case "string":
		if fv.Kind() != reflect.String {
			return ErrBadFieldType
		}
		fv.SetString(string(f.Bytes))
	case "bytes":
		if fv.Kind() != reflect.Slice || fv.Type().Elem().Kind() != reflect.Uint8 {
			return ErrBadFieldType
		}
		cp := make([]byte, len(f.Bytes))
		copy(cp, f.Bytes)
		fv.SetBytes(cp)
	default:
		return ErrBadFieldType
	}
	return nil
}

func appendPacked(fv reflect.Value, elemKind string, payload []byte) error {
	et := fv.Type().Elem()
	set := func(v uint64) error {
		elem := reflect.New(et).Elem()
		if err := setScalar(elem, elemKind, Field{Type: wantWire(elemKind), Value: v}); err != nil {
			return err
		}
		fv.Set(reflect.Append(fv, elem))
		return nil
	}
	switch elemKind {
	case "varint", "sint":
		vs, err := DecodePackedVarint(payload)
		if err != nil {
			return err
		}
		for _, v := range vs {
			if err := set(v); err != nil {
				return err
			}
		}
	case "fixed32":
		vs, err := DecodePackedFixed32(payload)
		if err != nil {
			return err
		}
		for _, v := range vs {
			if err := set(uint64(v)); err != nil {
				return err
			}
		}
	case "fixed64":
		vs, err := DecodePackedFixed64(payload)
		if err != nil {
			return err
		}
		for _, v := range vs {
			if err := set(v); err != nil {
				return err
			}
		}
	}
	return nil
}
