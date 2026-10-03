// Package schema is a reflection-driven validation engine for the wire
// codec. Struct fields declare their protobuf mapping with tags, e.g.:
//
//	type Envelope struct {
//	    ID    uint64   `protobuf:"1,varint"`
//	    Name  string   `protobuf:"2,string"`
//	    Score int64    `protobuf:"3,zigzag"`
//	    Tags  []uint32 `protobuf:"4,packed,varint"`
//	    Inner *Inner   `protobuf:"5,message"`
//	}
//
// Validate inspects a struct type and reports malformed tags, unsupported
// kinds, duplicate field numbers and type/codec mismatches before any byte
// is encoded. Marshal and Unmarshal convert between validated structs and
// the protobuf binary wire format, with strict bounds checking on decode.
package schema

import (
	"fmt"
	"reflect"
	"strings"

	"wirefmt/wire"
)

// Codec names usable in the `protobuf` tag.
const (
	CodecVarint  = "varint"  // uint/int/bool as varint, int64/int32 as two's complement
	CodecZigZag  = "zigzag"  // sint32/sint64
	CodecFixed32 = "fixed32" // also float32
	CodecFixed64 = "fixed64" // also float64
	CodecString  = "string"
	CodecBytes   = "bytes"
	CodecMessage = "message" // nested struct or *struct
)

// FieldInfo is the validated mapping of one struct field.
type FieldInfo struct {
	Number  int
	Codec   string
	Packed  bool
	Index   int // struct field index
	GoType  reflect.Type
	WireTyp int
}

// MessageInfo is the validated schema of a struct type.
type MessageInfo struct {
	Type   reflect.Type
	Fields []FieldInfo
	byNum  map[int]FieldInfo
}

// ByNumber returns the field with the given protobuf number.
func (m *MessageInfo) ByNumber(n int) (FieldInfo, bool) {
	f, ok := m.byNum[n]
	return f, ok
}

var schemaCache = newCache()

// Validate inspects the type of v (a struct or pointer to struct) and
// returns its validated message schema. It is safe for concurrent use.
func Validate(v interface{}) (*MessageInfo, error) {
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("schema: %v is not a struct type", t)
	}
	if info, ok := schemaCache.get(t); ok {
		return info, nil
	}
	info, err := buildInfo(t)
	if err != nil {
		return nil, err
	}
	schemaCache.put(t, info)
	return info, nil
}

func buildInfo(t reflect.Type) (*MessageInfo, error) {
	info := &MessageInfo{Type: t, byNum: make(map[int]FieldInfo)}
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag := sf.Tag.Get("protobuf")
		if tag == "" || tag == "-" {
			continue
		}
		if sf.PkgPath != "" {
			return nil, fmt.Errorf("schema: %s.%s: unexported field cannot be mapped", t.Name(), sf.Name)
		}
		fi, err := parseField(sf, tag, i)
		if err != nil {
			return nil, fmt.Errorf("schema: %s.%s: %v", t.Name(), sf.Name, err)
		}
		if _, dup := info.byNum[fi.Number]; dup {
			return nil, fmt.Errorf("schema: %s.%s: duplicate field number %d", t.Name(), sf.Name, fi.Number)
		}
		info.byNum[fi.Number] = fi
		info.Fields = append(info.Fields, fi)
	}
	if len(info.Fields) == 0 {
		return nil, fmt.Errorf("schema: %s: no protobuf-tagged fields", t.Name())
	}
	return info, nil
}

func parseField(sf reflect.StructField, tag string, index int) (FieldInfo, error) {
	parts := strings.Split(tag, ",")
	if len(parts) < 2 {
		return FieldInfo{}, fmt.Errorf("tag %q needs at least number and codec", tag)
	}
	var fi FieldInfo
	if _, err := fmt.Sscanf(parts[0], "%d", &fi.Number); err != nil || fi.Number <= 0 || fi.Number > wire.MaxFieldNumber {
		return FieldInfo{}, fmt.Errorf("illegal field number %q", parts[0])
	}
	fi.Index = index
	fi.GoType = sf.Type
	fi.Packed = false

	opts := parts[1:]
	codec := ""
	for _, o := range opts {
		if o == "packed" {
			fi.Packed = true
			continue
		}
		if codec != "" {
			return FieldInfo{}, fmt.Errorf("multiple codecs in tag %q", tag)
		}
		codec = o
	}
	fi.Codec = codec

	if err := checkType(fi, sf.Type); err != nil {
		return FieldInfo{}, err
	}
	fi.WireTyp = wireTypeOf(fi)
	return fi, nil
}

// baseKind resolves the kind that a codec operates on: the element kind for
// slices, the struct itself otherwise.
func baseKind(t reflect.Type) reflect.Kind {
	if t.Kind() == reflect.Slice && t.Elem().Kind() != reflect.Uint8 {
		return t.Elem().Kind()
	}
	return t.Kind()
}

func checkType(fi FieldInfo, t reflect.Type) error {
	isSlice := t.Kind() == reflect.Slice && t.Elem().Kind() != reflect.Uint8
	if isSlice && !fi.Packed && fi.Codec != CodecMessage && fi.Codec != CodecString && fi.Codec != CodecBytes {
		return fmt.Errorf("repeated field of codec %q must be packed", fi.Codec)
	}
	if fi.Packed && !isSlice {
		return fmt.Errorf("packed requires a slice field")
	}
	k := baseKind(t)
	switch fi.Codec {
	case CodecVarint:
		switch k {
		case reflect.Uint32, reflect.Uint64, reflect.Uint, reflect.Int32, reflect.Int64, reflect.Int, reflect.Bool:
		default:
			return fmt.Errorf("codec varint incompatible with kind %s", k)
		}
	case CodecZigZag:
		switch k {
		case reflect.Int32, reflect.Int64, reflect.Int:
		default:
			return fmt.Errorf("codec zigzag incompatible with kind %s", k)
		}
	case CodecFixed32:
		switch k {
		case reflect.Uint32, reflect.Int32, reflect.Float32:
		default:
			return fmt.Errorf("codec fixed32 incompatible with kind %s", k)
		}
	case CodecFixed64:
		switch k {
		case reflect.Uint64, reflect.Int64, reflect.Float64:
		default:
			return fmt.Errorf("codec fixed64 incompatible with kind %s", k)
		}
	case CodecString:
		if k != reflect.String {
			return fmt.Errorf("codec string incompatible with kind %s", k)
		}
	case CodecBytes:
		if t.Kind() != reflect.Slice || t.Elem().Kind() != reflect.Uint8 {
			return fmt.Errorf("codec bytes requires []byte")
		}
	case CodecMessage:
		ek := t.Kind()
		et := t
		if isSlice {
			et = t.Elem()
			ek = et.Kind()
		}
		if ek == reflect.Ptr {
			et = et.Elem()
			ek = et.Kind()
		}
		if ek != reflect.Struct {
			return fmt.Errorf("codec message requires struct, *struct or slice thereof")
		}
		if _, err := Validate(reflect.New(et).Interface()); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown codec %q", fi.Codec)
	}
	return nil
}

func wireTypeOf(fi FieldInfo) int {
	if fi.Packed {
		return wire.WireLengthPref
	}
	switch fi.Codec {
	case CodecVarint, CodecZigZag:
		return wire.WireVarint
	case CodecFixed32:
		return wire.WireFixed32
	case CodecFixed64:
		return wire.WireFixed64
	default:
		return wire.WireLengthPref
	}
}
