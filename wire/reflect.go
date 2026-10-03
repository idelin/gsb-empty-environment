// Reflection-mode decoder and validation engine.
//
// Struct fields are mapped to protobuf fields with tags:
//
//	protobuf:"<codec>,<fieldNum>[,packed]"
//
// codec is one of: varint, zigzag, fixed32, fixed64, bytes.
// Supported Go field shapes:
//
//	varint : bool, int32, int64, uint32, uint64 (and []T repeated)
//	zigzag : int32, int64 (sint32/sint64)
//	fixed32: uint32, float32
//	fixed64: uint64, float64
//	bytes  : string, []byte, nested struct / *struct (embedded message)
//
// Repeated numeric fields decode both packed (wire type 2) and unpacked
// encodings. Unknown fields are skipped safely.
package wire

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

type fieldInfo struct {
	index  int
	codec  string
	packed bool
	typ    reflect.Type
}

type structInfo struct {
	byNum map[int]fieldInfo
}

var structCache sync.Map // reflect.Type -> *structInfo

func parseStructInfo(t reflect.Type) (*structInfo, error) {
	if cached, ok := structCache.Load(t); ok {
		return cached.(*structInfo), nil
	}
	info := &structInfo{byNum: make(map[int]fieldInfo)}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag, ok := f.Tag.Lookup("protobuf")
		if !ok {
			continue
		}
		parts := strings.Split(tag, ",")
		if len(parts) < 2 {
			return nil, fmt.Errorf("wire: %s.%s: malformed protobuf tag %q", t.Name(), f.Name, tag)
		}
		codec := parts[0]
		switch codec {
		case "varint", "zigzag", "fixed32", "fixed64", "bytes":
		default:
			return nil, fmt.Errorf("wire: %s.%s: unknown codec %q", t.Name(), f.Name, codec)
		}
		num, err := strconv.Atoi(parts[1])
		if err != nil || num <= 0 {
			return nil, fmt.Errorf("wire: %s.%s: bad field number in tag %q", t.Name(), f.Name, tag)
		}
		fi := fieldInfo{index: i, codec: codec, typ: f.Type}
		for _, opt := range parts[2:] {
			if opt == "packed" {
				fi.packed = true
			}
		}
		if _, dup := info.byNum[num]; dup {
			return nil, fmt.Errorf("wire: %s: duplicate protobuf field number %d", t.Name(), num)
		}
		info.byNum[num] = fi
	}
	structCache.Store(t, info)
	return info, nil
}

// Unmarshal decodes a protobuf wire stream into the struct pointed to by v.
// It returns an error on truncation, overflow, or wire-type mismatch; unknown
// fields are skipped.
func Unmarshal(data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("wire: Unmarshal target must be a non-nil pointer, got %T", v)
	}
	return decodeMessage(NewReader(data), rv.Elem())
}

func decodeMessage(r *Reader, sv reflect.Value) error {
	if sv.Kind() != reflect.Struct {
		return fmt.Errorf("wire: cannot decode into %s", sv.Type())
	}
	info, err := parseStructInfo(sv.Type())
	if err != nil {
		return err
	}
	for !r.Done() {
		num, wt, err := r.ReadTag()
		if err != nil {
			return err
		}
		fi, known := info.byNum[num]
		if !known {
			if err := r.Skip(wt); err != nil {
				return err
			}
			continue
		}
		if err := decodeField(r, sv.Field(fi.index), fi, wt); err != nil {
			return fmt.Errorf("wire: field %d (%s): %w", num, sv.Type().Field(fi.index).Name, err)
		}
	}
	return nil
}

// wantWireType maps a codec to the wire type a scalar (non-packed) value uses.
func wantWireType(codec string) Type {
	switch codec {
	case "varint", "zigzag":
		return Varint
	case "fixed32":
		return Fixed32
	case "fixed64":
		return Fixed64
	default:
		return LengthDelim
	}
}

func decodeField(r *Reader, fv reflect.Value, fi fieldInfo, wt Type) error {
	// Repeated fields.
	if fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() != reflect.Uint8 {
		elem := fi.typ.Elem()
		if wt == LengthDelim && isPackable(elem) {
			packed, err := r.ReadBytes()
			if err != nil {
				return err
			}
			pr := NewReader(packed)
			for !pr.Done() {
				ev, err := readScalar(pr, fi.codec, elem)
				if err != nil {
					return err
				}
				fv.Set(reflect.Append(fv, ev))
			}
			return nil
		}
		if wt != wantWireType(fi.codec) {
			return fmt.Errorf("wire type %s does not match codec %q", wt, fi.codec)
		}
		ev, err := readScalar(r, fi.codec, elem)
		if err != nil {
			return err
		}
		fv.Set(reflect.Append(fv, ev))
		return nil
	}

	if wt != wantWireType(fi.codec) {
		return fmt.Errorf("wire type %s does not match codec %q", wt, fi.codec)
	}

	// Embedded message: struct or pointer-to-struct with bytes codec.
	if fi.codec == "bytes" {
		t := fv.Type()
		if t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct {
			if fv.IsNil() {
				fv.Set(reflect.New(t.Elem()))
			}
			raw, err := r.ReadBytes()
			if err != nil {
				return err
			}
			return decodeMessage(NewReader(raw), fv.Elem())
		}
		if t.Kind() == reflect.Struct {
			raw, err := r.ReadBytes()
			if err != nil {
				return err
			}
			return decodeMessage(NewReader(raw), fv)
		}
	}

	v, err := readScalar(r, fi.codec, fv.Type())
	if err != nil {
		return err
	}
	fv.Set(v)
	return nil
}

func isPackable(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Bool, reflect.Int32, reflect.Int64, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// readScalar decodes one value of the given codec into a reflect.Value of type t.
func readScalar(r *Reader, codec string, t reflect.Type) (reflect.Value, error) {
	switch codec {
	case "varint":
		v, err := r.ReadVarint()
		if err != nil {
			return reflect.Value{}, err
		}
		switch t.Kind() {
		case reflect.Bool:
			return reflect.ValueOf(v != 0), nil
		case reflect.Int32:
			return reflect.ValueOf(int32(v)), nil
		case reflect.Int64:
			return reflect.ValueOf(int64(v)), nil
		case reflect.Uint32:
			return reflect.ValueOf(uint32(v)), nil
		case reflect.Uint64:
			return reflect.ValueOf(v), nil
		}
	case "zigzag":
		v, err := r.ReadZigZag()
		if err != nil {
			return reflect.Value{}, err
		}
		switch t.Kind() {
		case reflect.Int32:
			return reflect.ValueOf(int32(v)), nil
		case reflect.Int64:
			return reflect.ValueOf(v), nil
		}
	case "fixed32":
		v, err := r.ReadFixed32()
		if err != nil {
			return reflect.Value{}, err
		}
		switch t.Kind() {
		case reflect.Uint32:
			return reflect.ValueOf(v), nil
		case reflect.Float32:
			return reflect.ValueOf(math.Float32frombits(v)), nil
		}
	case "fixed64":
		v, err := r.ReadFixed64()
		if err != nil {
			return reflect.Value{}, err
		}
		switch t.Kind() {
		case reflect.Uint64:
			return reflect.ValueOf(v), nil
		case reflect.Float64:
			return reflect.ValueOf(math.Float64frombits(v)), nil
		}
	case "bytes":
		v, err := r.ReadBytes()
		if err != nil {
			return reflect.Value{}, err
		}
		switch t.Kind() {
		case reflect.String:
			return reflect.ValueOf(string(v)), nil
		case reflect.Slice:
			if t.Elem().Kind() == reflect.Uint8 {
				return reflect.ValueOf(v).Convert(t), nil
			}
		}
	}
	return reflect.Value{}, fmt.Errorf("codec %q cannot fill Go type %s", codec, t)
}
