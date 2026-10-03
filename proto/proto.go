// Package proto is a reflection-driven codec and validation engine built on
// the hand-rolled wire package. Struct fields are mapped with tags:
//
//	type Msg struct {
//	    ID   uint64   `wire:"1,varint" check:"required,min=1"`
//	    Name string   `wire:"2,bytes"  check:"minlen=1,maxlen=64"`
//	    Nums []uint64 `wire:"3,varint,packed"`
//	    Sub  *Other   `wire:"4,bytes"`
//	}
//
// Supported kinds: varint, zigzag, fixed32, fixed64, bytes. Nested structs
// and pointers to structs are encoded as length-delimited embedded messages;
// numeric slices support packed repeated encoding.
package proto

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"compact/wire"
)

var byteSliceType = reflect.TypeOf([]byte(nil))

type fieldInfo struct {
	num    int
	kind   string
	packed bool
	index  int
	typ    reflect.Type
	name   string
	check  string
}

type structInfo struct {
	fields   []*fieldInfo
	byNumber map[int]*fieldInfo
}

var cache sync.Map // reflect.Type -> *structInfo

func getStructInfo(t reflect.Type) *structInfo {
	if v, ok := cache.Load(t); ok {
		return v.(*structInfo)
	}
	si := &structInfo{byNumber: make(map[int]*fieldInfo)}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("wire")
		if tag == "" || tag == "-" || f.PkgPath != "" {
			continue
		}
		parts := strings.Split(tag, ",")
		num, err := strconv.Atoi(parts[0])
		if err != nil || num <= 0 {
			panic(fmt.Sprintf("proto: bad field number in tag %q on %s.%s", tag, t.Name(), f.Name))
		}
		fi := &fieldInfo{num: num, index: i, typ: f.Type, name: f.Name, check: f.Tag.Get("check")}
		for _, opt := range parts[1:] {
			switch opt {
			case "packed":
				fi.packed = true
			case "varint", "zigzag", "fixed32", "fixed64", "bytes":
				fi.kind = opt
			case "":
			default:
				panic(fmt.Sprintf("proto: unknown option %q on %s.%s", opt, t.Name(), f.Name))
			}
		}
		if fi.kind == "" {
			fi.kind = inferKind(f.Type)
		}
		si.fields = append(si.fields, fi)
		si.byNumber[fi.num] = fi
	}
	cache.Store(t, si)
	return si
}

func isNumericKind(k reflect.Kind) bool {
	switch k {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func isMessageType(t reflect.Type) bool {
	return t.Kind() == reflect.Struct ||
		(t.Kind() == reflect.Ptr && t.Elem().Kind() == reflect.Struct)
}

func inferKind(t reflect.Type) string {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() == reflect.Slice && t != byteSliceType {
		t = t.Elem()
		for t.Kind() == reflect.Ptr {
			t = t.Elem()
		}
	}
	switch t.Kind() {
	case reflect.Float32:
		return "fixed32"
	case reflect.Float64:
		return "fixed64"
	case reflect.String, reflect.Struct, reflect.Slice:
		return "bytes"
	default:
		return "varint"
	}
}

func expectedWireType(kind string) wire.WireType {
	switch kind {
	case "fixed32":
		return wire.Fixed32
	case "fixed64":
		return wire.Fixed64
	case "bytes":
		return wire.Bytes
	default:
		return wire.Varint
	}
}

// Encode serializes a struct (or pointer to struct) to protobuf wire format.
func Encode(v interface{}) ([]byte, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil, errors.New("proto: Encode of nil pointer")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("proto: Encode expects a struct, got %s", rv.Kind())
	}
	buf := wire.NewBuffer()
	if err := encodeStruct(buf, rv); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeStruct(buf *wire.Buffer, rv reflect.Value) error {
	si := getStructInfo(rv.Type())
	for _, fi := range si.fields {
		if err := encodeField(buf, fi, rv.Field(fi.index)); err != nil {
			return fmt.Errorf("proto: field %s: %v", fi.name, err)
		}
	}
	return nil
}

func encodeField(buf *wire.Buffer, fi *fieldInfo, fv reflect.Value) error {
	t := fi.typ
	if isMessageType(t) {
		if t.Kind() == reflect.Ptr {
			if fv.IsNil() {
				return nil
			}
		}
		return encodeMessage(buf, fi, fv)
	}
	if t.Kind() == reflect.Slice && t != byteSliceType {
		et := t.Elem()
		if isMessageType(et) {
			for i := 0; i < fv.Len(); i++ {
				if err := encodeMessage(buf, fi, fv.Index(i)); err != nil {
					return err
				}
			}
			return nil
		}
		if fi.packed && isNumericKind(et.Kind()) {
			if fv.Len() == 0 {
				return nil
			}
			inner := wire.NewBuffer()
			for i := 0; i < fv.Len(); i++ {
				appendScalar(inner, fi.kind, fv.Index(i))
			}
			buf.AppendTag(fi.num, wire.Bytes)
			buf.AppendBytes(inner.Bytes())
			return nil
		}
		for i := 0; i < fv.Len(); i++ {
			appendTagged(buf, fi, fv.Index(i))
		}
		return nil
	}
	appendTagged(buf, fi, fv)
	return nil
}

func encodeMessage(buf *wire.Buffer, fi *fieldInfo, fv reflect.Value) error {
	if fv.Kind() == reflect.Ptr {
		if fv.IsNil() {
			return nil
		}
		fv = fv.Elem()
	}
	inner := wire.NewBuffer()
	if err := encodeStruct(inner, fv); err != nil {
		return err
	}
	buf.AppendTag(fi.num, wire.Bytes)
	buf.AppendBytes(inner.Bytes())
	return nil
}

func appendTagged(buf *wire.Buffer, fi *fieldInfo, fv reflect.Value) {
	for fv.Kind() == reflect.Ptr {
		if fv.IsNil() {
			return
		}
		fv = fv.Elem()
	}
	buf.AppendTag(fi.num, expectedWireType(fi.kind))
	appendScalar(buf, fi.kind, fv)
}

func appendScalar(buf *wire.Buffer, kind string, fv reflect.Value) {
	switch kind {
	case "zigzag":
		buf.AppendZigZag(fv.Int())
	case "fixed32":
		if fv.Kind() == reflect.Float32 {
			buf.AppendFixed32(math.Float32bits(float32(fv.Float())))
		} else if isSignedKind(fv.Kind()) {
			buf.AppendFixed32(uint32(fv.Int()))
		} else {
			buf.AppendFixed32(uint32(fv.Uint()))
		}
	case "fixed64":
		if fv.Kind() == reflect.Float64 {
			buf.AppendFixed64(math.Float64bits(fv.Float()))
		} else if isSignedKind(fv.Kind()) {
			buf.AppendFixed64(uint64(fv.Int()))
		} else {
			buf.AppendFixed64(fv.Uint())
		}
	case "bytes":
		if fv.Kind() == reflect.String {
			buf.AppendBytes([]byte(fv.String()))
		} else {
			buf.AppendBytes(fv.Bytes())
		}
	default: // varint
		switch fv.Kind() {
		case reflect.Bool:
			if fv.Bool() {
				buf.AppendVarint(1)
			} else {
				buf.AppendVarint(0)
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			buf.AppendVarint(uint64(fv.Int()))
		default:
			buf.AppendVarint(fv.Uint())
		}
	}
}

func isSignedKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	}
	return false
}

// Decode parses protobuf wire data into the struct pointed to by v. Unknown
// fields are skipped. Truncated or malformed input returns an error; it
// never panics or reads out of bounds.
func Decode(data []byte, v interface{}) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return errors.New("proto: Decode expects a non-nil pointer to struct")
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("proto: Decode expects a pointer to struct, got pointer to %s", rv.Kind())
	}
	return decodeStruct(wire.NewReader(data), rv)
}

func decodeStruct(r *wire.Reader, rv reflect.Value) error {
	si := getStructInfo(rv.Type())
	for !r.EOF() {
		num, wt, err := r.ReadTag()
		if err != nil {
			return err
		}
		fi := si.byNumber[num]
		if fi == nil {
			if err := r.Skip(wt); err != nil {
				return err
			}
			continue
		}
		if err := decodeField(r, rv.Field(fi.index), fi, wt); err != nil {
			return fmt.Errorf("proto: field %s: %v", fi.name, err)
		}
	}
	return nil
}

func decodeField(r *wire.Reader, fv reflect.Value, fi *fieldInfo, wt wire.WireType) error {
	t := fi.typ
	if isMessageType(t) {
		if wt != wire.Bytes {
			return wire.ErrBadWireType
		}
		b, err := r.ReadBytes()
		if err != nil {
			return err
		}
		var target reflect.Value
		if t.Kind() == reflect.Ptr {
			target = reflect.New(t.Elem())
			fv.Set(target)
		} else {
			target = fv.Addr()
		}
		return decodeStruct(wire.NewReader(b), target.Elem())
	}
	if t.Kind() == reflect.Slice && t != byteSliceType {
		et := t.Elem()
		if isMessageType(et) {
			if wt != wire.Bytes {
				return wire.ErrBadWireType
			}
			b, err := r.ReadBytes()
			if err != nil {
				return err
			}
			var elem reflect.Value
			if et.Kind() == reflect.Ptr {
				elem = reflect.New(et.Elem())
			} else {
				elem = reflect.New(et)
			}
			if err := decodeStruct(wire.NewReader(b), elem.Elem()); err != nil {
				return err
			}
			if et.Kind() == reflect.Ptr {
				fv.Set(reflect.Append(fv, elem))
			} else {
				fv.Set(reflect.Append(fv, elem.Elem()))
			}
			return nil
		}
		// repeated scalar; packed encoding arrives as length-delimited
		if wt == wire.Bytes && isNumericKind(et.Kind()) {
			b, err := r.ReadBytes()
			if err != nil {
				return err
			}
			sub := wire.NewReader(b)
			for !sub.EOF() {
				v, err := readScalar(sub, fi.kind, et)
				if err != nil {
					return err
				}
				fv.Set(reflect.Append(fv, v))
			}
			return nil
		}
		v, err := readScalarChecked(r, fi, wt, et)
		if err != nil {
			return err
		}
		fv.Set(reflect.Append(fv, v))
		return nil
	}
	if t.Kind() == reflect.Ptr {
		v, err := readScalarChecked(r, fi, wt, t.Elem())
		if err != nil {
			return err
		}
		p := reflect.New(t.Elem())
		p.Elem().Set(v)
		fv.Set(p)
		return nil
	}
	v, err := readScalarChecked(r, fi, wt, t)
	if err != nil {
		return err
	}
	fv.Set(v)
	return nil
}

func readScalarChecked(r *wire.Reader, fi *fieldInfo, wt wire.WireType, t reflect.Type) (reflect.Value, error) {
	if wt != expectedWireType(fi.kind) {
		return reflect.Value{}, wire.ErrBadWireType
	}
	return readScalar(r, fi.kind, t)
}

func readScalar(r *wire.Reader, kind string, t reflect.Type) (reflect.Value, error) {
	switch kind {
	case "zigzag":
		z, err := r.ReadZigZag()
		if err != nil {
			return reflect.Value{}, err
		}
		v := reflect.New(t).Elem()
		v.SetInt(z)
		return v, nil
	case "fixed32":
		u, err := r.ReadFixed32()
		if err != nil {
			return reflect.Value{}, err
		}
		v := reflect.New(t).Elem()
		switch t.Kind() {
		case reflect.Float32:
			v.SetFloat(float64(math.Float32frombits(u)))
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			v.SetInt(int64(u))
		default:
			v.SetUint(uint64(u))
		}
		return v, nil
	case "fixed64":
		u, err := r.ReadFixed64()
		if err != nil {
			return reflect.Value{}, err
		}
		v := reflect.New(t).Elem()
		switch t.Kind() {
		case reflect.Float64:
			v.SetFloat(math.Float64frombits(u))
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			v.SetInt(int64(u))
		default:
			v.SetUint(uint64(u))
		}
		return v, nil
	case "bytes":
		b, err := r.ReadBytes()
		if err != nil {
			return reflect.Value{}, err
		}
		v := reflect.New(t).Elem()
		if t.Kind() == reflect.String {
			v.SetString(string(b))
		} else {
			v.SetBytes(append([]byte(nil), b...))
		}
		return v, nil
	default: // varint
		u, err := r.ReadVarint()
		if err != nil {
			return reflect.Value{}, err
		}
		v := reflect.New(t).Elem()
		switch t.Kind() {
		case reflect.Bool:
			v.SetBool(u != 0)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			v.SetInt(int64(u))
		default:
			v.SetUint(u)
		}
		return v, nil
	}
}
