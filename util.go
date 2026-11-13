package netsec

import (
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

func fmtError(message string) error {
	return errors.New(message)
}

func equalFold(a, b string) bool {
	return strings.EqualFold(a, b)
}

func cloneBytes(value []byte) []byte {
	return append([]byte(nil), value...)
}

// sortViolations gives callers a stable ordering regardless of map
// iteration inside the Secret fetcher.
func sortViolations(violations []Violation) {
	sort.SliceStable(violations, func(i, j int) bool {
		left, right := violations[i], violations[j]
		comparison := compareStrings(
			left.Kind, right.Kind,
			left.Position, right.Position,
			left.Function, right.Function,
			left.Namespace, right.Namespace,
			left.ResourceName, right.ResourceName,
			left.FieldPath, right.FieldPath,
			left.SecretNamespace, right.SecretNamespace,
			left.SecretName, right.SecretName,
			left.SecretKey, right.SecretKey,
			left.Encoding, right.Encoding,
		)
		return comparison < 0
	})
}

func compareStrings(pairs ...string) int {
	for i := 0; i < len(pairs); i += 2 {
		if pairs[i] != pairs[i+1] {
			if pairs[i] < pairs[i+1] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Flatten walks a rendered object represented as nested map[string]any,
// []any and scalar values and returns one FieldValue per string field.
// Maps and slices produced by encoding/json (e.g. from unstructured.Unstructured)
// are supported directly. Label and annotation values are included because
// they are ordinary map entries under metadata.
func Flatten(value any) []FieldValue {
	var fields []FieldValue
	walkValue(reflect.ValueOf(value), "", &fields)
	return fields
}

func walkValue(value reflect.Value, path string, fields *[]FieldValue) {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return
	}

	switch value.Kind() {
	case reflect.Map:
		keys := make([]string, 0, value.Len())
		keyByName := make(map[string]reflect.Value, value.Len())
		iter := value.MapRange()
		for iter.Next() {
			key := keyToString(iter.Key())
			keys = append(keys, key)
			keyByName[key] = iter.Value()
		}
		sort.Strings(keys)
		for _, key := range keys {
			walkValue(keyByName[key], joinPath(path, key), fields)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			walkValue(value.Index(i), joinIndex(path, i), fields)
		}
	case reflect.String:
		if path != "" {
			*fields = append(*fields, FieldValue{Path: path, Value: value.String()})
		}
	}
}

func keyToString(key reflect.Value) string {
	for key.Kind() == reflect.Interface {
		key = key.Elem()
	}
	if key.Kind() == reflect.String {
		return key.String()
	}
	return ""
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

func joinIndex(parent string, index int) string {
	return parent + "[" + strconv.Itoa(index) + "]"
}
