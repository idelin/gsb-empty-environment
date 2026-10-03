// Package schema is a reflection-driven engine that maps parsed protobuf
// fields onto Go structs and validates them. Field bindings come from the
// `protobuf` struct tag; constraints come from the `validate` tag.
//
// Tag grammar:
//
//	protobuf:"<kind>,<num>[,rep][,packed]"
//	validate:"required,min=<n>,max=<n>,minlen=<n>,maxlen=<n>"
//
// Supported kinds: varint, zigzag32, zigzag64, fixed32, fixed64,
// bytes, string, message.
package schema

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"protowire/proto"
	"protowire/wire"
)

// Validatable is implemented by structs that carry domain-level invariants.
// The engine invokes Validate after field binding and tag checks.
type Validatable interface {
	Validate() error
}

// fieldBinding describes one tagged struct field.
type fieldBinding struct {
	index  int
	kind   string
	num    int
	rep    bool
	packed bool
	rules  rules
}

type rules struct {
	required bool
	hasMin   bool
	min      int64
	hasMax   bool
	max      int64
	minLen   int
	maxLen   int
}

// ValidationError aggregates all constraint violations found in one pass.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "schema: validation failed: " + strings.Join(e.Problems, "; ")
}

func (e *ValidationError) add(format string, args ...interface{}) {
	e.Problems = append(e.Problems, fmt.Sprintf(format, args...))
}

// Unmarshal parses data as a protobuf message and binds it onto v, which
// must be a pointer to a struct. Unknown fields are ignored (forward
// compatible). After binding, tag constraints and Validate() are checked;
// violations are returned as a *ValidationError.
func Unmarshal(data []byte, v interface{}) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("schema: target must be pointer to struct, got %T", v)
	}
	msg, err := proto.Parse(data)
	if err != nil {
		return err
	}
	return bind(msg, rv.Elem())
}

func bind(msg proto.Message, sv reflect.Value) error {
	bindings, err := bindingsFor(sv.Type())
	if err != nil {
		return err
	}
	verr := &ValidationError{}
	for _, b := range bindings {
		fields := msg.ByNum(b.num)
		fv := sv.Field(b.index)
		if len(fields) == 0 {
			if b.rules.required {
				verr.add("field %d (%s) is required but missing", b.num, sv.Type().Field(b.index).Name)
			}
			continue
		}
		if err := bindField(fv, b, fields); err != nil {
			return err
		}
		checkRules(verr, sv.Type().Field(b.index).Name, fv, b.rules)
	}
	if len(verr.Problems) > 0 {
		return verr
	}
	// Domain-level validation hook (also on nested structs, invoked during
	// their own bind).
	if sv.CanAddr() {
		if va, ok := sv.Addr().Interface().(Validatable); ok {
			if err := va.Validate(); err != nil {
				return &ValidationError{Problems: []string{err.Error()}}
			}
		}
	}
	return nil
}

func bindField(fv reflect.Value, b fieldBinding, fields []proto.Field) error {
	if b.rep {
		return bindRepeated(fv, b, fields)
	}
	// Last-one-wins for singular fields, per protobuf semantics.
	return bindSingular(fv, b, fields[len(fields)-1])
}

func bindSingular(fv reflect.Value, b fieldBinding, f proto.Field) error {
	switch b.kind {
	case "varint":
		if f.WireType != proto.WireVarint {
			return wireTypeErr(b.num, b.kind, f.WireType)
		}
		return setUintOrBool(fv, f.U64)
	case "zigzag32":
		if f.WireType != proto.WireVarint {
			return wireTypeErr(b.num, b.kind, f.WireType)
		}
		fv.SetInt(int64(wire.ZigZagDecode32(uint32(f.U64))))
	case "zigzag64":
		if f.WireType != proto.WireVarint {
			return wireTypeErr(b.num, b.kind, f.WireType)
		}
		fv.SetInt(wire.ZigZagDecode64(f.U64))
	case "fixed32":
		if f.WireType != proto.WireFixed32 {
			return wireTypeErr(b.num, b.kind, f.WireType)
		}
		return setUintOrFloat(fv, f.U64, 32)
	case "fixed64":
		if f.WireType != proto.WireFixed64 {
			return wireTypeErr(b.num, b.kind, f.WireType)
		}
		return setUintOrFloat(fv, f.U64, 64)
	case "string":
		if f.WireType != proto.WireLengthDelimited {
			return wireTypeErr(b.num, b.kind, f.WireType)
		}
		fv.SetString(string(f.Bytes))
	case "bytes":
		if f.WireType != proto.WireLengthDelimited {
			return wireTypeErr(b.num, b.kind, f.WireType)
		}
		cp := make([]byte, len(f.Bytes))
		copy(cp, f.Bytes)
		fv.SetBytes(cp)
	case "message":
		if f.WireType != proto.WireLengthDelimited {
			return wireTypeErr(b.num, b.kind, f.WireType)
		}
		sub, err := f.ParseNested(0)
		if err != nil {
			return err
		}
		target := fv
		if fv.Kind() == reflect.Ptr {
			if fv.IsNil() {
				fv.Set(reflect.New(fv.Type().Elem()))
			}
			target = fv.Elem()
		}
		return bind(sub, target)
	default:
		return fmt.Errorf("schema: unknown kind %q", b.kind)
	}
	return nil
}

func bindRepeated(fv reflect.Value, b fieldBinding, fields []proto.Field) error {
	if b.packed {
		// Packed: elements live inside one length-delimited payload.
		for _, f := range fields {
			if f.WireType != proto.WireLengthDelimited {
				return wireTypeErr(b.num, b.kind+" packed", f.WireType)
			}
			if err := appendPacked(fv, b.kind, f.Bytes); err != nil {
				return err
			}
		}
		return nil
	}
	// Unpacked: one field occurrence per element.
	for _, f := range fields {
		elem := reflect.New(fv.Type().Elem()).Elem()
		if err := bindSingular(elem, b, f); err != nil {
			return err
		}
		fv.Set(reflect.Append(fv, elem))
	}
	return nil
}

func appendPacked(fv reflect.Value, kind string, payload []byte) error {
	elemType := fv.Type().Elem()
	switch kind {
	case "varint", "zigzag32", "zigzag64":
		us, err := proto.PackedVarints(payload)
		if err != nil {
			return err
		}
		for _, u := range us {
			elem := reflect.New(elemType).Elem()
			switch kind {
			case "varint":
				if err := setUintOrBool(elem, u); err != nil {
					return err
				}
			case "zigzag32":
				elem.SetInt(int64(wire.ZigZagDecode32(uint32(u))))
			case "zigzag64":
				elem.SetInt(wire.ZigZagDecode64(u))
			}
			fv.Set(reflect.Append(fv, elem))
		}
	case "fixed32":
		us, err := proto.PackedFixed32(payload)
		if err != nil {
			return err
		}
		for _, u := range us {
			elem := reflect.New(elemType).Elem()
			if err := setUintOrFloat(elem, uint64(u), 32); err != nil {
				return err
			}
			fv.Set(reflect.Append(fv, elem))
		}
	case "fixed64":
		us, err := proto.PackedFixed64(payload)
		if err != nil {
			return err
		}
		for _, u := range us {
			elem := reflect.New(elemType).Elem()
			if err := setUintOrFloat(elem, u, 64); err != nil {
				return err
			}
			fv.Set(reflect.Append(fv, elem))
		}
	default:
		return fmt.Errorf("schema: kind %q cannot be packed", kind)
	}
	return nil
}

func setUintOrBool(fv reflect.Value, u uint64) error {
	switch fv.Kind() {
	case reflect.Bool:
		fv.SetBool(u != 0)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		fv.SetUint(u)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		fv.SetInt(int64(u))
	default:
		return fmt.Errorf("schema: cannot assign varint to %s", fv.Type())
	}
	return nil
}

func setUintOrFloat(fv reflect.Value, u uint64, bits int) error {
	switch fv.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		fv.SetUint(u)
	case reflect.Float32:
		fv.SetFloat(float64(math.Float32frombits(uint32(u))))
	case reflect.Float64:
		fv.SetFloat(math.Float64frombits(u))
	default:
		return fmt.Errorf("schema: cannot assign fixed%d to %s", bits, fv.Type())
	}
	return nil
}

func checkRules(verr *ValidationError, name string, fv reflect.Value, r rules) {
	if r.required && isZero(fv) {
		verr.add("%s is required but zero", name)
	}
	if r.hasMin || r.hasMax {
		var n int64
		switch fv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			n = fv.Int()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			n = int64(fv.Uint())
		default:
			return
		}
		if r.hasMin && n < r.min {
			verr.add("%s=%d below min %d", name, n, r.min)
		}
		if r.hasMax && n > r.max {
			verr.add("%s=%d above max %d", name, n, r.max)
		}
	}
	if r.minLen >= 0 || r.maxLen >= 0 {
		var l int
		switch fv.Kind() {
		case reflect.String, reflect.Slice, reflect.Array, reflect.Map:
			l = fv.Len()
		default:
			return
		}
		if r.minLen >= 0 && l < r.minLen {
			verr.add("%s length %d below minlen %d", name, l, r.minLen)
		}
		if r.maxLen >= 0 && l > r.maxLen {
			verr.add("%s length %d above maxlen %d", name, l, r.maxLen)
		}
	}
}

func isZero(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.String:
		return v.Len() == 0
	case reflect.Slice, reflect.Map, reflect.Ptr:
		return v.IsNil()
	}
	return false
}

func wireTypeErr(num int, kind string, got int) error {
	return fmt.Errorf("schema: field %d kind %q but wire type %d", num, kind, got)
}

// bindingsFor parses the tags of t into field bindings.
func bindingsFor(t reflect.Type) ([]fieldBinding, error) {
	var out []fieldBinding
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag := sf.Tag.Get("protobuf")
		if tag == "" || tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		if len(parts) < 2 {
			return nil, fmt.Errorf("schema: bad protobuf tag %q on %s.%s", tag, t.Name(), sf.Name)
		}
		num, err := strconv.Atoi(parts[1])
		if err != nil || num <= 0 {
			return nil, fmt.Errorf("schema: bad field number in tag %q on %s.%s", tag, t.Name(), sf.Name)
		}
		b := fieldBinding{index: i, kind: parts[0], num: num, rules: rules{minLen: -1, maxLen: -1}}
		for _, opt := range parts[2:] {
			switch opt {
			case "rep":
				b.rep = true
			case "packed":
				b.packed = true
			default:
				return nil, fmt.Errorf("schema: unknown option %q on %s.%s", opt, t.Name(), sf.Name)
			}
		}
		if err := parseRules(sf.Tag.Get("validate"), &b.rules); err != nil {
			return nil, fmt.Errorf("schema: %s.%s: %v", t.Name(), sf.Name, err)
		}
		out = append(out, b)
	}
	return out, nil
}

func parseRules(tag string, r *rules) error {
	if tag == "" {
		return nil
	}
	for _, rule := range strings.Split(tag, ",") {
		if rule == "required" {
			r.required = true
			continue
		}
		kv := strings.SplitN(rule, "=", 2)
		if len(kv) != 2 {
			return fmt.Errorf("bad validate rule %q", rule)
		}
		n, err := strconv.ParseInt(kv[1], 10, 64)
		if err != nil {
			return fmt.Errorf("bad validate rule %q: %v", rule, err)
		}
		switch kv[0] {
		case "min":
			r.hasMin, r.min = true, n
		case "max":
			r.hasMax, r.max = true, n
		case "minlen":
			r.minLen = int(n)
		case "maxlen":
			r.maxLen = int(n)
		default:
			return fmt.Errorf("unknown validate rule %q", kv[0])
		}
	}
	return nil
}
