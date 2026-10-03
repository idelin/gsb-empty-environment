package wire

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// ValidationError describes one violated constraint.
type ValidationError struct {
	Field  string
	Rule   string
	Detail string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validate: field %s violates %s: %s", e.Field, e.Rule, e.Detail)
}

// ValidationErrors aggregates every violated constraint found in a struct.
type ValidationErrors []*ValidationError

func (vs ValidationErrors) Error() string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = v.Error()
	}
	return strings.Join(parts, "; ")
}

// Validate walks v (struct or pointer to struct) via reflection and checks
// the constraints declared in `validate` tags:
//
//	Name  string `validate:"required,minlen=1,maxlen=64"`
//	Age   int32  `validate:"min=0,max=150"`
//	Items []int  `validate:"required,minlen=1"`
//
// Rules: required (non-zero value), min/max (numeric bounds), minlen/maxlen
// (string, slice or map length). Nested structs and struct slices are
// validated recursively. It returns nil when valid, or a ValidationErrors
// listing every violation.
func Validate(v interface{}) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return &ValidationError{Field: "<root>", Rule: "required", Detail: "nil pointer"}
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("validate: requires struct or pointer to struct, got %T", v)
	}
	var errs ValidationErrors
	validateStruct(rv, rv.Type().Name(), &errs)
	if len(errs) > 0 {
		return errs
	}
	return nil
}

func validateStruct(sv reflect.Value, prefix string, errs *ValidationErrors) {
	st := sv.Type()
	for i := 0; i < st.NumField(); i++ {
		sf := st.Field(i)
		fv := sv.Field(i)
		path := prefix + "." + sf.Name

		// Recurse into nested structs and struct slices first.
		switch {
		case fv.Kind() == reflect.Struct:
			validateStruct(fv, path, errs)
		case fv.Kind() == reflect.Ptr && fv.Type().Elem().Kind() == reflect.Struct:
			if !fv.IsNil() {
				validateStruct(fv.Elem(), path, errs)
			}
		case fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() == reflect.Struct:
			for j := 0; j < fv.Len(); j++ {
				validateStruct(fv.Index(j), fmt.Sprintf("%s[%d]", path, j), errs)
			}
		}

		tag := sf.Tag.Get("validate")
		if tag == "" || tag == "-" {
			continue
		}
		for _, rule := range strings.Split(tag, ",") {
			applyRule(path, fv, rule, errs)
		}
	}
}

func applyRule(path string, fv reflect.Value, rule string, errs *ValidationErrors) {
	kv := strings.SplitN(rule, "=", 2)
	name := kv[0]
	var arg string
	if len(kv) == 2 {
		arg = kv[1]
	}
	fail := func(detail string) {
		*errs = append(*errs, &ValidationError{Field: path, Rule: name, Detail: detail})
	}

	switch name {
	case "required":
		if isZeroValue(fv) {
			fail("zero value not allowed")
		}
	case "min", "max":
		limit, err := strconv.ParseFloat(arg, 64)
		if err != nil {
			fail("bad limit " + arg)
			return
		}
		val, ok := numericValue(fv)
		if !ok {
			fail("not a numeric field")
			return
		}
		if name == "min" && val < limit {
			fail(fmt.Sprintf("%v < %v", val, limit))
		}
		if name == "max" && val > limit {
			fail(fmt.Sprintf("%v > %v", val, limit))
		}
	case "minlen", "maxlen":
		limit, err := strconv.Atoi(arg)
		if err != nil {
			fail("bad limit " + arg)
			return
		}
		var ln int
		switch fv.Kind() {
		case reflect.String, reflect.Slice, reflect.Map, reflect.Array:
			ln = fv.Len()
		default:
			fail("no length for " + fv.Kind().String())
			return
		}
		if name == "minlen" && ln < limit {
			fail(fmt.Sprintf("len %d < %d", ln, limit))
		}
		if name == "maxlen" && ln > limit {
			fail(fmt.Sprintf("len %d > %d", ln, limit))
		}
	default:
		fail("unknown rule")
	}
}

func isZeroValue(fv reflect.Value) bool {
	switch fv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map:
		return fv.IsNil()
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
	case reflect.Struct:
		zero := reflect.Zero(fv.Type())
		return reflect.DeepEqual(fv.Interface(), zero.Interface())
	}
	return false
}

func numericValue(fv reflect.Value) (float64, bool) {
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
