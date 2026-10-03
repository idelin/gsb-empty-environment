package wire

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// ErrTypeMismatch is returned when a wire type cannot fill the Go field
// selected by the struct tag.
var ErrTypeMismatch = errors.New("wire: wire type does not match destination field")

// ErrInvalidTarget is returned when Unmarshal's out argument is not a
// non-nil pointer to a struct.
var ErrInvalidTarget = errors.New("wire: target must be a non-nil pointer to struct")

// Struct tag grammar: `pb:"<field-number>,<kind>"` where kind is one of
// varint, zigzag, fixed32, fixed64, bytes. Slices of numeric/bool fields
// accept both packed (BytesType) and unpacked encodings.
type fieldMeta struct {
	index int
	num   int
	kind  string
}

var (
	metaMu    sync.RWMutex
	metaCache = map[reflect.Type][]fieldMeta{}
)

func fieldsOf(t reflect.Type) ([]fieldMeta, error) {
	metaMu.RLock()
	metas, ok := metaCache[t]
	metaMu.RUnlock()
	if ok {
		return metas, nil
	}
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag, ok := sf.Tag.Lookup("pb")
		if !ok {
			continue
		}
		parts := strings.SplitN(tag, ",", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("wire: bad pb tag %q on %s.%s", tag, t.Name(), sf.Name)
		}
		num, err := strconv.Atoi(parts[0])
		if err != nil || num < 1 || num > MaxFieldNumber {
			return nil, fmt.Errorf("wire: bad field number in tag %q on %s.%s", tag, t.Name(), sf.Name)
		}
		switch parts[1] {
		case "varint", "zigzag", "fixed32", "fixed64", "bytes":
		default:
			return nil, fmt.Errorf("wire: unknown kind %q on %s.%s", parts[1], t.Name(), sf.Name)
		}
		metas = append(metas, fieldMeta{index: i, num: num, kind: parts[1]})
	}
	metaMu.Lock()
	metaCache[t] = metas
	metaMu.Unlock()
	return metas, nil
}

// Unmarshal decodes a protobuf wire-format message into out, which must be
// a non-nil pointer to a struct whose fields carry `pb` tags. Unknown field
// numbers are skipped, matching protobuf semantics. It is safe for
// concurrent use.
func Unmarshal(data []byte, out interface{}) error {
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Ptr || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return ErrInvalidTarget
	}
	return unmarshalInto(data, rv.Elem())
}

func unmarshalInto(data []byte, dst reflect.Value) error {
	metas, err := fieldsOf(dst.Type())
	if err != nil {
		return err
	}
	byNum := make(map[int]fieldMeta, len(metas))
	for _, m := range metas {
		byNum[m.num] = m
	}
	fields, err := ParseMessage(data)
	if err != nil {
		return err
	}
	for _, f := range fields {
		m, ok := byNum[f.Number]
		if !ok {
			continue // unknown field: skip
		}
		if err := assign(dst.Field(m.index), m, f); err != nil {
			return fmt.Errorf("wire: field %d: %v", f.Number, err)
		}
	}
	return nil
}

func assign(dst reflect.Value, m fieldMeta, f Field) error {
	if dst.Kind() == reflect.Slice && dst.Type().Elem().Kind() != reflect.Uint8 {
		return assignRepeated(dst, m, f)
	}
	return assignScalar(dst, m, f)
}

func assignScalar(dst reflect.Value, m fieldMeta, f Field) error {
	switch dst.Kind() {
	case reflect.Bool:
		if f.Type != VarintType {
			return ErrTypeMismatch
		}
		dst.SetBool(f.Varint != 0)
	case reflect.Int32, reflect.Int64:
		v, err := signedValue(m, f, dst.Type().Bits())
		if err != nil {
			return err
		}
		dst.SetInt(v)
	case reflect.Uint32, reflect.Uint64:
		v, err := unsignedValue(m, f)
		if err != nil {
			return err
		}
		dst.SetUint(v)
	case reflect.Float32:
		if f.Type != Fixed32Type {
			return ErrTypeMismatch
		}
		dst.SetFloat(float64(math.Float32frombits(f.Fixed32)))
	case reflect.Float64:
		if f.Type != Fixed64Type {
			return ErrTypeMismatch
		}
		dst.SetFloat(math.Float64frombits(f.Fixed64))
	case reflect.String:
		if f.Type != BytesType {
			return ErrTypeMismatch
		}
		dst.SetString(string(f.Bytes))
	case reflect.Slice: // []byte
		if f.Type != BytesType {
			return ErrTypeMismatch
		}
		cp := make([]byte, len(f.Bytes))
		copy(cp, f.Bytes)
		dst.SetBytes(cp)
	case reflect.Ptr:
		if f.Type != BytesType || dst.Type().Elem().Kind() != reflect.Struct {
			return ErrTypeMismatch
		}
		sub := reflect.New(dst.Type().Elem())
		if err := unmarshalInto(f.Bytes, sub.Elem()); err != nil {
			return err
		}
		dst.Set(sub)
	default:
		return ErrTypeMismatch
	}
	return nil
}

func assignRepeated(dst reflect.Value, m fieldMeta, f Field) error {
	elem := dst.Type().Elem()
	if elem.Kind() == reflect.Ptr && elem.Elem().Kind() == reflect.Struct {
		if f.Type != BytesType {
			return ErrTypeMismatch
		}
		sub := reflect.New(elem.Elem())
		if err := unmarshalInto(f.Bytes, sub.Elem()); err != nil {
			return err
		}
		dst.Set(reflect.Append(dst, sub))
		return nil
	}
	if elem.Kind() == reflect.String {
		if f.Type != BytesType {
			return ErrTypeMismatch
		}
		dst.Set(reflect.Append(dst, reflect.ValueOf(string(f.Bytes))))
		return nil
	}
	// Numeric/bool repeated: packed (BytesType) or a single unpacked value.
	if f.Type == BytesType {
		return appendPacked(dst, m, f.Bytes)
	}
	one := reflect.New(elem).Elem()
	if err := assignScalar(one, m, f); err != nil {
		return err
	}
	dst.Set(reflect.Append(dst, one))
	return nil
}

func appendPacked(dst reflect.Value, m fieldMeta, buf []byte) error {
	elem := dst.Type().Elem()
	switch elem.Kind() {
	case reflect.Bool, reflect.Int32, reflect.Int64, reflect.Uint32, reflect.Uint64:
		vals, err := ConsumePackedVarints(buf)
		if err != nil {
			return err
		}
		for _, v := range vals {
			one := reflect.New(elem).Elem()
			if err := assignScalar(one, m, Field{Number: m.num, Type: VarintType, Varint: v}); err != nil {
				return err
			}
			dst.Set(reflect.Append(dst, one))
		}
	case reflect.Float32:
		vals, err := ConsumePackedFixed32(buf)
		if err != nil {
			return err
		}
		for _, v := range vals {
			dst.Set(reflect.Append(dst, reflect.ValueOf(math.Float32frombits(v)).Convert(elem)))
		}
	case reflect.Float64:
		vals, err := ConsumePackedFixed64(buf)
		if err != nil {
			return err
		}
		for _, v := range vals {
			dst.Set(reflect.Append(dst, reflect.ValueOf(math.Float64frombits(v)).Convert(elem)))
		}
	default:
		return ErrTypeMismatch
	}
	return nil
}

func signedValue(m fieldMeta, f Field, bits int) (int64, error) {
	switch m.kind {
	case "varint":
		if f.Type != VarintType {
			return 0, ErrTypeMismatch
		}
		return int64(f.Varint), nil
	case "zigzag":
		if f.Type != VarintType {
			return 0, ErrTypeMismatch
		}
		if bits == 32 {
			return int64(DecodeZigZag32(uint32(f.Varint))), nil
		}
		return DecodeZigZag64(f.Varint), nil
	case "fixed32":
		if f.Type != Fixed32Type {
			return 0, ErrTypeMismatch
		}
		return int64(int32(f.Fixed32)), nil
	case "fixed64":
		if f.Type != Fixed64Type {
			return 0, ErrTypeMismatch
		}
		return int64(f.Fixed64), nil
	}
	return 0, ErrTypeMismatch
}

func unsignedValue(m fieldMeta, f Field) (uint64, error) {
	switch m.kind {
	case "varint":
		if f.Type != VarintType {
			return 0, ErrTypeMismatch
		}
		return f.Varint, nil
	case "fixed32":
		if f.Type != Fixed32Type {
			return 0, ErrTypeMismatch
		}
		return uint64(f.Fixed32), nil
	case "fixed64":
		if f.Type != Fixed64Type {
			return 0, ErrTypeMismatch
		}
		return f.Fixed64, nil
	}
	return 0, ErrTypeMismatch
}
