package wire

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// Unmarshal parses a protobuf message body and reflects it into out, which
// must be a non-nil pointer to a struct. Fields are bound via struct tags:
//
//	`pb:"<number>,<kind>[,packed]"`
//
// kind is one of: varint, zigzag, fixed32, fixed64, bytes.
// Repeated fields use Go slices; numeric repeated fields may be marked
// packed to accept length-delimited packed encoding (non-packed occurrences
// are also accepted, per the protobuf spec). Unknown fields are skipped.
func Unmarshal(buf []byte, out interface{}) error {
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return &Error{Op: "unmarshal", Err: fmt.Errorf("%v: out must be a non-nil pointer", ErrUnsupportedSchema)}
	}
	if rv.Elem().Kind() != reflect.Struct {
		return &Error{Op: "unmarshal", Err: fmt.Errorf("%v: out must point to a struct", ErrUnsupportedSchema)}
	}
	fields, err := ParseMessage(buf)
	if err != nil {
		return err
	}
	return decodeMessage(fields, rv.Elem())
}

type fieldSpec struct {
	number int
	kind   string // varint | zigzag | fixed32 | fixed64 | bytes
	packed bool
}

func parseTag(tag string) (fieldSpec, error) {
	var spec fieldSpec
	parts := strings.Split(tag, ",")
	if len(parts) < 2 {
		return spec, fmt.Errorf("%v: malformed tag %q", ErrUnsupportedSchema, tag)
	}
	num, err := strconv.Atoi(parts[0])
	if err != nil || num < 1 || num > MaxFieldNumber {
		return spec, fmt.Errorf("%v: bad field number in tag %q", ErrBadFieldNumber, tag)
	}
	spec.number = num
	spec.kind = parts[1]
	switch spec.kind {
	case "varint", "zigzag", "fixed32", "fixed64", "bytes":
	default:
		return spec, fmt.Errorf("%v: unknown kind %q", ErrUnsupportedSchema, spec.kind)
	}
	for _, opt := range parts[2:] {
		if opt == "packed" {
			spec.packed = true
		} else {
			return spec, fmt.Errorf("%v: unknown option %q", ErrUnsupportedSchema, opt)
		}
	}
	return spec, nil
}

func decodeMessage(fields []Field, sv reflect.Value) error {
	byNum := make(map[int][]Field)
	for _, f := range fields {
		byNum[f.Number] = append(byNum[f.Number], f)
	}
	st := sv.Type()
	for i := 0; i < st.NumField(); i++ {
		sf := st.Field(i)
		tag := sf.Tag.Get("pb")
		if tag == "" {
			continue
		}
		spec, err := parseTag(tag)
		if err != nil {
			return &Error{Op: "decode", Field: i + 1, Err: err}
		}
		occ := byNum[spec.number]
		if len(occ) == 0 {
			continue
		}
		if err := assignValue(sv.Field(i), spec, occ); err != nil {
			return &Error{Op: "decode " + sf.Name, Field: spec.number, Err: err}
		}
	}
	return nil
}

func assignValue(fv reflect.Value, spec fieldSpec, occ []Field) error {
	if fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() != reflect.Uint8 {
		for _, f := range occ {
			if spec.packed && f.Type == WireBytes {
				if err := decodePacked(fv, spec, f.Bytes); err != nil {
					return err
				}
				continue
			}
			elem := reflect.New(fv.Type().Elem()).Elem()
			if err := assignScalar(elem, spec, f); err != nil {
				return err
			}
			fv.Set(reflect.Append(fv, elem))
		}
		return nil
	}
	// Singular: last occurrence wins, per protobuf semantics.
	return assignScalar(fv, spec, occ[len(occ)-1])
}

func assignScalar(fv reflect.Value, spec fieldSpec, f Field) error {
	switch spec.kind {
	case "varint":
		if f.Type != WireVarint {
			return ErrWireTypeMismatch
		}
		switch fv.Kind() {
		case reflect.Bool:
			fv.SetBool(f.Varint != 0)
		case reflect.Uint, reflect.Uint32, reflect.Uint64:
			fv.SetUint(f.Varint)
		case reflect.Int, reflect.Int32, reflect.Int64:
			fv.SetInt(int64(f.Varint))
		default:
			return fmt.Errorf("%v: varint into %s", ErrUnsupportedSchema, fv.Type())
		}
	case "zigzag":
		if f.Type != WireVarint {
			return ErrWireTypeMismatch
		}
		switch fv.Kind() {
		case reflect.Int32:
			fv.SetInt(int64(DecodeZigZag32(f.Varint)))
		case reflect.Int, reflect.Int64:
			fv.SetInt(DecodeZigZag64(f.Varint))
		default:
			return fmt.Errorf("%v: zigzag into %s", ErrUnsupportedSchema, fv.Type())
		}
	case "fixed32":
		if f.Type != WireFixed32 {
			return ErrWireTypeMismatch
		}
		switch fv.Kind() {
		case reflect.Uint32:
			fv.SetUint(uint64(f.Fixed32))
		case reflect.Int32:
			fv.SetInt(int64(int32(f.Fixed32)))
		case reflect.Float32:
			fv.SetFloat(float64(math.Float32frombits(f.Fixed32)))
		default:
			return fmt.Errorf("%v: fixed32 into %s", ErrUnsupportedSchema, fv.Type())
		}
	case "fixed64":
		if f.Type != WireFixed64 {
			return ErrWireTypeMismatch
		}
		switch fv.Kind() {
		case reflect.Uint64:
			fv.SetUint(f.Fixed64)
		case reflect.Int64:
			fv.SetInt(int64(f.Fixed64))
		case reflect.Float64:
			fv.SetFloat(math.Float64frombits(f.Fixed64))
		default:
			return fmt.Errorf("%v: fixed64 into %s", ErrUnsupportedSchema, fv.Type())
		}
	case "bytes":
		if f.Type != WireBytes {
			return ErrWireTypeMismatch
		}
		switch fv.Kind() {
		case reflect.String:
			fv.SetString(string(f.Bytes))
		case reflect.Slice: // []byte
			fv.SetBytes(append([]byte(nil), f.Bytes...))
		case reflect.Struct: // nested message, by value
			sub, err := ParseMessage(f.Bytes)
			if err != nil {
				return err
			}
			return decodeMessage(sub, fv)
		case reflect.Ptr: // nested message, by pointer
			if fv.Type().Elem().Kind() != reflect.Struct {
				return fmt.Errorf("%v: bytes into %s", ErrUnsupportedSchema, fv.Type())
			}
			p := reflect.New(fv.Type().Elem())
			sub, err := ParseMessage(f.Bytes)
			if err != nil {
				return err
			}
			if err := decodeMessage(sub, p.Elem()); err != nil {
				return err
			}
			fv.Set(p)
		default:
			return fmt.Errorf("%v: bytes into %s", ErrUnsupportedSchema, fv.Type())
		}
	default:
		return fmt.Errorf("%v: kind %q", ErrUnsupportedSchema, spec.kind)
	}
	return nil
}

// decodePacked expands a length-delimited packed repeated payload.
func decodePacked(fv reflect.Value, spec fieldSpec, buf []byte) error {
	elemType := fv.Type().Elem()
	for len(buf) > 0 {
		elem := reflect.New(elemType).Elem()
		var f Field
		switch spec.kind {
		case "varint", "zigzag":
			v, n, err := ConsumeVarint(buf)
			if err != nil {
				return &Error{Op: "read packed varint", Err: err}
			}
			f = Field{Type: WireVarint, Varint: v}
			buf = buf[n:]
		case "fixed32":
			v, n, err := ConsumeFixed32(buf)
			if err != nil {
				return &Error{Op: "read packed fixed32", Err: err}
			}
			f = Field{Type: WireFixed32, Fixed32: v}
			buf = buf[n:]
		case "fixed64":
			v, n, err := ConsumeFixed64(buf)
			if err != nil {
				return &Error{Op: "read packed fixed64", Err: err}
			}
			f = Field{Type: WireFixed64, Fixed64: v}
			buf = buf[n:]
		default:
			return fmt.Errorf("%v: packed %s", ErrUnsupportedSchema, spec.kind)
		}
		if err := assignScalar(elem, spec, f); err != nil {
			return err
		}
		fv.Set(reflect.Append(fv, elem))
	}
	return nil
}
