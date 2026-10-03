// Reflection-mode validation engine. Fields carry an optional tag:
//
//	validate:"required,min=1,max=100,minlen=1,maxlen=64"
//
// Validate walks structs (including nested messages and slices) and collects
// every violation with its dotted field path.
package wire

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// ValidationError describes one violated constraint.
type ValidationError struct {
	Path  string
	Rule  string
	Value any
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("validate: %s: violates %q (value=%v)", e.Path, e.Rule, e.Value)
}

// ValidationErrors is the aggregate returned by Validate.
type ValidationErrors []ValidationError

func (es ValidationErrors) Error() string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}

// Validate checks v (struct or pointer to struct) against its `validate` tags.
// It returns nil when every constraint holds, else a ValidationErrors listing
// all violations.
func Validate(v any) error {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return ValidationErrors{{Path: "<root>", Rule: "required", Value: nil}}
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("validate: expected struct, got %s", rv.Type())
	}
	var errs ValidationErrors
	walkValidate(rv, rv.Type().Name(), &errs)
	if len(errs) > 0 {
		return errs
	}
	return nil
}

func walkValidate(sv reflect.Value, path string, errs *ValidationErrors) {
	t := sv.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		fv := sv.Field(i)
		fp := path + "." + f.Name

		// Recurse into nested messages first so violations are ordered depth-first.
		switch {
		case fv.Kind() == reflect.Struct:
			walkValidate(fv, fp, errs)
		case fv.Kind() == reflect.Pointer && fv.Type().Elem().Kind() == reflect.Struct:
			if !fv.IsNil() {
				walkValidate(fv.Elem(), fp, errs)
			}
		case fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() == reflect.Struct:
			for j := 0; j < fv.Len(); j++ {
				walkValidate(fv.Index(j), fmt.Sprintf("%s[%d]", fp, j), errs)
			}
		}

		tag, ok := f.Tag.Lookup("validate")
		if !ok || tag == "" {
			continue
		}
		for _, rule := range strings.Split(tag, ",") {
			checkRule(fv, fp, rule, errs)
		}
	}
}

func checkRule(fv reflect.Value, path, rule string, errs *ValidationErrors) {
	fail := func() {
		*errs = append(*errs, ValidationError{Path: path, Rule: rule, Value: fv.Interface()})
	}
	switch {
	case rule == "required":
		if fv.IsZero() {
			fail()
		}
	case strings.HasPrefix(rule, "min="):
		if n, err := strconv.ParseFloat(strings.TrimPrefix(rule, "min="), 64); err == nil && numVal(fv) < n {
			fail()
		}
	case strings.HasPrefix(rule, "max="):
		if n, err := strconv.ParseFloat(strings.TrimPrefix(rule, "max="), 64); err == nil && numVal(fv) > n {
			fail()
		}
	case strings.HasPrefix(rule, "minlen="):
		if n, err := strconv.Atoi(strings.TrimPrefix(rule, "minlen=")); err == nil && lengthOf(fv) < n {
			fail()
		}
	case strings.HasPrefix(rule, "maxlen="):
		if n, err := strconv.Atoi(strings.TrimPrefix(rule, "maxlen=")); err == nil && lengthOf(fv) > n {
			fail()
		}
	}
}

func numVal(v reflect.Value) float64 {
	switch v.Kind() {
	case reflect.Int, reflect.Int32, reflect.Int64:
		return float64(v.Int())
	case reflect.Uint, reflect.Uint32, reflect.Uint64:
		return float64(v.Uint())
	case reflect.Float32, reflect.Float64:
		return v.Float()
	}
	return 0
}

func lengthOf(v reflect.Value) int {
	switch v.Kind() {
	case reflect.String, reflect.Slice, reflect.Map, reflect.Array:
		return v.Len()
	}
	return 0
}
