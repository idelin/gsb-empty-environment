package wire

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// Struct tag format:
//
//	`protobuf:"<fieldNum>[,<encoding>][,packed][,req]"`
//
// encoding is one of: varint, zigzag, fixed32, fixed64, bytes.
// If omitted it is inferred from the Go field kind. "packed" marks a
// repeated numeric field as packed-encoded; "req" marks it required for
// the validation engine.
//
// ValidationError reports required fields that were absent from the stream.
type ValidationError struct {
	Missing []string
}

func (e *ValidationError) Error() string {
	return "wire: missing required fields: " + strings.Join(e.Missing, ", ")
}

type fieldInfo struct {
	num    int
	enc    string
	packed bool
	req    bool
	index  int
	typ    reflect.Type
}

type structInfo struct {
	byNum  map[int]fieldInfo
	fields []fieldInfo
}

// structCache avoids re-parsing tags; sync.Map is read-mostly and lock-free
// on the hot path.
var structCache sync.Map // map[reflect.Type]*structInfo

func infoFor(t reflect.Type) (*structInfo, error) {
	if v, ok := structCache.Load(t); ok {
		return v.(*structInfo), nil
	}
	si := &structInfo{byNum: make(map[int]fieldInfo)}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("protobuf")
		if tag == "" || tag == "-" {
			continue
		}
		fi, err := parseField(f, tag, i)
		if err != nil {
			return nil, err
		}
		if _, dup := si.byNum[fi.num]; dup {
			return nil, fmt.Errorf("wire: duplicate field number %d in %s", fi.num, t)
		}
		si.byNum[fi.num] = fi
		si.fields = append(si.fields, fi)
	}
	structCache.Store(t, si)
	return si, nil
}

func parseField(f reflect.StructField, tag string, idx int) (fieldInfo, error) {
	parts := strings.Split(tag, ",")
	num, err := strconv.Atoi(parts[0])
	if err != nil || num <= 0 {
		return fieldInfo{}, fmt.Errorf("wire: bad field number in tag %q on %s", tag, f.Name)
	}
	fi := fieldInfo{num: num, index: idx, typ: f.Type, enc: inferEnc(f.Type)}
	for _, opt := range parts[1:] {
		switch opt {
		case "varint", "zigzag", "fixed32", "fixed64", "bytes":
			fi.enc = opt
		case "packed":
			fi.packed = true
		case "req":
			fi.req = true
		default:
			return fieldInfo{}, fmt.Errorf("wire: unknown tag option %q on %s", opt, f.Name)
		}
	}
	return fi, nil
}

func inferEnc(t reflect.Type) string {
	if t.Kind() == reflect.Slice && t.Elem().Kind() != reflect.Uint8 {
		return inferEnc(t.Elem())
	}
	switch t.Kind() {
	case reflect.Bool, reflect.Int32, reflect.Int64, reflect.Uint32, reflect.Uint64:
		return "varint"
	case reflect.Uint8: // []byte
		return "bytes"
	case reflect.String, reflect.Slice, reflect.Struct, reflect.Ptr:
		return "bytes"
	case reflect.Float32:
		return "fixed32"
	case reflect.Float64:
		return "fixed64"
	}
	return "varint"
}

func encWireType(enc string) int {
	switch enc {
	case "varint", "zigzag":
		return WTVarint
	case "fixed32":
		return WTFixed32
	case "fixed64":
		return WTFixed64
	}
	return WTBytes
}

// Unmarshal parses the protobuf wire-format stream in data into v, which
// must be a pointer to a struct. Unknown fields are skipped. After decoding,
// the validation engine checks that every field tagged "req" was present;
// violations are returned as *ValidationError. Truncated or malformed input
// yields ErrTruncated / ErrOverflow / ErrBadWireType.
func Unmarshal(data []byte, v interface{}) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("wire: Unmarshal requires *struct, got %T", v)
	}
	return decodeStruct(NewDecoder(data), rv.Elem(), "")
}

func decodeStruct(d *Decoder, rv reflect.Value, path string) error {
	si, err := infoFor(rv.Type())
	if err != nil {
		return err
	}
	seen := make(map[int]bool, len(si.fields))
	for d.More() {
		num, wt, err := d.Tag()
		if err != nil {
			return err
		}
		fi, ok := si.byNum[num]
		if !ok {
			if err := d.Skip(wt); err != nil {
				return err
			}
			continue
		}
		if err := decodeField(d, rv.Field(fi.index), fi, wt, path); err != nil {
			return err
		}
		seen[num] = true
	}
	var missing []string
	for _, fi := range si.fields {
		if fi.req && !seen[fi.num] {
			missing = append(missing, path+rv.Type().Field(fi.index).Name)
		}
	}
	if len(missing) > 0 {
		return &ValidationError{Missing: missing}
	}
	return nil
}

func decodeField(d *Decoder, fv reflect.Value, fi fieldInfo, wt int, path string) error {
	ft := fi.typ
	// Repeated fields (except []byte which is a scalar bytes field).
	if ft.Kind() == reflect.Slice && ft.Elem().Kind() != reflect.Uint8 {
		return decodeRepeated(d, fv, fi, wt, path)
	}
	if ft.Kind() == reflect.Ptr && ft.Elem().Kind() == reflect.Struct {
		if wt != WTBytes {
			return ErrBadWireType
		}
		sub, err := d.Bytes()
		if err != nil {
			return err
		}
		nv := reflect.New(ft.Elem())
		if err := decodeStruct(NewDecoder(sub), nv.Elem(), path+nv.Elem().Type().Name()+"."); err != nil {
			return err
		}
		fv.Set(nv)
		return nil
	}
	if ft.Kind() == reflect.Struct {
		if wt != WTBytes {
			return ErrBadWireType
		}
		sub, err := d.Bytes()
		if err != nil {
			return err
		}
		return decodeStruct(NewDecoder(sub), fv, path+ft.Name()+".")
	}
	if wt != encWireType(fi.enc) {
		return ErrBadWireType
	}
	return decodeScalar(d, fv, fi.enc)
}

func decodeRepeated(d *Decoder, fv reflect.Value, fi fieldInfo, wt int, path string) error {
	et := fi.typ.Elem()
	if et.Kind() == reflect.Ptr && et.Elem().Kind() == reflect.Struct ||
		et.Kind() == reflect.Struct || et.Kind() == reflect.String {
		// Repeated messages / strings: one length-delimited element per tag.
		if wt != WTBytes {
			return ErrBadWireType
		}
		sub, err := d.Bytes()
		if err != nil {
			return err
		}
		switch {
		case et.Kind() == reflect.String:
			fv.Set(reflect.Append(fv, reflect.ValueOf(string(sub))))
		case et.Kind() == reflect.Ptr:
			nv := reflect.New(et.Elem())
			if err := decodeStruct(NewDecoder(sub), nv.Elem(), path+et.Elem().Name()+"."); err != nil {
				return err
			}
			fv.Set(reflect.Append(fv, nv))
		default:
			nv := reflect.New(et).Elem()
			if err := decodeStruct(NewDecoder(sub), nv, path+et.Name()+"."); err != nil {
				return err
			}
			fv.Set(reflect.Append(fv, nv))
		}
		return nil
	}
	// Numeric repeated: packed (WTBytes) or unpacked single element.
	if wt == WTBytes {
		packed, err := d.Bytes()
		if err != nil {
			return err
		}
		pd := NewDecoder(packed)
		for pd.More() {
			nv := reflect.New(et).Elem()
			if err := decodeScalar(pd, nv, fi.enc); err != nil {
				return err
			}
			fv.Set(reflect.Append(fv, nv))
		}
		return nil
	}
	if wt != encWireType(fi.enc) {
		return ErrBadWireType
	}
	nv := reflect.New(et).Elem()
	if err := decodeScalar(d, nv, fi.enc); err != nil {
		return err
	}
	fv.Set(reflect.Append(fv, nv))
	return nil
}

func decodeScalar(d *Decoder, fv reflect.Value, enc string) error {
	switch enc {
	case "varint":
		v, err := d.Varint()
		if err != nil {
			return err
		}
		switch fv.Kind() {
		case reflect.Bool:
			fv.SetBool(v != 0)
		case reflect.Int32, reflect.Int64:
			fv.SetInt(int64(v))
		case reflect.Uint32, reflect.Uint64:
			fv.SetUint(v)
		default:
			return fmt.Errorf("wire: varint into unsupported kind %s", fv.Kind())
		}
	case "zigzag":
		v, err := d.ZigZag()
		if err != nil {
			return err
		}
		fv.SetInt(v)
	case "fixed32":
		v, err := d.Fixed32()
		if err != nil {
			return err
		}
		switch fv.Kind() {
		case reflect.Float32:
			fv.SetFloat(float64(math.Float32frombits(v)))
		case reflect.Int32:
			fv.SetInt(int64(int32(v)))
		default:
			fv.SetUint(uint64(v))
		}
	case "fixed64":
		v, err := d.Fixed64()
		if err != nil {
			return err
		}
		switch fv.Kind() {
		case reflect.Float64:
			fv.SetFloat(math.Float64frombits(v))
		case reflect.Int64:
			fv.SetInt(int64(v))
		default:
			fv.SetUint(v)
		}
	case "bytes":
		b, err := d.Bytes()
		if err != nil {
			return err
		}
		switch fv.Kind() {
		case reflect.String:
			fv.SetString(string(b))
		case reflect.Slice:
			fv.SetBytes(append([]byte(nil), b...))
		default:
			return fmt.Errorf("wire: bytes into unsupported kind %s", fv.Kind())
		}
	default:
		return fmt.Errorf("wire: unknown encoding %q", enc)
	}
	return nil
}
