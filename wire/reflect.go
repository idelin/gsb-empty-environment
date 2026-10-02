package wire

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// fieldKind is the schema kind declared in a `pb` struct tag.
type fieldKind int

const (
	kindVarint fieldKind = iota // uint32/uint64/int32/int64 (two's complement varint)
	kindZigzag                  // sint32/sint64
	kindBool
	kindFixed32
	kindFixed64
	kindString
	kindBytes
	kindMessage // embedded struct, length-delimited
	kindPacked  // packed repeated varint (slices of ints)
)

// tag syntax: `pb:"<fieldNum>,<kind>"` e.g. `pb:"1,varint"`, `pb:"3,msg"`.
func parseTag(tag string) (num int, kind fieldKind, err error) {
	if tag == "" {
		return 0, 0, fmt.Errorf("empty pb tag")
	}
	parts := strings.Split(tag, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("pb tag %q must be \"<number>,<kind>\"", tag)
	}
	num, err = strconv.Atoi(parts[0])
	if err != nil || num <= 0 || num > MaxFieldNumber {
		return 0, 0, fmt.Errorf("pb tag %q has invalid field number", tag)
	}
	switch parts[1] {
	case "varint":
		kind = kindVarint
	case "zigzag":
		kind = kindZigzag
	case "bool":
		kind = kindBool
	case "fixed32":
		kind = kindFixed32
	case "fixed64":
		kind = kindFixed64
	case "string":
		kind = kindString
	case "bytes":
		kind = kindBytes
	case "msg":
		kind = kindMessage
	case "packed":
		kind = kindPacked
	default:
		return 0, 0, fmt.Errorf("pb tag %q has unknown kind %q", tag, parts[1])
	}
	return num, kind, nil
}

// Unmarshal decodes buf into out, which must be a pointer to a struct
// whose exported fields carry `pb:"<number>,<kind>"` tags. It validates
// wire types against the declared kinds, enforces the nesting depth
// limit, and returns *Error (inspectable via CodeOf) on any violation.
// Unknown fields in the stream are skipped, matching protobuf semantics.
func Unmarshal(buf []byte, out interface{}) error {
	return unmarshalDepth(buf, out, DefaultMaxDepth)
}

// UnmarshalDepth is Unmarshal with an explicit nesting limit.
func UnmarshalDepth(buf []byte, out interface{}, maxDepth int) error {
	return unmarshalDepth(buf, out, maxDepth)
}

func unmarshalDepth(buf []byte, out interface{}, depth int) error {
	if depth < 0 {
		return newError(ErrDepthLimit, 0, 0, "nesting exceeds maximum depth")
	}
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return newError(ErrUnsupported, 0, 0, "out must be a non-nil pointer to struct")
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return newError(ErrUnsupported, 0, 0, "out must point to a struct, got %s", rv.Kind())
	}

	// Index target fields by protobuf field number.
	type target struct {
		val  reflect.Value
		kind fieldKind
	}
	byNum := make(map[int]target)
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		sf := rt.Field(i)
		tag := sf.Tag.Get("pb")
		if tag == "" {
			continue
		}
		num, kind, err := parseTag(tag)
		if err != nil {
			return newError(ErrUnsupported, 0, 0, "field %s: %v", sf.Name, err)
		}
		byNum[num] = target{val: rv.Field(i), kind: kind}
	}

	r := NewReader(buf)
	for !r.EOF() {
		off := r.Offset()
		num, wt, err := r.ReadKey()
		if err != nil {
			return err
		}
		tgt, ok := byNum[num]
		if !ok {
			if err := r.Skip(wt); err != nil {
				return err
			}
			continue
		}
		if err := assignField(r, tgt.val, tgt.kind, wt, num, off, depth); err != nil {
			return err
		}
	}
	return nil
}

func mismatch(off, num int, kind string, wt WireType) *Error {
	return newError(ErrTypeMismatch, off, num,
		"tag kind %s cannot read wire type %s", kind, wt)
}

func assignField(r *Reader, val reflect.Value, kind fieldKind, wt WireType, num, off, depth int) error {
	switch kind {
	case kindVarint, kindZigzag, kindBool:
		if wt != WireVarint {
			return mismatch(off, num, "varint-family", wt)
		}
		v, err := r.ReadVarint()
		if err != nil {
			return err
		}
		return setScalar(val, kind, v)
	case kindFixed32:
		if wt != WireFixed32 {
			return mismatch(off, num, "fixed32", wt)
		}
		v, err := r.ReadFixed32()
		if err != nil {
			return err
		}
		return setFixed(val, uint64(v))
	case kindFixed64:
		if wt != WireFixed64 {
			return mismatch(off, num, "fixed64", wt)
		}
		v, err := r.ReadFixed64()
		if err != nil {
			return err
		}
		return setFixed(val, v)
	case kindString:
		if wt != WireLengthDelimited {
			return mismatch(off, num, "string", wt)
		}
		b, err := r.ReadLengthDelimited()
		if err != nil {
			return err
		}
		if val.Kind() != reflect.String {
			return newError(ErrUnsupported, off, num, "string kind needs string field, got %s", val.Kind())
		}
		val.SetString(string(b))
		return nil
	case kindBytes:
		if wt != WireLengthDelimited {
			return mismatch(off, num, "bytes", wt)
		}
		b, err := r.ReadLengthDelimited()
		if err != nil {
			return err
		}
		if val.Kind() != reflect.Slice || val.Type().Elem().Kind() != reflect.Uint8 {
			return newError(ErrUnsupported, off, num, "bytes kind needs []byte field, got %s", val.Type())
		}
		cp := make([]byte, len(b))
		copy(cp, b)
		val.SetBytes(cp)
		return nil
	case kindMessage:
		if wt != WireLengthDelimited {
			return mismatch(off, num, "msg", wt)
		}
		b, err := r.ReadLengthDelimited()
		if err != nil {
			return err
		}
		return assignMessage(val, b, off, num, depth)
	case kindPacked:
		if wt != WireLengthDelimited {
			return mismatch(off, num, "packed", wt)
		}
		b, err := r.ReadLengthDelimited()
		if err != nil {
			return err
		}
		return assignPacked(val, b, off, num)
	}
	return newError(ErrUnsupported, off, num, "unhandled kind %d", int(kind))
}

func setScalar(val reflect.Value, kind fieldKind, v uint64) error {
	switch val.Kind() {
	case reflect.Bool:
		if kind != kindBool {
			return newError(ErrUnsupported, 0, 0, "bool field requires bool kind")
		}
		val.SetBool(v != 0)
	case reflect.Int32, reflect.Int64:
		if kind == kindZigzag {
			val.SetInt(ZigZag64(v))
		} else {
			val.SetInt(int64(v))
		}
	case reflect.Uint32, reflect.Uint64:
		val.SetUint(v)
	default:
		return newError(ErrUnsupported, 0, 0, "varint kind cannot fill %s", val.Kind())
	}
	return nil
}

func setFixed(val reflect.Value, v uint64) error {
	switch val.Kind() {
	case reflect.Uint32, reflect.Uint64, reflect.Int32, reflect.Int64:
		val.SetUint(v & maskFor(val.Kind()))
	case reflect.Float32:
		val.SetFloat(float64(math.Float32frombits(uint32(v))))
	case reflect.Float64:
		val.SetFloat(math.Float64frombits(v))
	default:
		return newError(ErrUnsupported, 0, 0, "fixed kind cannot fill %s", val.Kind())
	}
	return nil
}

func maskFor(k reflect.Kind) uint64 {
	if k == reflect.Uint32 || k == reflect.Int32 {
		return 0xffffffff
	}
	return ^uint64(0)
}

func assignMessage(val reflect.Value, b []byte, off, num, depth int) error {
	switch val.Kind() {
	case reflect.Struct:
		return unmarshalDepth(b, val.Addr().Interface(), depth-1)
	case reflect.Ptr:
		if val.Type().Elem().Kind() != reflect.Struct {
			return newError(ErrUnsupported, off, num, "msg kind needs struct or *struct, got %s", val.Type())
		}
		p := reflect.New(val.Type().Elem())
		if err := unmarshalDepth(b, p.Interface(), depth-1); err != nil {
			return err
		}
		val.Set(p)
		return nil
	case reflect.Slice:
		// Repeated embedded message: append one element per occurrence.
		elem := val.Type().Elem()
		isPtr := elem.Kind() == reflect.Ptr
		base := elem
		if isPtr {
			base = elem.Elem()
		}
		if base.Kind() != reflect.Struct {
			return newError(ErrUnsupported, off, num, "repeated msg needs []struct or []*struct, got %s", val.Type())
		}
		p := reflect.New(base)
		if err := unmarshalDepth(b, p.Interface(), depth-1); err != nil {
			return err
		}
		if isPtr {
			val.Set(reflect.Append(val, p))
		} else {
			val.Set(reflect.Append(val, p.Elem()))
		}
		return nil
	default:
		return newError(ErrUnsupported, off, num, "msg kind cannot fill %s", val.Kind())
	}
}

func assignPacked(val reflect.Value, b []byte, off, num int) error {
	if val.Kind() != reflect.Slice {
		return newError(ErrUnsupported, off, num, "packed kind needs a slice field, got %s", val.Kind())
	}
	elem := val.Type().Elem().Kind()
	r := NewReader(b)
	for !r.EOF() {
		v, err := r.ReadVarint()
		if err != nil {
			return err
		}
		switch elem {
		case reflect.Int32, reflect.Int64:
			val.Set(reflect.Append(val, reflect.ValueOf(ZigZag64(v)).Convert(val.Type().Elem())))
		case reflect.Uint32, reflect.Uint64:
			val.Set(reflect.Append(val, reflect.ValueOf(v).Convert(val.Type().Elem())))
		default:
			return newError(ErrUnsupported, off, num, "packed kind cannot fill []%s", elem)
		}
	}
	return nil
}
