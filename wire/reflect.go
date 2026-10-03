// Reflection-driven validation engine: decodes a protobuf wire stream into
// a tagged Go struct, verifying wire types, required fields and nested
// messages as it goes. Unknown fields are skipped safely; every malformed
// condition is reported, never panics.
package wire

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// FieldError reports a single validation failure for one struct field.
type FieldError struct {
	Path string // dotted struct path, e.g. "Envelope.Header"
	Num  int    // protobuf field number
	Err  error
}

func (e FieldError) Error() string {
	return fmt.Sprintf("%s (field %d): %v", e.Path, e.Num, e.Err)
}

// ValidationError aggregates every problem found while decoding a message.
type ValidationError struct {
	Problems []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		parts[i] = p.Error()
	}
	return "wire: validation failed: " + strings.Join(parts, "; ")
}

var (
	errTypeMismatch  = fmt.Errorf("wire type does not match schema")
	errMissingFields = fmt.Errorf("required field missing")
)

// fieldSpec is the parsed form of a `wire:"kind,num[,packed][,required]"`
// struct tag.
type fieldSpec struct {
	kind     string // varint, zigzag32, zigzag64, fixed32, fixed64, bytes, string, message
	num      int
	packed   bool
	required bool
	wireType WireType
}

func parseSpec(tag string, goType reflect.Type) (fieldSpec, error) {
	var s fieldSpec
	parts := strings.Split(tag, ",")
	if len(parts) < 2 {
		return s, fmt.Errorf("malformed tag %q", tag)
	}
	s.kind = parts[0]
	num, err := strconv.Atoi(parts[1])
	if err != nil || num <= 0 {
		return s, fmt.Errorf("bad field number in tag %q", tag)
	}
	s.num = num
	for _, opt := range parts[2:] {
		switch opt {
		case "packed":
			s.packed = true
		case "required":
			s.required = true
		default:
			return s, fmt.Errorf("unknown tag option %q", opt)
		}
	}
	if s.kind == "" {
		s.kind, err = inferKind(goType)
		if err != nil {
			return s, err
		}
	}
	switch s.kind {
	case "varint", "zigzag32", "zigzag64":
		s.wireType = WireVarint
	case "fixed32":
		s.wireType = WireFixed32
	case "fixed64":
		s.wireType = WireFixed64
	case "bytes", "string", "message":
		s.wireType = WireBytes
	default:
		return s, fmt.Errorf("unknown kind %q", s.kind)
	}
	return s, nil
}

// inferKind derives the wire kind from the Go type when the tag omits it.
func inferKind(t reflect.Type) (string, error) {
	if t.Kind() == reflect.Slice && t.Elem().Kind() != reflect.Uint8 {
		t = t.Elem() // repeated field: infer from element
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Uint64, reflect.Uint32, reflect.Uint, reflect.Bool:
		return "varint", nil
	case reflect.Int64, reflect.Int32, reflect.Int:
		return "zigzag64", nil
	case reflect.String:
		return "string", nil
	case reflect.Struct:
		return "message", nil
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return "bytes", nil
		}
	}
	return "", fmt.Errorf("cannot infer wire kind for Go type %s", t)
}

// Unmarshal decodes the protobuf wire stream b into out, which must be a
// pointer to a struct whose fields carry `wire:"kind,num,..."` tags.
// It returns *ParseError for malformed input and *ValidationError when the
// stream is well-formed but violates the schema.
func Unmarshal(b []byte, out interface{}) error {
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("wire: Unmarshal target must be a pointer to struct, got %T", out)
	}
	v := &validator{}
	if err := v.message(b, rv.Elem(), rv.Elem().Type().Name()); err != nil {
		return err
	}
	if len(v.problems) > 0 {
		return &ValidationError{Problems: v.problems}
	}
	return nil
}

type validator struct {
	problems []FieldError
}

func (v *validator) fail(path string, num int, err error) {
	v.problems = append(v.problems, FieldError{Path: path, Num: num, Err: err})
}

// structMeta maps protobuf field numbers to struct field indexes.
type structMeta struct {
	specs []fieldSpec
	index []int // parallel to specs: struct field index
}

func metaFor(t reflect.Type) (structMeta, error) {
	var m structMeta
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag := sf.Tag.Get("wire")
		if tag == "" || tag == "-" {
			continue
		}
		spec, err := parseSpec(tag, sf.Type)
		if err != nil {
			return m, fmt.Errorf("wire: struct %s field %s: %v", t.Name(), sf.Name, err)
		}
		m.specs = append(m.specs, spec)
		m.index = append(m.index, i)
	}
	return m, nil
}

func (v *validator) message(b []byte, sv reflect.Value, path string) error {
	meta, err := metaFor(sv.Type())
	if err != nil {
		return err
	}
	byNum := make(map[int]int) // field num -> position in meta.specs
	for i, s := range meta.specs {
		byNum[s.num] = i
	}
	seen := make(map[int]bool)

	for len(b) > 0 {
		f, n, err := NextField(b)
		if err != nil {
			return err // malformed stream: *ParseError, fail fast
		}
		b = b[n:]
		pos, known := byNum[f.Number]
		if !known {
			continue // unknown field: safely skipped
		}
		spec := meta.specs[pos]
		sf := sv.Field(meta.index[pos])
		sfType := sv.Type().Field(meta.index[pos])
		seen[spec.num] = true
		v.assign(f, spec, sf, path+"."+sfType.Name)
	}

	for i, spec := range meta.specs {
		if spec.required && !seen[spec.num] {
			sfType := sv.Type().Field(meta.index[i])
			v.fail(path+"."+sfType.Name, spec.num, errMissingFields)
		}
	}
	return nil
}

func (v *validator) assign(f Field, spec fieldSpec, dst reflect.Value, path string) {
	// Repeated targets: any slice except []byte (which is a bytes field).
	if dst.Kind() == reflect.Slice && dst.Type().Elem().Kind() != reflect.Uint8 {
		if spec.packed {
			if f.Type != WireBytes {
				v.fail(path, spec.num, errTypeMismatch)
				return
			}
			v.assignPacked(f, spec, dst, path)
			return
		}
		elem := reflect.New(dst.Type().Elem()).Elem()
		if !v.assignScalar(f, spec, elem, path) {
			return
		}
		dst.Set(reflect.Append(dst, elem))
		return
	}
	v.assignScalar(f, spec, dst, path)
}

func (v *validator) assignPacked(f Field, spec fieldSpec, dst reflect.Value, path string) {
	elemType := dst.Type().Elem()
	switch spec.kind {
	case "varint", "zigzag32", "zigzag64":
		vs, err := ConsumePackedVarints(f.Bytes)
		if err != nil {
			v.fail(path, spec.num, err)
			return
		}
		for _, x := range vs {
			elem := reflect.New(elemType).Elem()
			setVarintKind(elem, spec.kind, x)
			dst.Set(reflect.Append(dst, elem))
		}
	case "fixed32":
		vs, err := ConsumePackedFixed32(f.Bytes)
		if err != nil {
			v.fail(path, spec.num, err)
			return
		}
		for _, x := range vs {
			elem := reflect.New(elemType).Elem()
			elem.SetUint(uint64(x))
			dst.Set(reflect.Append(dst, elem))
		}
	case "fixed64":
		vs, err := ConsumePackedFixed64(f.Bytes)
		if err != nil {
			v.fail(path, spec.num, err)
			return
		}
		for _, x := range vs {
			elem := reflect.New(elemType).Elem()
			elem.SetUint(x)
			dst.Set(reflect.Append(dst, elem))
		}
	default:
		v.fail(path, spec.num, fmt.Errorf("kind %q cannot be packed", spec.kind))
	}
}

// assignScalar decodes one wire field into a non-slice destination. It
// reports false when the assignment failed (problem already recorded).
func (v *validator) assignScalar(f Field, spec fieldSpec, dst reflect.Value, path string) bool {
	if f.Type != spec.wireType {
		v.fail(path, spec.num, errTypeMismatch)
		return false
	}
	switch spec.kind {
	case "varint", "zigzag32", "zigzag64":
		setVarintKind(dst, spec.kind, f.Varint)
	case "fixed32":
		dst.SetUint(uint64(f.Fixed32))
	case "fixed64":
		dst.SetUint(f.Fixed64)
	case "bytes":
		dst.SetBytes(append([]byte(nil), f.Bytes...))
	case "string":
		dst.SetString(string(f.Bytes))
	case "message":
		target := dst
		if dst.Kind() == reflect.Ptr {
			target = reflect.New(dst.Type().Elem()).Elem()
		}
		if target.Kind() != reflect.Struct {
			v.fail(path, spec.num, fmt.Errorf("message kind requires struct target"))
			return false
		}
		if err := v.message(f.Bytes, target, path); err != nil {
			if pe, ok := err.(*ParseError); ok {
				v.fail(path, spec.num, pe)
				return false
			}
			return false
		}
		if dst.Kind() == reflect.Ptr {
			dst.Set(target.Addr())
		}
		return true
	}
	return true
}

func setVarintKind(dst reflect.Value, kind string, x uint64) {
	switch kind {
	case "zigzag32":
		dst.SetInt(int64(DecodeZigZag32(uint32(x))))
	case "zigzag64":
		dst.SetInt(DecodeZigZag64(x))
	default: // varint
		switch dst.Kind() {
		case reflect.Bool:
			dst.SetBool(x != 0)
		case reflect.Int, reflect.Int32, reflect.Int64:
			dst.SetInt(int64(x))
		default:
			dst.SetUint(x)
		}
	}
}
