// Package validate is a reflection-driven validation engine. It walks
// structs (including nested structs and slices of structs) and checks
// fields against rules declared in the `validate` struct tag:
//
//	type Node struct {
//		ID       string `validate:"required"`
//		Weight   int    `validate:"min=1,max=64"`
//		Region   string `validate:"required,min=2,max=32"`
//	}
//
// Supported rules: required, min=N, max=N, len=N. For numbers min/max
// compare the value; for strings, slices and maps they compare length.
// All violations are collected and returned as a Violations error.
package validate

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Violation describes a single failed rule on one field.
type Violation struct {
	Path   string // dotted field path, e.g. "Cluster.Nodes[2].ID"
	Rule   string // e.g. "required", "min=1"
	Detail string
}

func (v Violation) Error() string {
	return fmt.Sprintf("%s: rule %q violated (%s)", v.Path, v.Rule, v.Detail)
}

// Violations is the aggregate error returned by Validate.
type Violations []Violation

func (vs Violations) Error() string {
	if len(vs) == 1 {
		return vs[0].Error()
	}
	return fmt.Sprintf("%d validation violations, first: %s", len(vs), vs[0].Error())
}

// Validate checks s against its `validate` tags. It returns nil when
// valid, or a Violations value describing every failure found.
func Validate(s interface{}) error {
	var vs Violations
	walk(reflect.ValueOf(s), "", &vs)
	if len(vs) > 0 {
		return vs
	}
	return nil
}

func walk(v reflect.Value, path string, out *Violations) {
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" { // unexported
				continue
			}
			name := f.Name
			if path != "" {
				name = path + "." + name
			}
			fv := v.Field(i)
			if tag := f.Tag.Get("validate"); tag != "" && tag != "-" {
				checkRules(fv, name, tag, out)
			}
			walk(fv, name, out)
		}
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return // []byte is a leaf
		}
		for i := 0; i < v.Len(); i++ {
			walk(v.Index(i), fmt.Sprintf("%s[%d]", path, i), out)
		}
	}
}

func checkRules(v reflect.Value, path, tag string, out *Violations) {
	for _, rule := range strings.Split(tag, ",") {
		rule = strings.TrimSpace(rule)
		switch {
		case rule == "required":
			if isZero(v) {
				*out = append(*out, Violation{path, rule, "zero value not allowed"})
			}
		case strings.HasPrefix(rule, "min="):
			if n, err := strconv.ParseFloat(rule[4:], 64); err == nil {
				if got, ok := numericOrLen(v); ok && got < n {
					*out = append(*out, Violation{path, rule, fmt.Sprintf("got %v", got)})
				}
			}
		case strings.HasPrefix(rule, "max="):
			if n, err := strconv.ParseFloat(rule[4:], 64); err == nil {
				if got, ok := numericOrLen(v); ok && got > n {
					*out = append(*out, Violation{path, rule, fmt.Sprintf("got %v", got)})
				}
			}
		case strings.HasPrefix(rule, "len="):
			if n, err := strconv.Atoi(rule[4:]); err == nil {
				if got, ok := lengthOf(v); ok && got != n {
					*out = append(*out, Violation{path, rule, fmt.Sprintf("got length %d", got)})
				}
			}
		}
	}
}

func isZero(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map:
		return v.IsNil()
	}
	return reflect.DeepEqual(v.Interface(), reflect.Zero(v.Type()).Interface())
}

func numericOrLen(v reflect.Value) (float64, bool) {
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return 0, false
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(v.Uint()), true
	case reflect.Float32, reflect.Float64:
		return v.Float(), true
	}
	if n, ok := lengthOf(v); ok {
		return float64(n), true
	}
	return 0, false
}

func lengthOf(v reflect.Value) (int, bool) {
	switch v.Kind() {
	case reflect.String, reflect.Slice, reflect.Array, reflect.Map:
		return v.Len(), true
	}
	return 0, false
}
