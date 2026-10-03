// Reflection-mode engine: maps Go structs onto protobuf wire format using
// struct tags, and validates decoded values. Zero code generation, zero
// external dependencies.
//
// Field tag syntax:
//
//	`protobuf:"<kind>,<number>[,packed]"`
//
// kinds: varint, zigzag, fixed32, fixed64, sfixed32, sfixed64, bytes
// Validation tag syntax (comma separated):
//
//	`validate:"nonzero,min=1,max=100"`
package wire

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// fieldDesc describes one tagged struct field.
type fieldDesc struct {
	index  int
	number int
	kind   string
	packed bool
	typ    reflect.Type
}

func parseTag(sf reflect.StructField) (*fieldDesc, bool) {
	tag, ok := sf.Tag.Lookup("protobuf")
	if !ok || tag == "" || tag == "-" {
		return nil, false
	}
	parts := strings.Split(tag, ",")
	if len(parts) < 2 {
		return nil, false
	}
	num, err := strconv.Atoi(parts[1])
	if err != nil || num <= 0 {
		return nil, false
	}
	d := &fieldDesc{index: -1, number: num, kind: parts[0]}
	for _, opt := range parts[2:] {
		if opt == "packed" {
			d.packed = true
		}
	}
	return d, true
}

func structDesc(t reflect.Type) []fieldDesc {
	var out []fieldDesc
	for i := 0; i < t.NumField(); i++ {
		d, ok := parseTag(t.Field(i))
		if !ok {
			continue
		}
		d.index = i
		d.typ = t.Field(i).Type
		out = append(out, *d)
	}
	return out
}

// wireTypeFor returns the wire type used by a (non-packed) scalar kind.
func wireTypeFor(kind string) (WireType, error) {
	switch kind {
	case "varint", "zigzag":
		return WireVarint, nil
	case "fixed32", "sfixed32":
		return WireFixed32, nil
	case "fixed64", "sfixed64":
		return WireFixed64, nil
	case "bytes":
		return WireBytes, nil
	}
	return 0, fmt.Errorf("wire: unknown kind %q", kind)
}

// Marshal encodes a tagged struct (or pointer to one) into protobuf wire format.
func Marshal(v interface{}) ([]byte, error) {
	rv := reflect.Indirect(reflect.ValueOf(v))
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("wire: Marshal expects struct, got %s", rv.Kind())
	}
	var out []byte
	for _, d := range structDesc(rv.Type()) {
		fv := rv.Field(d.index)
		var err error
		out, err = marshalField(out, d, fv)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func marshalField(out []byte, d fieldDesc, fv reflect.Value) ([]byte, error) {
	if fv.Kind() == reflect.Slice && d.kind != "bytes" {
		// Repeated scalar. Packed when tagged, else one record per element.
		if d.packed {
			var body []byte
			for i := 0; i < fv.Len(); i++ {
				body = appendScalar(body, d, fv.Index(i))
			}
			out = AppendVarint(out, Key(d.number, WireBytes))
			out = AppendVarint(out, uint64(len(body)))
			return append(out, body...), nil
		}
		for i := 0; i < fv.Len(); i++ {
			wt, err := wireTypeFor(d.kind)
			if err != nil {
				return nil, err
			}
			out = AppendVarint(out, Key(d.number, wt))
			out = appendScalar(out, d, fv.Index(i))
		}
		return out, nil
	}

	wt, err := wireTypeFor(d.kind)
	if err != nil {
		return nil, err
	}
	switch d.kind {
	case "bytes":
		switch fv.Kind() {
		case reflect.String:
			if fv.Len() == 0 {
				return out, nil
			}
			out = AppendVarint(out, Key(d.number, WireBytes))
			out = AppendVarint(out, uint64(fv.Len()))
			return append(out, fv.String()...), nil
		case reflect.Slice:
			if fv.Type().Elem().Kind() == reflect.Uint8 { // []byte
				if fv.Len() == 0 {
					return out, nil
				}
				out = AppendVarint(out, Key(d.number, WireBytes))
				out = AppendVarint(out, uint64(fv.Len()))
				return append(out, fv.Bytes()...), nil
			}
			if fv.Type().Elem().Kind() == reflect.Struct { // repeated embedded message
				for i := 0; i < fv.Len(); i++ {
					body, err := Marshal(fv.Index(i).Addr().Interface())
					if err != nil {
						return nil, err
					}
					out = AppendVarint(out, Key(d.number, WireBytes))
					out = AppendVarint(out, uint64(len(body)))
					out = append(out, body...)
				}
				return out, nil
			}
			return nil, fmt.Errorf("wire: unsupported bytes field type %s", fv.Type())
		case reflect.Struct, reflect.Ptr:
			if fv.Kind() == reflect.Ptr && fv.IsNil() {
				return out, nil
			}
			body, err := Marshal(fv.Interface())
			if err != nil {
				return nil, err
			}
			out = AppendVarint(out, Key(d.number, WireBytes))
			out = AppendVarint(out, uint64(len(body)))
			return append(out, body...), nil
		}
		return nil, fmt.Errorf("wire: unsupported bytes field type %s", fv.Kind())
	default:
		if isZeroValue(fv) {
			return out, nil // proto3-style: zero values are omitted
		}
		out = AppendVarint(out, Key(d.number, wt))
		return appendScalar(out, d, fv), nil
	}
}

func appendScalar(out []byte, d fieldDesc, fv reflect.Value) []byte {
	switch d.kind {
	case "varint":
		switch fv.Kind() {
		case reflect.Bool:
			if fv.Bool() {
				return AppendVarint(out, 1)
			}
			return AppendVarint(out, 0)
		case reflect.Int32, reflect.Int64, reflect.Int:
			return AppendVarint(out, uint64(fv.Int()))
		default:
			return AppendVarint(out, fv.Uint())
		}
	case "zigzag":
		return AppendVarint(out, ZigZagEncode(fv.Int()))
	case "fixed32":
		return AppendFixed32(out, uint32(fv.Uint()))
	case "sfixed32":
		return AppendFixed32(out, uint32(int32(fv.Int())))
	case "fixed64":
		return AppendFixed64(out, fv.Uint())
	case "sfixed64":
		return AppendFixed64(out, uint64(fv.Int()))
	}
	return out
}

func isZeroValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.String, reflect.Slice:
		return v.Len() == 0
	}
	return false
}

// Unmarshal decodes protobuf wire data into a tagged struct pointer.
// Unknown field numbers are skipped safely. Truncated or malformed input
// yields a *FieldError; the decoder never reads out of bounds.
func Unmarshal(data []byte, v interface{}) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("wire: Unmarshal expects pointer to struct")
	}
	st := rv.Elem()
	descs := structDesc(st.Type())
	byNumber := make(map[int]fieldDesc, len(descs))
	for _, d := range descs {
		byNumber[d.number] = d
	}

	fields, err := ParseMessage(data)
	if err != nil {
		return err
	}
	for _, f := range fields {
		d, ok := byNumber[f.Number]
		if !ok {
			continue // unknown field: skip
		}
		fv := st.Field(d.index)
		if err := assignField(fv, d, f); err != nil {
			return err
		}
	}
	return nil
}

func assignField(fv reflect.Value, d fieldDesc, f Field) error {
	// Packed repeated scalar arrives as one WireBytes field.
	if fv.Kind() == reflect.Slice && d.kind != "bytes" && f.Type == WireBytes {
		return unpackPacked(fv, d, f.Bytes)
	}
	wt, err := wireTypeFor(d.kind)
	if err != nil {
		return err
	}
	if d.kind == "bytes" {
		if f.Type != WireBytes {
			return &FieldError{Field: f.Number, Op: "assign", Err: fmt.Errorf("expected length-delimited, got type %d", f.Type)}
		}
		switch fv.Kind() {
		case reflect.String:
			fv.SetString(string(f.Bytes))
		case reflect.Slice:
			if fv.Type().Elem().Kind() == reflect.Uint8 {
				cp := make([]byte, len(f.Bytes))
				copy(cp, f.Bytes)
				fv.SetBytes(cp)
				return nil
			}
			if fv.Type().Elem().Kind() == reflect.Struct { // repeated embedded message
				elem := reflect.New(fv.Type().Elem())
				if err := Unmarshal(f.Bytes, elem.Interface()); err != nil {
					return err
				}
				fv.Set(reflect.Append(fv, elem.Elem()))
				return nil
			}
			return fmt.Errorf("wire: unsupported bytes target %s", fv.Type())
		case reflect.Struct:
			return Unmarshal(f.Bytes, fv.Addr().Interface())
		case reflect.Ptr:
			nv := reflect.New(fv.Type().Elem())
			if err := Unmarshal(f.Bytes, nv.Interface()); err != nil {
				return err
			}
			fv.Set(nv)
		default:
			return fmt.Errorf("wire: unsupported bytes target %s", fv.Kind())
		}
		return nil
	}
	if f.Type != wt {
		return &FieldError{Field: f.Number, Op: "assign", Err: fmt.Errorf("wire type mismatch: want %d got %d", wt, f.Type)}
	}
	// Repeated scalar, non-packed: append one element per record.
	if fv.Kind() == reflect.Slice {
		elem := reflect.New(fv.Type().Elem()).Elem()
		if err := setScalar(elem, d, f); err != nil {
			return err
		}
		fv.Set(reflect.Append(fv, elem))
		return nil
	}
	return setScalar(fv, d, f)
}

func unpackPacked(fv reflect.Value, d fieldDesc, body []byte) error {
	elemT := fv.Type().Elem()
	for len(body) > 0 {
		elem := reflect.New(elemT).Elem()
		var n int
		var err error
		switch d.kind {
		case "varint", "zigzag":
			var v uint64
			v, n, err = ConsumeVarint(body)
			if err != nil {
				return &FieldError{Field: d.number, Op: "read packed element", Err: err}
			}
			if d.kind == "zigzag" {
				elem.SetInt(ZigZagDecode(v))
			} else if elem.Kind() == reflect.Bool {
				elem.SetBool(v != 0)
			} else if isSigned(elem.Kind()) {
				elem.SetInt(int64(v))
			} else {
				elem.SetUint(v)
			}
		case "fixed32", "sfixed32":
			var v uint32
			v, n, err = ConsumeFixed32(body)
			if err != nil {
				return &FieldError{Field: d.number, Op: "read packed element", Err: err}
			}
			if isSigned(elem.Kind()) {
				elem.SetInt(int64(int32(v)))
			} else {
				elem.SetUint(uint64(v))
			}
		case "fixed64", "sfixed64":
			var v uint64
			v, n, err = ConsumeFixed64(body)
			if err != nil {
				return &FieldError{Field: d.number, Op: "read packed element", Err: err}
			}
			if isSigned(elem.Kind()) {
				elem.SetInt(int64(v))
			} else {
				elem.SetUint(v)
			}
		default:
			return fmt.Errorf("wire: kind %q cannot be packed", d.kind)
		}
		body = body[n:]
		fv.Set(reflect.Append(fv, elem))
	}
	return nil
}

func setScalar(fv reflect.Value, d fieldDesc, f Field) error {
	switch d.kind {
	case "varint":
		switch fv.Kind() {
		case reflect.Bool:
			fv.SetBool(f.Varint != 0)
		case reflect.Int, reflect.Int32, reflect.Int64:
			fv.SetInt(int64(f.Varint))
		default:
			fv.SetUint(f.Varint)
		}
	case "zigzag":
		fv.SetInt(ZigZagDecode(f.Varint))
	case "fixed32":
		fv.SetUint(uint64(f.Fixed32))
	case "sfixed32":
		fv.SetInt(int64(int32(f.Fixed32)))
	case "fixed64":
		fv.SetUint(f.Fixed64)
	case "sfixed64":
		fv.SetInt(int64(f.Fixed64))
	default:
		return fmt.Errorf("wire: cannot assign kind %q to scalar", d.kind)
	}
	return nil
}

func isSigned(k reflect.Kind) bool {
	return k == reflect.Int || k == reflect.Int32 || k == reflect.Int64
}

// Validate checks a tagged struct against its `validate` rules.
// Supported rules: nonzero, min=N, max=N (on signed integers and string/
// slice length). Nested structs and slices of structs are validated
// recursively. Returns nil when every rule passes.
func Validate(v interface{}) error {
	rv := reflect.Indirect(reflect.ValueOf(v))
	if !rv.IsValid() || rv.Kind() != reflect.Struct {
		return fmt.Errorf("wire: Validate expects struct, got %v", rv.Kind())
	}
	return validateStruct(rv, rv.Type().Name())
}

func validateStruct(rv reflect.Value, path string) error {
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		fv := rv.Field(i)
		name := path + "." + sf.Name

		switch fv.Kind() {
		case reflect.Struct:
			if err := validateStruct(fv, name); err != nil {
				return err
			}
		case reflect.Ptr:
			if !fv.IsNil() && fv.Elem().Kind() == reflect.Struct {
				if err := validateStruct(fv.Elem(), name); err != nil {
					return err
				}
			}
		case reflect.Slice:
			if fv.Type().Elem().Kind() == reflect.Struct {
				for j := 0; j < fv.Len(); j++ {
					if err := validateStruct(fv.Index(j), fmt.Sprintf("%s[%d]", name, j)); err != nil {
						return err
					}
				}
			}
		}

		tag := sf.Tag.Get("validate")
		if tag == "" {
			continue
		}
		for _, rule := range strings.Split(tag, ",") {
			if err := applyRule(fv, rule); err != nil {
				return fmt.Errorf("validate: %s: %v", name, err)
			}
		}
	}
	return nil
}

func applyRule(fv reflect.Value, rule string) error {
	switch {
	case rule == "nonzero":
		if isZeroValue(fv) {
			return fmt.Errorf("must be non-zero")
		}
	case strings.HasPrefix(rule, "min="):
		bound, err := strconv.ParseInt(rule[4:], 10, 64)
		if err != nil {
			return err
		}
		if numericOf(fv) < bound {
			return fmt.Errorf("below min %d", bound)
		}
	case strings.HasPrefix(rule, "max="):
		bound, err := strconv.ParseInt(rule[4:], 10, 64)
		if err != nil {
			return err
		}
		if numericOf(fv) > bound {
			return fmt.Errorf("above max %d", bound)
		}
	default:
		return fmt.Errorf("unknown rule %q", rule)
	}
	return nil
}

func numericOf(fv reflect.Value) int64 {
	switch fv.Kind() {
	case reflect.Int, reflect.Int32, reflect.Int64:
		return fv.Int()
	case reflect.Uint, reflect.Uint32, reflect.Uint64:
		return int64(fv.Uint())
	case reflect.String, reflect.Slice:
		return int64(fv.Len())
	}
	return 0
}
