package wire

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Reflection-mode validation engine. Struct fields carry a `validate` tag:
//
//	type Reading struct {
//	    SensorID uint64  `protobuf:"1,varint" validate:"required,min=1"`
//	    Temp     int64   `protobuf:"2,zigzag"  validate:"min=-40,max=125"`
//	    Label    string  `protobuf:"3,bytes"   validate:"required,len=1..64"`
//	    History  []int64 `protobuf:"4,zigzag,packed" validate:"len=1..1024"`
//	}
//
// Supported rules:
//   required       value must not be the zero value (nil pointer, empty
//                  string/slice/map, or numeric zero)
//   min=N / max=N  numeric bounds (inclusive) for ints, uints, floats
//   len=A..B       length bounds for strings and slices (B optional: len=A)
//
// Nested structs, struct pointers, and slices of structs are validated
// recursively. All violations are collected into a ValidationErrors.

// ValidationErrors is the list of every rule violation found in one pass.
type ValidationErrors []FieldError

// FieldError describes a single rule violation.
type FieldError struct {
	Path  string // dotted field path, e.g. "Header.Samples"
	Rule  string
	Value string
}

func (e FieldError) Error() string {
	return fmt.Sprintf("validate: %s violates %s (value %s)", e.Path, e.Rule, e.Value)
}

func (ve ValidationErrors) Error() string {
	if len(ve) == 0 {
		return "validate: no errors"
	}
	msgs := make([]string, len(ve))
	for i, e := range ve {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "; ")
}

// Validate walks v (struct or pointer to struct) and returns nil when every
// rule holds, otherwise a ValidationErrors describing all violations.
func Validate(v interface{}) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return ValidationErrors{{Path: "<root>", Rule: "required", Value: "nil"}}
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return ValidationErrors{{Path: "<root>", Rule: "struct", Value: rv.Kind().String()}}
	}
	var errs ValidationErrors
	validateStruct(rv, rv.Type().Name(), &errs)
	if len(errs) > 0 {
		return errs
	}
	return nil
}

func validateStruct(sv reflect.Value, path string, errs *ValidationErrors) {
	t := sv.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		fv := sv.Field(i)
		fieldPath := path + "." + f.Name
		tag := f.Tag.Get("validate")
		if tag != "" {
			applyRules(fv, fieldPath, tag, errs)
		}
		// Recurse into nested structs regardless of rules.
		switch {
		case fv.Kind() == reflect.Struct:
			validateStruct(fv, fieldPath, errs)
		case fv.Kind() == reflect.Ptr && fv.Type().Elem().Kind() == reflect.Struct && !fv.IsNil():
			validateStruct(fv.Elem(), fieldPath, errs)
		case fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() == reflect.Struct:
			for j := 0; j < fv.Len(); j++ {
				validateStruct(fv.Index(j), fmt.Sprintf("%s[%d]", fieldPath, j), errs)
			}
		}
	}
}

func applyRules(fv reflect.Value, path, tag string, errs *ValidationErrors) {
	for _, rule := range strings.Split(tag, ",") {
		rule = strings.TrimSpace(rule)
		switch {
		case rule == "required":
			if isZero(fv) {
				*errs = append(*errs, FieldError{path, "required", display(fv)})
			}
		case strings.HasPrefix(rule, "min="):
			if bound, err := strconv.ParseFloat(rule[4:], 64); err == nil {
				if val, ok := numeric(fv); ok && val < bound {
					*errs = append(*errs, FieldError{path, rule, display(fv)})
				}
			}
		case strings.HasPrefix(rule, "max="):
			if bound, err := strconv.ParseFloat(rule[4:], 64); err == nil {
				if val, ok := numeric(fv); ok && val > bound {
					*errs = append(*errs, FieldError{path, rule, display(fv)})
				}
			}
		case strings.HasPrefix(rule, "len="):
			applyLen(fv, path, rule[4:], errs)
		}
	}
}

func applyLen(fv reflect.Value, path, spec string, errs *ValidationErrors) {
	var lo, hi int
	hi = -1
	if parts := strings.SplitN(spec, "..", 2); len(parts) == 2 {
		lo, _ = strconv.Atoi(parts[0])
		hi, _ = strconv.Atoi(parts[1])
	} else {
		lo, _ = strconv.Atoi(spec)
		hi = lo
	}
	var n int
	switch fv.Kind() {
	case reflect.String, reflect.Slice, reflect.Map, reflect.Array:
		n = fv.Len()
	default:
		return
	}
	if n < lo || (hi >= 0 && n > hi) {
		*errs = append(*errs, FieldError{path, "len=" + spec, strconv.Itoa(n)})
	}
}

func isZero(fv reflect.Value) bool {
	switch fv.Kind() {
	case reflect.Ptr, reflect.Interface:
		return fv.IsNil()
	case reflect.Slice, reflect.Map:
		return fv.IsNil() || fv.Len() == 0
	case reflect.String, reflect.Array:
		return fv.Len() == 0
	case reflect.Bool:
		return !fv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fv.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fv.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return fv.Float() == 0
	}
	return false
}

func numeric(fv reflect.Value) (float64, bool) {
	switch fv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(fv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(fv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return fv.Float(), true
	}
	return 0, false
}

func display(fv reflect.Value) string {
	switch fv.Kind() {
	case reflect.String:
		return strconv.Quote(fv.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(fv.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(fv.Uint(), 10)
	case reflect.Bool:
		return strconv.FormatBool(fv.Bool())
	case reflect.Ptr, reflect.Slice, reflect.Map:
		if fv.IsNil() {
			return "nil"
		}
	}
	return fmt.Sprintf("%v", fv)
}
