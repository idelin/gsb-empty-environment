// Package validate is a reflection-driven engine that unmarshals protobuf
// wire-format bytes into tagged Go structs and enforces validation rules.
//
// Struct tags:
//
//	pb:"1,varint"            uint64/int/enum-like kinds
//	pb:"2,zigzag"            signed ints via zigzag
//	pb:"3,fixed32"           uint32/float32 bit patterns
//	pb:"4,fixed64"           uint64/float64 bit patterns
//	pb:"5,bytes"             []byte
//	pb:"6,string"            string
//	pb:"7,packed"            []uint64/[]int64(zigzag)/[]uint32/[]uint64-fixed
//	pb:"8,message"           nested struct
//	validate:"required,min=1,max=100"
package validate

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"protowire/wire"
)

// Error describes a single decode or validation failure.
type Error struct {
	Field  string
	Rule   string
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("validate: field %s: %s: %s", e.Field, e.Rule, e.Detail)
}

type fieldMeta struct {
	index  int
	name   string
	number int
	kind   string
	rules  map[string]string
}

func parseMeta(t reflect.Type) ([]fieldMeta, error) {
	var metas []fieldMeta
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag, ok := sf.Tag.Lookup("pb")
		if !ok {
			continue
		}
		parts := strings.Split(tag, ",")
		if len(parts) != 2 {
			return nil, fmt.Errorf("validate: field %s: malformed pb tag %q", sf.Name, tag)
		}
		num, err := strconv.Atoi(parts[0])
		if err != nil || num < 1 {
			return nil, fmt.Errorf("validate: field %s: bad field number %q", sf.Name, parts[0])
		}
		m := fieldMeta{index: i, name: sf.Name, number: num, kind: parts[1], rules: map[string]string{}}
		if v := sf.Tag.Get("validate"); v != "" {
			for _, r := range strings.Split(v, ",") {
				if r == "required" {
					m.rules["required"] = ""
					continue
				}
				kv := strings.SplitN(r, "=", 2)
				if len(kv) == 2 {
					m.rules[kv[0]] = kv[1]
				}
			}
		}
		metas = append(metas, m)
	}
	return metas, nil
}

// Unmarshal parses data as a wire-format message and fills out, which must
// be a non-nil pointer to a struct, then applies validate-tag rules.
func Unmarshal(data []byte, out interface{}) error {
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Ptr || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("validate: out must be a non-nil pointer to struct, got %T", out)
	}
	fields, err := wire.ParseMessage(data)
	if err != nil {
		return err
	}
	return fill(rv.Elem(), fields)
}

func fill(rv reflect.Value, fields []wire.Field) error {
	metas, err := parseMeta(rv.Type())
	if err != nil {
		return err
	}
	for _, m := range metas {
		matches := wire.ByNumber(fields, m.number)
		fv := rv.Field(m.index)
		if len(matches) == 0 {
			if _, req := m.rules["required"]; req {
				return &Error{Field: m.name, Rule: "required", Detail: "field missing on wire"}
			}
			continue
		}
		if err := assign(fv, m, matches); err != nil {
			return err
		}
		if err := checkRules(fv, m); err != nil {
			return err
		}
	}
	return nil
}

func assign(fv reflect.Value, m fieldMeta, matches []wire.Field) error {
	switch m.kind {
	case "varint":
		f := matches[len(matches)-1]
		if f.Type != wire.WireVarint {
			return &Error{Field: m.name, Rule: "wiretype", Detail: "expected varint"}
		}
		return setUint(fv, m.name, f.Varint)
	case "zigzag":
		f := matches[len(matches)-1]
		if f.Type != wire.WireVarint {
			return &Error{Field: m.name, Rule: "wiretype", Detail: "expected varint"}
		}
		return setInt(fv, m.name, wire.DecodeZigZag(f.Varint))
	case "fixed32":
		f := matches[len(matches)-1]
		if f.Type != wire.WireFixed32 {
			return &Error{Field: m.name, Rule: "wiretype", Detail: "expected fixed32"}
		}
		return setUint(fv, m.name, uint64(f.Fixed32))
	case "fixed64":
		f := matches[len(matches)-1]
		if f.Type != wire.WireFixed64 {
			return &Error{Field: m.name, Rule: "wiretype", Detail: "expected fixed64"}
		}
		return setUint(fv, m.name, f.Fixed64)
	case "bytes":
		f := matches[len(matches)-1]
		if f.Type != wire.WireLengthDelimited {
			return &Error{Field: m.name, Rule: "wiretype", Detail: "expected length-delimited"}
		}
		fv.SetBytes(append([]byte(nil), f.Bytes...))
		return nil
	case "string":
		f := matches[len(matches)-1]
		if f.Type != wire.WireLengthDelimited {
			return &Error{Field: m.name, Rule: "wiretype", Detail: "expected length-delimited"}
		}
		fv.SetString(string(f.Bytes))
		return nil
	case "packed":
		return assignPacked(fv, m, matches[len(matches)-1])
	case "message":
		f := matches[len(matches)-1]
		if f.Type != wire.WireLengthDelimited {
			return &Error{Field: m.name, Rule: "wiretype", Detail: "expected length-delimited"}
		}
		nested, err := f.Nested()
		if err != nil {
			return err
		}
		target := fv
		if fv.Kind() == reflect.Ptr {
			target = reflect.New(fv.Type().Elem())
			fv.Set(target)
			target = target.Elem()
		}
		return fill(target, nested)
	default:
		return &Error{Field: m.name, Rule: "kind", Detail: "unknown pb kind " + m.kind}
	}
}

func assignPacked(fv reflect.Value, m fieldMeta, f wire.Field) error {
	if f.Type != wire.WireLengthDelimited {
		return &Error{Field: m.name, Rule: "wiretype", Detail: "packed field must be length-delimited"}
	}
	elem := fv.Type().Elem()
	var vals []uint64
	var err error
	switch elem.Kind() {
	case reflect.Uint32:
		var raw []uint32
		raw, err = wire.DecodePackedFixed32(f.Bytes)
		for _, v := range raw {
			vals = append(vals, uint64(v))
		}
	case reflect.Uint64:
		vals, err = wire.DecodePackedFixed64(f.Bytes)
	default: // int kinds: varint-packed
		vals, err = wire.DecodePackedVarints(f.Bytes)
	}
	if err != nil {
		return err
	}
	out := reflect.MakeSlice(fv.Type(), len(vals), len(vals))
	for i, v := range vals {
		e := out.Index(i)
		switch elem.Kind() {
		case reflect.Int, reflect.Int32, reflect.Int64:
			e.SetInt(wire.DecodeZigZag(v))
		default:
			e.SetUint(v)
		}
	}
	fv.Set(out)
	return nil
}

func setUint(fv reflect.Value, name string, v uint64) error {
	switch fv.Kind() {
	case reflect.Uint, reflect.Uint32, reflect.Uint64:
		if fv.OverflowUint(v) {
			return &Error{Field: name, Rule: "overflow", Detail: "value overflows destination"}
		}
		fv.SetUint(v)
	case reflect.Int, reflect.Int32, reflect.Int64:
		if fv.OverflowInt(int64(v)) {
			return &Error{Field: name, Rule: "overflow", Detail: "value overflows destination"}
		}
		fv.SetInt(int64(v))
	case reflect.Bool:
		fv.SetBool(v != 0)
	default:
		return &Error{Field: name, Rule: "kind", Detail: "unsupported destination kind " + fv.Kind().String()}
	}
	return nil
}

func setInt(fv reflect.Value, name string, v int64) error {
	if fv.Kind() != reflect.Int && fv.Kind() != reflect.Int32 && fv.Kind() != reflect.Int64 {
		return &Error{Field: name, Rule: "kind", Detail: "zigzag requires signed destination"}
	}
	if fv.OverflowInt(v) {
		return &Error{Field: name, Rule: "overflow", Detail: "value overflows destination"}
	}
	fv.SetInt(v)
	return nil
}

func checkRules(fv reflect.Value, m fieldMeta) error {
	if raw, ok := m.rules["min"]; ok {
		min, _ := strconv.ParseInt(raw, 10, 64)
		if numericValue(fv) < min {
			return &Error{Field: m.name, Rule: "min", Detail: fmt.Sprintf("%d < %d", numericValue(fv), min)}
		}
	}
	if raw, ok := m.rules["max"]; ok {
		max, _ := strconv.ParseInt(raw, 10, 64)
		if numericValue(fv) > max {
			return &Error{Field: m.name, Rule: "max", Detail: fmt.Sprintf("%d > %d", numericValue(fv), max)}
		}
	}
	return nil
}

func numericValue(fv reflect.Value) int64 {
	switch fv.Kind() {
	case reflect.Int, reflect.Int32, reflect.Int64:
		return fv.Int()
	case reflect.Uint, reflect.Uint32, reflect.Uint64:
		return int64(fv.Uint())
	}
	return 0
}
