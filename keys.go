package gin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
)

// ArrayKeys extracts scalar keys from a Go array or slice. Nested arrays are
// flattened. Strings are used verbatim; other scalars use their canonical JSON
// representation. Unsupported values are ignored.
func ArrayKeys(value any) []string {
	keys, _ := arrayKeys(value)
	return keys
}

// ExtractArrayKeys is an alias for ArrayKeys.
func ExtractArrayKeys(value any) []string {
	return ArrayKeys(value)
}

func arrayKeys(value any) ([]string, error) {
	set := make(map[string]struct{})
	if err := collectArrayKeys(reflect.ValueOf(value), set); err != nil {
		return nil, err
	}
	return sortedMapKeys(set), nil
}

func collectArrayKeys(value reflect.Value, keys map[string]struct{}) error {
	if !value.IsValid() {
		keys["null"] = struct{}{}
		return nil
	}
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			keys["null"] = struct{}{}
			return nil
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice && value.IsNil() {
			keys["null"] = struct{}{}
			return nil
		}
		for i := 0; i < value.Len(); i++ {
			if err := collectArrayKeys(value.Index(i), keys); err != nil {
				return err
			}
		}
		return nil
	case reflect.String:
		keys[value.String()] = struct{}{}
		return nil
	case reflect.Bool:
		keys[strconv.FormatBool(value.Bool())] = struct{}{}
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		keys[strconv.FormatInt(value.Int(), 10)] = struct{}{}
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		keys[strconv.FormatUint(value.Uint(), 10)] = struct{}{}
		return nil
	case reflect.Float32, reflect.Float64:
		number := value.Float()
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("unsupported floating-point value %v", number)
		}
		keys[strconv.FormatFloat(number, 'g', -1, 64)] = struct{}{}
		return nil
	case reflect.Map, reflect.Struct:
		encoded, err := json.Marshal(value.Interface())
		if err != nil {
			return err
		}
		var decoded any
		if err := decodeJSON(encoded, &decoded); err != nil {
			return err
		}
		return walkJSON("", decoded, keys)
	default:
		return fmt.Errorf("unsupported array element type %s", value.Kind())
	}
}

// JSONBKeys extracts GIN keys from a JSONB document. JSON is decoded with
// json.Number so numeric tokens preserve their source representation.
//
// The generated key vocabulary is intentionally useful for both containment
// and path queries:
//
//   - an object member contributes its path, for example "user.name";
//   - a scalar contributes its canonical token, for example "alice", plus the
//     path-qualified token "user.name=alice";
//   - an array contributes its path with "[]" appended, plus one qualified
//     token per element, for example "tags[]=go".
func JSONBKeys(document []byte) ([]string, error) {
	var decoded any
	if err := decodeJSON(document, &decoded); err != nil {
		return nil, err
	}
	set := make(map[string]struct{})
	if err := walkJSON("", decoded, set); err != nil {
		return nil, err
	}
	return sortedMapKeys(set), nil
}

// ExtractJSONBKeys accepts JSON bytes, a JSON string, or any Go value that can
// be marshaled to JSON.
func ExtractJSONBKeys(value any) ([]string, error) {
	switch document := value.(type) {
	case []byte:
		return JSONBKeys(document)
	case json.RawMessage:
		return JSONBKeys(document)
	case string:
		return JSONBKeys([]byte(document))
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return JSONBKeys(encoded)
	}
}

// InsertArray indexes an array value for tid.
func (idx *Index) InsertArray(tid TID, value any) error {
	keys, err := arrayKeys(value)
	if err != nil {
		return err
	}
	return idx.InsertKeys(tid, keys)
}

// InsertJSONB indexes a JSONB document for tid. The document may be JSON bytes,
// a JSON string, or a marshalable Go value.
func (idx *Index) InsertJSONB(tid TID, document any) error {
	keys, err := ExtractJSONBKeys(document)
	if err != nil {
		return err
	}
	return idx.InsertKeys(tid, keys)
}

// InsertJSON is an alias for InsertJSONB.
func (idx *Index) InsertJSON(tid TID, document any) error {
	return idx.InsertJSONB(tid, document)
}

// InsertAny extracts keys from arrays or JSON-like values and inserts them.
func (idx *Index) InsertAny(tid TID, values ...any) error {
	keys := make(map[string]struct{})
	for _, value := range values {
		extracted := ArrayKeys(value)
		for _, key := range extracted {
			keys[key] = struct{}{}
		}
	}
	return idx.InsertKeys(tid, sortedMapKeys(keys))
}

func decodeJSON(document []byte, target *any) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode JSONB: %w", err)
	}
	var trailing any
	err := decoder.Decode(&trailing)
	if err == nil {
		return fmt.Errorf("decode JSONB: trailing JSON value")
	}
	if err != io.EOF {
		return fmt.Errorf("decode JSONB: %w", err)
	}
	return nil
}

func walkJSON(path string, value any, keys map[string]struct{}) error {
	switch current := value.(type) {
	case map[string]any:
		members := make([]string, 0, len(current))
		for member := range current {
			members = append(members, member)
		}
		sort.Strings(members)
		for _, member := range members {
			memberPath := joinJSONPath(path, member)
			keys[memberPath] = struct{}{}
			if err := walkJSON(memberPath, current[member], keys); err != nil {
				return err
			}
		}
		return nil
	case []any:
		arrayPath := path + "[]"
		if path == "" {
			arrayPath = "[]"
		}
		keys[arrayPath] = struct{}{}
		for _, element := range current {
			if token, ok := canonicalJSONScalar(element); ok {
				keys[arrayPath+"="+token] = struct{}{}
			}
			if err := walkJSON(arrayPath, element, keys); err != nil {
				return err
			}
		}
		return nil
	default:
		token, ok := canonicalJSONScalar(current)
		if !ok {
			return fmt.Errorf("unsupported JSON value type %T", value)
		}
		keys[token] = struct{}{}
		if path != "" {
			keys[path+"="+token] = struct{}{}
		}
		return nil
	}
}

func joinJSONPath(path, member string) string {
	if path == "" {
		return member
	}
	return path + "." + member
}

func canonicalJSONScalar(value any) (string, bool) {
	switch scalar := value.(type) {
	case nil:
		return "null", true
	case string:
		return scalar, true
	case bool:
		return strconv.FormatBool(scalar), true
	case json.Number:
		return scalar.String(), true
	case float64:
		if math.IsNaN(scalar) || math.IsInf(scalar, 0) {
			return "", false
		}
		return strconv.FormatFloat(scalar, 'g', -1, 64), true
	case float32:
		number := float64(scalar)
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return "", false
		}
		return strconv.FormatFloat(number, 'g', -1, 32), true
	case int:
		return strconv.FormatInt(int64(scalar), 10), true
	case int8:
		return strconv.FormatInt(int64(scalar), 10), true
	case int16:
		return strconv.FormatInt(int64(scalar), 10), true
	case int32:
		return strconv.FormatInt(int64(scalar), 10), true
	case int64:
		return strconv.FormatInt(scalar, 10), true
	case uint:
		return strconv.FormatUint(uint64(scalar), 10), true
	case uint8:
		return strconv.FormatUint(uint64(scalar), 10), true
	case uint16:
		return strconv.FormatUint(uint64(scalar), 10), true
	case uint32:
		return strconv.FormatUint(uint64(scalar), 10), true
	case uint64:
		return strconv.FormatUint(scalar, 10), true
	default:
		return "", false
	}
}
