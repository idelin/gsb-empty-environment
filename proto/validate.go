package proto

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Violation describes a single failed validation rule.
type Violation struct {
	Path string // dotted field path, e.g. "Child.Name"
	Rule string // e.g. "required", "min=1"
}

// ValidationError aggregates every violation found in a struct tree.
type ValidationError struct {
	Violations []Violation
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Violations))
	for i, v := range e.Violations {
		parts[i] = v.Path + "[" + v.Rule + "]"
	}
	return "proto: validation failed: " + strings.Join(parts, ", ")
}

// Validate walks v via reflection and checks every `check:"..."` tag.
// Supported rules: required, min=N, max=N, minlen=N, maxlen=N.
// Nested structs, pointers and slices are traversed recursively.
// Returns nil when the value is valid.
func Validate(v interface{}) error {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return &ValidationError{[]Violation{{Path: "", Rule: "required"}}}
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("proto: Validate expects a struct, got %s", rv.Kind())
	}
	var vs []Violation
	validateStruct(rv, rv.Type().Name(), &vs)
	if len(vs) > 0 {
		return &ValidationError{vs}
	}
	return nil
}

func validateStruct(rv reflect.Value, path string, vs *[]Violation) {
	si := getStructInfo(rv.Type())
	for _, fi := range si.fields {
		validateField(fi, rv.Field(fi.index), path+"."+fi.name, vs)
	}
}

func validateField(fi *fieldInfo, fv reflect.Value, path string, vs *[]Violation) {
	rules := parseCheck(fi.check)

	if _, ok := rules["required"]; ok && isZeroValue(fv) {
		*vs = append(*vs, Violation{Path: path, Rule: "required"})
		return
	}
	if isNilPtr(fv) {
		return // absent optional field: nothing more to check
	}

	for name, arg := range rules {
		switch name {
		case "min", "max":
			limit, _ := strconv.ParseFloat(arg, 64)
			if n, ok := numericValue(fv); ok {
				if name == "min" && n < limit {
					*vs = append(*vs, Violation{path, "min=" + arg})
				}
				if name == "max" && n > limit {
					*vs = append(*vs, Violation{path, "max=" + arg})
				}
			}
		case "minlen", "maxlen":
			limit, _ := strconv.Atoi(arg)
			if l, ok := lengthOf(fv); ok {
				if name == "minlen" && l < limit {
					*vs = append(*vs, Violation{path, "minlen=" + arg})
				}
				if name == "maxlen" && l > limit {
					*vs = append(*vs, Violation{path, "maxlen=" + arg})
				}
			}
		}
	}

	// recurse into nested messages
	t := fi.typ
	if isMessageType(t) {
		sub := fv
		if sub.Kind() == reflect.Ptr {
			sub = sub.Elem()
		}
		validateStruct(sub, path, vs)
		return
	}
	if t.Kind() == reflect.Slice && t != byteSliceType && isMessageType(t.Elem()) {
		for i := 0; i < fv.Len(); i++ {
			elem := fv.Index(i)
			if elem.Kind() == reflect.Ptr {
				if elem.IsNil() {
					continue
				}
				elem = elem.Elem()
			}
			validateStruct(elem, fmt.Sprintf("%s[%d]", path, i), vs)
		}
	}
}

func parseCheck(tag string) map[string]string {
	rules := make(map[string]string)
	for _, part := range strings.Split(tag, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if kv := strings.SplitN(part, "=", 2); len(kv) == 2 {
			rules[kv[0]] = kv[1]
		} else {
			rules[part] = ""
		}
	}
	return rules
}

func isNilPtr(fv reflect.Value) bool {
	return fv.Kind() == reflect.Ptr && fv.IsNil()
}

func isZeroValue(fv reflect.Value) bool {
	switch fv.Kind() {
	case reflect.Ptr:
		return fv.IsNil()
	case reflect.Slice, reflect.Map:
		return fv.Len() == 0
	case reflect.String:
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
		z := reflect.Zero(fv.Type())
		return reflect.DeepEqual(fv.Interface(), z.Interface())
	}
	return false
}

func numericValue(fv reflect.Value) (float64, bool) {
	for fv.Kind() == reflect.Ptr {
		if fv.IsNil() {
			return 0, false
		}
		fv = fv.Elem()
	}
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

func lengthOf(fv reflect.Value) (int, bool) {
	for fv.Kind() == reflect.Ptr {
		if fv.IsNil() {
			return 0, false
		}
		fv = fv.Elem()
	}
	switch fv.Kind() {
	case reflect.String, reflect.Slice, reflect.Map, reflect.Array:
		return fv.Len(), true
	}
	return 0, false
}
