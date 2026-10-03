package wire

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Reflection-based (un)marshaling between Go structs and protobuf wire
// format, driven by struct tags:
//
//	type Header struct {
//	    ID      uint64   `protobuf:"1,varint"`
//	    Delta   int64    `protobuf:"2,zigzag"`
//	    Flags   uint32   `protobuf:"3,fixed32"`
//	    Stamp   uint64   `protobuf:"4,fixed64"`
//	    Name    string   `protobuf:"5,bytes"`
//	    Payload []byte   `protobuf:"6,bytes"`
//	    Child   *Child   `protobuf:"7,message"`
//	    Samples []int64  `protobuf:"8,zigzag,packed"`
//	    Scores  []uint32 `protobuf:"9,fixed32,packed"`
//	}
//
// Field kinds: varint, zigzag, fixed32, fixed64, bytes, message.
// Repeated fields use a slice type; the "packed" option encodes them as a
// single length-delimited payload (the proto3 default for scalars).

// ErrNotStructPointer is returned when Unmarshal/Marshal receive a value
// that is not a pointer to struct / struct.
var ErrNotStructPointer = errors.New("wire: expected pointer to struct")

type fieldSpec struct {
	num    int
	kind   string
	packed bool
	index  int
}

func parseTag(tag string) (fieldSpec, bool, error) {
	var spec fieldSpec
	if tag == "" {
		return spec, false, nil
	}
	parts := strings.Split(tag, ",")
	if len(parts) < 2 {
		return spec, false, fmt.Errorf("wire: malformed protobuf tag %q", tag)
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n < 1 || n > maxFieldNumber {
		return spec, false, fmt.Errorf("wire: bad field number in tag %q", tag)
	}
	spec.num = n
	spec.kind = parts[1]
	switch spec.kind {
	case "varint", "zigzag", "fixed32", "fixed64", "bytes", "message":
	default:
		return spec, false, fmt.Errorf("wire: unknown kind %q", spec.kind)
	}
	for _, opt := range parts[2:] {
		if opt == "packed" {
			spec.packed = true
		}
	}
	return spec, true, nil
}

func specsFor(t reflect.Type) ([]fieldSpec, error) {
	var specs []fieldSpec
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		spec, ok, err := parseTag(f.Tag.Get("protobuf"))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		spec.index = i
		specs = append(specs, spec)
	}
	return specs, nil
}

// ---------------------------------------------------------------------------
// Unmarshal
// ---------------------------------------------------------------------------

// Unmarshal decodes a wire-format message into out, which must be a pointer
// to a struct. Unknown fields are skipped safely; truncated or malformed
// input returns an error and never panics.
func Unmarshal(data []byte, out interface{}) error {
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Struct {
		return ErrNotStructPointer
	}
	return unmarshalInto(data, rv.Elem())
}

func unmarshalInto(data []byte, dst reflect.Value) error {
	specs, err := specsFor(dst.Type())
	if err != nil {
		return err
	}
	byNum := make(map[int]fieldSpec, len(specs))
	for _, s := range specs {
		byNum[s.num] = s
	}

	r := NewReader(data)
	for !r.Done() {
		num, wt, err := r.ReadTag()
		if err != nil {
			return fmt.Errorf("wire: tag at offset %d: %v", r.Offset(), err)
		}
		spec, known := byNum[num]
		if !known {
			if err := r.Skip(wt); err != nil {
				return fmt.Errorf("wire: skipping unknown field %d: %v", num, err)
			}
			continue
		}
		if err := decodeField(r, dst.Field(spec.index), spec, wt); err != nil {
			return fmt.Errorf("wire: field %d (%s): %v", num, spec.kind, err)
		}
	}
	return nil
}

func decodeField(r *Reader, fv reflect.Value, spec fieldSpec, wt int) error {
	if fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() != reflect.Uint8 && spec.kind != "bytes" {
		return decodeRepeated(r, fv, spec, wt)
	}
	return decodeScalar(r, fv, spec, wt)
}

func decodeRepeated(r *Reader, fv reflect.Value, spec fieldSpec, wt int) error {
	elemType := fv.Type().Elem()
	if spec.packed {
		if wt != WireBytes {
			return fmt.Errorf("expected packed payload, got wire type %d", wt)
		}
		payload, err := r.ReadBytes()
		if err != nil {
			return err
		}
		pr := NewReader(payload)
		for !pr.Done() {
			elem := reflect.New(elemType).Elem()
			if err := decodeScalarValue(pr, elem, spec); err != nil {
				return fmt.Errorf("packed element at offset %d: %v", pr.Offset(), err)
			}
			fv.Set(reflect.Append(fv, elem))
		}
		return nil
	}
	// Non-packed: one element per occurrence of the tag.
	elem := reflect.New(elemType).Elem()
	if err := decodeScalarWithWireCheck(r, elem, spec, wt); err != nil {
		return err
	}
	fv.Set(reflect.Append(fv, elem))
	return nil
}

func decodeScalar(r *Reader, fv reflect.Value, spec fieldSpec, wt int) error {
	return decodeScalarWithWireCheck(r, fv, spec, wt)
}

func decodeScalarWithWireCheck(r *Reader, fv reflect.Value, spec fieldSpec, wt int) error {
	want := map[string]int{
		"varint": WireVarint, "zigzag": WireVarint,
		"fixed32": WireFixed32, "fixed64": WireFixed64,
		"bytes": WireBytes, "message": WireBytes,
	}[spec.kind]
	if wt != want {
		return fmt.Errorf("wire type mismatch: got %d, want %d", wt, want)
	}
	return decodeScalarValue(r, fv, spec)
}

// decodeScalarValue decodes one value of spec.kind from r into fv, assuming
// the wire type has already been validated (packed elements carry no tag).
func decodeScalarValue(r *Reader, fv reflect.Value, spec fieldSpec) error {
	switch spec.kind {
	case "varint":
		v, err := r.ReadVarint()
		if err != nil {
			return err
		}
		switch fv.Kind() {
		case reflect.Uint64, reflect.Uint32, reflect.Uint, reflect.Uint16, reflect.Uint8:
			fv.SetUint(v)
		case reflect.Int64, reflect.Int32, reflect.Int, reflect.Int16, reflect.Int8:
			fv.SetInt(int64(v))
		case reflect.Bool:
			fv.SetBool(v != 0)
		default:
			return fmt.Errorf("unsupported varint target %s", fv.Type())
		}
		return nil
	case "zigzag":
		v, err := r.ReadZigZag64()
		if err != nil {
			return err
		}
		fv.SetInt(v)
		return nil
	case "fixed32":
		v, err := r.ReadFixed32()
		if err != nil {
			return err
		}
		fv.SetUint(uint64(v))
		return nil
	case "fixed64":
		v, err := r.ReadFixed64()
		if err != nil {
			return err
		}
		fv.SetUint(v)
		return nil
	case "bytes":
		v, err := r.ReadBytes()
		if err != nil {
			return err
		}
		if fv.Kind() == reflect.String {
			fv.SetString(string(v))
			return nil
		}
		if fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() == reflect.Uint8 {
			cp := make([]byte, len(v))
			copy(cp, v)
			fv.SetBytes(cp)
			return nil
		}
		return fmt.Errorf("unsupported bytes target %s", fv.Type())
	case "message":
		v, err := r.ReadBytes()
		if err != nil {
			return err
		}
		if fv.Kind() == reflect.Ptr {
			if fv.IsNil() {
				fv.Set(reflect.New(fv.Type().Elem()))
			}
			fv = fv.Elem()
		}
		if fv.Kind() != reflect.Struct {
			return fmt.Errorf("unsupported message target %s", fv.Type())
		}
		return unmarshalInto(v, fv)
	}
	return fmt.Errorf("unknown kind %q", spec.kind)
}

// ---------------------------------------------------------------------------
// Marshal
// ---------------------------------------------------------------------------

// Marshal encodes a struct (or pointer to one) into protobuf wire format.
func Marshal(v interface{}) ([]byte, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil, ErrNotStructPointer
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, ErrNotStructPointer
	}
	buf := NewBuffer()
	if err := marshalValue(rv, buf); err != nil {
		return nil, err
	}
	out := make([]byte, len(buf.Bytes()))
	copy(out, buf.Bytes())
	return out, nil
}

func marshalValue(sv reflect.Value, buf *Buffer) error {
	specs, err := specsFor(sv.Type())
	if err != nil {
		return err
	}
	for _, spec := range specs {
		fv := sv.Field(spec.index)
		if fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() != reflect.Uint8 && spec.kind != "bytes" {
			if err := marshalRepeated(fv, spec, buf); err != nil {
				return err
			}
			continue
		}
		if spec.kind == "message" {
			if fv.Kind() == reflect.Ptr && fv.IsNil() {
				continue
			}
			sub := NewBuffer()
			target := fv
			if target.Kind() == reflect.Ptr {
				target = target.Elem()
			}
			if err := marshalValue(target, sub); err != nil {
				return err
			}
			buf.AppendTag(spec.num, WireBytes)
			buf.AppendBytes(sub.Bytes())
			continue
		}
		buf.AppendTag(spec.num, scalarWireType(spec.kind))
		if err := marshalScalar(fv, spec.kind, buf); err != nil {
			return err
		}
	}
	return nil
}

func marshalRepeated(fv reflect.Value, spec fieldSpec, buf *Buffer) error {
	if fv.Len() == 0 {
		return nil
	}
	if spec.packed {
		payload := NewBuffer()
		for i := 0; i < fv.Len(); i++ {
			if err := marshalScalar(fv.Index(i), spec.kind, payload); err != nil {
				return err
			}
		}
		buf.AppendTag(spec.num, WireBytes)
		buf.AppendBytes(payload.Bytes())
		return nil
	}
	for i := 0; i < fv.Len(); i++ {
		buf.AppendTag(spec.num, scalarWireType(spec.kind))
		if err := marshalScalar(fv.Index(i), spec.kind, buf); err != nil {
			return err
		}
	}
	return nil
}

func scalarWireType(kind string) int {
	switch kind {
	case "varint", "zigzag":
		return WireVarint
	case "fixed32":
		return WireFixed32
	case "fixed64":
		return WireFixed64
	default:
		return WireBytes
	}
}

func marshalScalar(fv reflect.Value, kind string, buf *Buffer) error {
	switch kind {
	case "varint":
		switch fv.Kind() {
		case reflect.Bool:
			if fv.Bool() {
				buf.AppendVarint(1)
			} else {
				buf.AppendVarint(0)
			}
		case reflect.Int64, reflect.Int32, reflect.Int, reflect.Int16, reflect.Int8:
			buf.AppendVarint(uint64(fv.Int()))
		default:
			buf.AppendVarint(fv.Uint())
		}
	case "zigzag":
		buf.AppendZigZag64(fv.Int())
	case "fixed32":
		buf.AppendFixed32(uint32(fv.Uint()))
	case "fixed64":
		buf.AppendFixed64(fv.Uint())
	case "bytes":
		if fv.Kind() == reflect.String {
			buf.AppendString(fv.String())
		} else {
			buf.AppendBytes(fv.Bytes())
		}
	default:
		return fmt.Errorf("wire: cannot marshal kind %q", kind)
	}
	return nil
}
