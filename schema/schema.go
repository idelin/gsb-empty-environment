// Package schema is a reflection-driven engine that validates struct schemas
// and marshals/unmarshals them to and from the protobuf binary wire format
// implemented by the wire package. Field mapping is declared with struct
// tags:
//
//	type Point struct {
//	    X uint32 `protobuf:"fixed32,1"`
//	    Y uint32 `protobuf:"fixed32,2"`
//	}
//
// Supported kinds: varint, zigzag, fixed32, fixed64, bytes (string/[]byte)
// and message (nested struct). Slices of numeric kinds are encoded as packed
// repeated fields; slices of string/[]byte/struct are encoded as repeated
// length-delimited fields.
package schema

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"wiresnap/wire"
)

type kind int

const (
	kindVarint kind = iota
	kindZigZag
	kindFixed32
	kindFixed64
	kindBytes
	kindMessage
)

var kindNames = map[string]kind{
	"varint":  kindVarint,
	"zigzag":  kindZigZag,
	"fixed32": kindFixed32,
	"fixed64": kindFixed64,
	"bytes":   kindBytes,
	"message": kindMessage,
}

func (k kind) wireType() int {
	switch k {
	case kindVarint, kindZigZag:
		return wire.WireVarint
	case kindFixed32:
		return wire.WireFixed32
	case kindFixed64:
		return wire.WireFixed64
	default:
		return wire.WireBytes
	}
}

type fieldInfo struct {
	name     string
	index    int
	num      int
	kind     kind
	typ      reflect.Type // element type for slices
	repeated bool
}

// Validate checks that v (a struct or pointer to struct) declares a coherent
// protobuf schema: parseable tags, unique field numbers and Go types that are
// compatible with the declared kind.
func Validate(v interface{}) error {
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return fmt.Errorf("schema: expected struct, got %s", t.Kind())
	}
	_, err := parseStruct(t)
	return err
}

func parseStruct(t reflect.Type) ([]fieldInfo, error) {
	seen := map[int]string{}
	var fields []fieldInfo
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag, ok := f.Tag.Lookup("protobuf")
		if !ok {
			continue
		}
		fi, err := parseField(f, tag)
		if err != nil {
			return nil, fmt.Errorf("schema: field %s: %w", f.Name, err)
		}
		if prev, dup := seen[fi.num]; dup {
			return nil, fmt.Errorf("schema: field %s: number %d already used by %s", f.Name, fi.num, prev)
		}
		seen[fi.num] = f.Name
		fi.index = i
		fields = append(fields, fi)
	}
	return fields, nil
}

func parseField(f reflect.StructField, tag string) (fieldInfo, error) {
	parts := strings.Split(tag, ",")
	if len(parts) != 2 {
		return fieldInfo{}, fmt.Errorf("tag must be \"kind,number\", got %q", tag)
	}
	k, ok := kindNames[parts[0]]
	if !ok {
		return fieldInfo{}, fmt.Errorf("unknown kind %q", parts[0])
	}
	num, err := strconv.Atoi(parts[1])
	if err != nil || num <= 0 {
		return fieldInfo{}, fmt.Errorf("invalid field number %q", parts[1])
	}
	fi := fieldInfo{name: f.Name, num: num, kind: k, typ: f.Type}
	if f.Type.Kind() == reflect.Slice && f.Type.Elem().Kind() != reflect.Uint8 {
		fi.repeated = true
		fi.typ = f.Type.Elem()
	}
	if err := checkType(fi); err != nil {
		return fieldInfo{}, err
	}
	return fi, nil
}

func checkType(fi fieldInfo) error {
	t := fi.typ
	bad := func() error {
		return fmt.Errorf("kind %q incompatible with Go type %s", kindName(fi.kind), t)
	}
	switch fi.kind {
	case kindVarint:
		switch t.Kind() {
		case reflect.Uint32, reflect.Uint64, reflect.Int32, reflect.Int64, reflect.Bool:
		default:
			return bad()
		}
	case kindZigZag:
		switch t.Kind() {
		case reflect.Int32, reflect.Int64:
		default:
			return bad()
		}
	case kindFixed32:
		switch t.Kind() {
		case reflect.Uint32, reflect.Float32:
		default:
			return bad()
		}
	case kindFixed64:
		switch t.Kind() {
		case reflect.Uint64, reflect.Float64:
		default:
			return bad()
		}
	case kindBytes:
		if t.Kind() != reflect.String && !(t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8) {
			return bad()
		}
	case kindMessage:
		for t.Kind() == reflect.Ptr {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return bad()
		}
		if _, err := parseStruct(t); err != nil {
			return err
		}
	}
	return nil
}

func kindName(k kind) string {
	for name, kk := range kindNames {
		if kk == k {
			return name
		}
	}
	return "?"
}

// Marshal encodes v (struct or pointer to struct) into protobuf wire format.
func Marshal(v interface{}) ([]byte, error) {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil, fmt.Errorf("schema: cannot marshal nil pointer")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("schema: expected struct, got %s", rv.Kind())
	}
	return marshalStruct(rv)
}

func marshalStruct(rv reflect.Value) ([]byte, error) {
	fields, err := parseStruct(rv.Type())
	if err != nil {
		return nil, err
	}
	var buf []byte
	for _, fi := range fields {
		fv := rv.Field(fi.index)
		var err error
		buf, err = appendField(buf, fi, fv)
		if err != nil {
			return nil, fmt.Errorf("schema: marshal field %s: %w", fi.name, err)
		}
	}
	return buf, nil
}

func appendField(buf []byte, fi fieldInfo, fv reflect.Value) ([]byte, error) {
	if fi.repeated {
		return appendRepeated(buf, fi, fv)
	}
	// proto3 semantics: zero-value fields are omitted from the encoding.
	if fv.IsZero() {
		return buf, nil
	}
	if fi.kind == kindMessage && fv.Kind() == reflect.Ptr && fv.IsNil() {
		return buf, nil // absent optional message
	}
	body, err := encodeValue(fi.kind, fi.typ, fv)
	if err != nil {
		return nil, err
	}
	buf = wire.AppendTag(buf, fi.num, fi.kind.wireType())
	if fi.kind.wireType() == wire.WireBytes {
		return wire.AppendBytes(buf, body), nil
	}
	return append(buf, body...), nil
}

func appendRepeated(buf []byte, fi fieldInfo, fv reflect.Value) ([]byte, error) {
	n := fv.Len()
	if n == 0 {
		return buf, nil
	}
	switch fi.kind {
	case kindVarint, kindZigZag, kindFixed32, kindFixed64:
		// Packed encoding: one tag, one length prefix, raw values.
		var body []byte
		for i := 0; i < n; i++ {
			b, err := encodeValue(fi.kind, fi.typ, fv.Index(i))
			if err != nil {
				return nil, err
			}
			body = append(body, b...)
		}
		buf = wire.AppendTag(buf, fi.num, wire.WireBytes)
		return wire.AppendBytes(buf, body), nil
	default:
		// Repeated length-delimited: one tag+value per element.
		for i := 0; i < n; i++ {
			body, err := encodeValue(fi.kind, fi.typ, fv.Index(i))
			if err != nil {
				return nil, err
			}
			buf = wire.AppendTag(buf, fi.num, wire.WireBytes)
			buf = wire.AppendBytes(buf, body)
		}
		return buf, nil
	}
}

// encodeValue encodes a single scalar/message value without its tag.
func encodeValue(k kind, t reflect.Type, fv reflect.Value) ([]byte, error) {
	for fv.Kind() == reflect.Ptr {
		if fv.IsNil() {
			return nil, fmt.Errorf("nil pointer")
		}
		fv = fv.Elem()
	}
	switch k {
	case kindVarint:
		switch t.Kind() {
		case reflect.Bool:
			if fv.Bool() {
				return []byte{1}, nil
			}
			return []byte{0}, nil
		case reflect.Int32, reflect.Int64:
			return wire.AppendVarint(nil, uint64(fv.Int())), nil
		default:
			return wire.AppendVarint(nil, fv.Uint()), nil
		}
	case kindZigZag:
		return wire.AppendZigZag(nil, fv.Int()), nil
	case kindFixed32:
		if t.Kind() == reflect.Float32 {
			return wire.AppendFixed32(nil, math.Float32bits(float32(fv.Float()))), nil
		}
		return wire.AppendFixed32(nil, uint32(fv.Uint())), nil
	case kindFixed64:
		if t.Kind() == reflect.Float64 {
			return wire.AppendFixed64(nil, math.Float64bits(fv.Float())), nil
		}
		return wire.AppendFixed64(nil, fv.Uint()), nil
	case kindBytes:
		if t.Kind() == reflect.String {
			return []byte(fv.String()), nil
		}
		return fv.Bytes(), nil
	case kindMessage:
		return marshalStruct(fv)
	}
	return nil, fmt.Errorf("unknown kind")
}

// Unmarshal decodes protobuf wire data into out, which must be a pointer to
// a struct. Truncated or malformed input is reported as an error wrapping
// wire.ErrTruncated / wire.ErrOverflow; unknown fields are skipped.
func Unmarshal(data []byte, out interface{}) error {
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("schema: out must be a non-nil pointer")
	}
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			rv.Set(reflect.New(rv.Type().Elem()))
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("schema: out must point to a struct, got %s", rv.Kind())
	}
	return unmarshalStruct(wire.NewReader(data), rv)
}

func unmarshalStruct(r *wire.Reader, rv reflect.Value) error {
	fields, err := parseStruct(rv.Type())
	if err != nil {
		return err
	}
	byNum := make(map[int]fieldInfo, len(fields))
	for _, fi := range fields {
		byNum[fi.num] = fi
	}
	for !r.Done() {
		num, wt, err := r.ReadTag()
		if err != nil {
			return err
		}
		fi, ok := byNum[num]
		if !ok {
			if err := r.SkipValue(wt); err != nil {
				return fmt.Errorf("schema: skip unknown field %d: %w", num, err)
			}
			continue
		}
		if err := decodeField(r, rv.Field(fi.index), fi, wt); err != nil {
			return fmt.Errorf("schema: field %s (num %d): %w", fi.name, num, err)
		}
	}
	return nil
}

func decodeField(r *wire.Reader, fv reflect.Value, fi fieldInfo, wt int) error {
	if fi.repeated {
		return decodeRepeated(r, fv, fi, wt)
	}
	if wt != fi.kind.wireType() {
		return fmt.Errorf("wire type %d does not match kind %q", wt, kindName(fi.kind))
	}
	return decodeValue(r, fi.kind, fv)
}

func decodeRepeated(r *wire.Reader, fv reflect.Value, fi fieldInfo, wt int) error {
	switch fi.kind {
	case kindVarint, kindZigZag, kindFixed32, kindFixed64:
		if wt == wire.WireBytes {
			// Packed form.
			packed, err := r.ReadBytes()
			if err != nil {
				return err
			}
			pr := wire.NewReader(packed)
			for !pr.Done() {
				elem := reflect.New(fi.typ).Elem()
				if err := decodeValue(pr, fi.kind, elem); err != nil {
					return fmt.Errorf("packed element: %w", err)
				}
				fv.Set(reflect.Append(fv, elem))
			}
			return nil
		}
		// Non-packed repeated scalar: one value per tag.
		if wt != fi.kind.wireType() {
			return fmt.Errorf("wire type %d does not match kind %q", wt, kindName(fi.kind))
		}
		elem := reflect.New(fi.typ).Elem()
		if err := decodeValue(r, fi.kind, elem); err != nil {
			return err
		}
		fv.Set(reflect.Append(fv, elem))
		return nil
	default:
		if wt != wire.WireBytes {
			return fmt.Errorf("wire type %d does not match kind %q", wt, kindName(fi.kind))
		}
		elem := reflect.New(fi.typ).Elem()
		if err := decodeValue(r, fi.kind, elem); err != nil {
			return err
		}
		fv.Set(reflect.Append(fv, elem))
		return nil
	}
}

// decodeValue decodes one value of the given kind into fv. For kindMessage
// and kindBytes it consumes the length-delimited body itself.
func decodeValue(r *wire.Reader, k kind, fv reflect.Value) error {
	for fv.Kind() == reflect.Ptr {
		fv.Set(reflect.New(fv.Type().Elem()))
		fv = fv.Elem()
	}
	switch k {
	case kindVarint:
		v, err := r.ReadVarint()
		if err != nil {
			return err
		}
		switch fv.Kind() {
		case reflect.Bool:
			fv.SetBool(v != 0)
		case reflect.Int32, reflect.Int64:
			fv.SetInt(int64(v))
		default:
			fv.SetUint(v)
		}
		return nil
	case kindZigZag:
		v, err := r.ReadZigZag()
		if err != nil {
			return err
		}
		fv.SetInt(v)
		return nil
	case kindFixed32:
		v, err := r.ReadFixed32()
		if err != nil {
			return err
		}
		if fv.Kind() == reflect.Float32 {
			fv.SetFloat(float64(math.Float32frombits(v)))
		} else {
			fv.SetUint(uint64(v))
		}
		return nil
	case kindFixed64:
		v, err := r.ReadFixed64()
		if err != nil {
			return err
		}
		if fv.Kind() == reflect.Float64 {
			fv.SetFloat(math.Float64frombits(v))
		} else {
			fv.SetUint(v)
		}
		return nil
	case kindBytes:
		b, err := r.ReadBytes()
		if err != nil {
			return err
		}
		if fv.Kind() == reflect.String {
			fv.SetString(string(b))
		} else {
			fv.SetBytes(append([]byte(nil), b...))
		}
		return nil
	case kindMessage:
		b, err := r.ReadBytes()
		if err != nil {
			return err
		}
		return unmarshalStruct(wire.NewReader(b), fv)
	}
	return fmt.Errorf("unknown kind")
}
