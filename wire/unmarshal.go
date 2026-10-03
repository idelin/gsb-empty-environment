package wire

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// Unmarshal parses the protobuf wire stream in data into v, which must be a
// pointer to a struct. Fields are bound via struct tags:
//
//	Field uint64 `protobuf:"varint,1"`
//	Name  string `protobuf:"string,2"`
//	Sub   Inner  `protobuf:"message,3"`
//	Nums  []int32 `protobuf:"zigzag32,4"`   // packed or unpacked
//
// Supported encodings: varint, zigzag32, zigzag64, fixed32, fixed64, bytes,
// string, message. Unknown fields are skipped. Numeric slices accept both
// packed (length-delimited) and unpacked representations.
func Unmarshal(data []byte, v interface{}) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("wire: Unmarshal requires a non-nil pointer to struct, got %T", v)
	}
	return unmarshalMessage(NewReader(data), rv.Elem())
}

type fieldInfo struct {
	index int
	enc   string
	num   int
}

// FieldError annotates a decode failure with the field that caused it. The
// underlying error stays reachable via Cause so helpers like IsTruncated can
// see through the wrap.
type FieldError struct {
	Num  int
	Name string
	Err  error
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("wire: field %d (%s): %v", e.Num, e.Name, e.Err)
}

// Cause returns the underlying error.
func (e *FieldError) Cause() error { return e.Err }

func parseProtoTag(sf reflect.StructField) (fieldInfo, bool) {
	tag, ok := sf.Tag.Lookup("protobuf")
	if !ok || tag == "" || tag == "-" {
		return fieldInfo{}, false
	}
	parts := strings.Split(tag, ",")
	if len(parts) != 2 {
		return fieldInfo{}, false
	}
	num, err := strconv.Atoi(parts[1])
	if err != nil || num <= 0 {
		return fieldInfo{}, false
	}
	return fieldInfo{index: sf.Index[0], enc: parts[0], num: num}, true
}

func fieldsOf(t reflect.Type) map[int]fieldInfo {
	m := make(map[int]fieldInfo)
	for i := 0; i < t.NumField(); i++ {
		if fi, ok := parseProtoTag(t.Field(i)); ok {
			m[fi.num] = fi
		}
	}
	return m
}

func unmarshalMessage(r *Reader, sv reflect.Value) error {
	fields := fieldsOf(sv.Type())
	for !r.Done() {
		num, wt, err := r.ReadTag()
		if err != nil {
			return err
		}
		fi, ok := fields[num]
		if !ok {
			if err := r.Skip(wt); err != nil {
				return err
			}
			continue
		}
		if err := decodeField(r, fi, sv.Field(fi.index), wt); err != nil {
			return &FieldError{Num: num, Name: sv.Type().Field(fi.index).Name, Err: err}
		}
	}
	return nil
}

// wantWireType maps an encoding to its expected wire type.
func wantWireType(enc string) (WireType, error) {
	switch enc {
	case "varint", "zigzag32", "zigzag64":
		return WireVarint, nil
	case "fixed32":
		return WireFixed32, nil
	case "fixed64":
		return WireFixed64, nil
	case "bytes", "string", "message":
		return WireBytes, nil
	}
	return 0, fmt.Errorf("unknown encoding %q", enc)
}

func decodeField(r *Reader, fi fieldInfo, fv reflect.Value, wt WireType) error {
	want, err := wantWireType(fi.enc)
	if err != nil {
		return err
	}

	// Repeated fields.
	if fv.Kind() == reflect.Slice && fi.enc != "bytes" {
		if wt == WireBytes && fi.enc != "message" && fi.enc != "string" {
			// Packed repeated: decode elements from the sub-buffer.
			sub, err := r.ReadBytes()
			if err != nil {
				return err
			}
			sr := newReaderDepth(sub, r.depth)
			for !sr.Done() {
				if err := appendOne(sr, fi, fv); err != nil {
					return err
				}
			}
			return nil
		}
		if wt != want {
			return fmt.Errorf("got wire type %d, want %d", wt, want)
		}
		return appendOne(r, fi, fv)
	}

	if wt != want {
		return fmt.Errorf("got wire type %d, want %d", wt, want)
	}
	return setOne(r, fi.enc, fv)
}

func appendOne(r *Reader, fi fieldInfo, fv reflect.Value) error {
	elem := fv.Type().Elem()
	nv := reflect.New(elem).Elem()
	if err := setOne(r, fi.enc, nv); err != nil {
		return err
	}
	fv.Set(reflect.Append(fv, nv))
	return nil
}

func setOne(r *Reader, enc string, fv reflect.Value) error {
	switch enc {
	case "varint":
		v, err := r.ReadVarint()
		if err != nil {
			return err
		}
		switch fv.Kind() {
		case reflect.Bool:
			fv.SetBool(v != 0)
		case reflect.Int, reflect.Int32, reflect.Int64:
			fv.SetInt(int64(v))
		case reflect.Uint, reflect.Uint32, reflect.Uint64:
			fv.SetUint(v)
		default:
			return fmt.Errorf("varint cannot target %s", fv.Kind())
		}
		return nil
	case "zigzag32":
		v, err := r.ReadZigZag32()
		if err != nil {
			return err
		}
		fv.SetInt(int64(v))
		return nil
	case "zigzag64":
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
		switch fv.Kind() {
		case reflect.Float32:
			fv.SetFloat(float64(math.Float32frombits(v)))
		case reflect.Int32, reflect.Int:
			fv.SetInt(int64(int32(v)))
		case reflect.Uint32, reflect.Uint:
			fv.SetUint(uint64(v))
		default:
			return fmt.Errorf("fixed32 cannot target %s", fv.Kind())
		}
		return nil
	case "fixed64":
		v, err := r.ReadFixed64()
		if err != nil {
			return err
		}
		switch fv.Kind() {
		case reflect.Float64:
			fv.SetFloat(math.Float64frombits(v))
		case reflect.Int64, reflect.Int:
			fv.SetInt(int64(v))
		case reflect.Uint64, reflect.Uint:
			fv.SetUint(v)
		default:
			return fmt.Errorf("fixed64 cannot target %s", fv.Kind())
		}
		return nil
	case "string":
		v, err := r.ReadString()
		if err != nil {
			return err
		}
		fv.SetString(v)
		return nil
	case "bytes":
		v, err := r.ReadBytes()
		if err != nil {
			return err
		}
		cp := make([]byte, len(v))
		copy(cp, v)
		fv.SetBytes(cp)
		return nil
	case "message":
		sub, err := r.ReadMessage()
		if err != nil {
			return err
		}
		target := fv
		if fv.Kind() == reflect.Ptr {
			nv := reflect.New(fv.Type().Elem())
			fv.Set(nv)
			target = nv.Elem()
		}
		if target.Kind() != reflect.Struct {
			return fmt.Errorf("message cannot target %s", fv.Kind())
		}
		return unmarshalMessage(sub, target)
	}
	return fmt.Errorf("unknown encoding %q", enc)
}
