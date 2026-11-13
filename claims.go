package netsec

import (
	"context"
	"encoding/json"
	"reflect"
)

// Claims is an immutable snapshot of verified claims. NewClaims deep-copies
// the supplied map and all JSON-like containers reachable from it. Lookup
// returns defensive copies rather than references to the snapshot.
type Claims struct {
	values map[string]any
}

// NewClaims returns an immutable claims snapshot. The input map can be reused
// or mutated by the caller without changing the returned Claims.
func NewClaims(values map[string]any) Claims {
	if values == nil {
		return Claims{}
	}
	return Claims{values: cloneStringMap(values)}
}

// NewClaimsFromJSON decodes a JSON object into an immutable claims snapshot.
func NewClaimsFromJSON(data []byte) (Claims, error) {
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return Claims{}, err
	}
	return NewClaims(values), nil
}

// Lookup returns a defensive copy of a top-level claim value.
func (c Claims) Lookup(name string) (any, bool) {
	value, ok := c.lookup(name)
	if !ok {
		return nil, false
	}
	return cloneValue(value), true
}

// String returns a top-level string claim.
func (c Claims) String(name string) (string, bool) {
	value, ok := c.lookup(name)
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

// Strings returns a string or string slice claim. JSON arrays are accepted
// when every element is a string.
func (c Claims) Strings(name string) ([]string, bool) {
	value, ok := c.lookup(name)
	if !ok {
		return nil, false
	}
	return claimStrings(value)
}

func (c Claims) lookup(name string) (any, bool) {
	if c.values == nil {
		return nil, false
	}
	value, ok := c.values[name]
	return value, ok
}

func claimStrings(value any) ([]string, bool) {
	switch typed := value.(type) {
	case string:
		return []string{typed}, true
	case []string:
		return append([]string(nil), typed...), true
	case []any:
		result := make([]string, len(typed))
		for i, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			result[i] = text
		}
		return result, true
	default:
		return nil, false
	}
}

func cloneStringMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = cloneValue(value)
	}
	return result
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case nil, string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return typed
	case []byte:
		return append([]byte(nil), typed...)
	case map[string]any:
		return cloneStringMap(typed)
	case map[string]string:
		copied := make(map[string]string, len(typed))
		for key, item := range typed {
			copied[key] = item
		}
		return copied
	case []string:
		return append([]string(nil), typed...)
	case []any:
		copied := make([]any, len(typed))
		for i, item := range typed {
			copied[i] = cloneValue(item)
		}
		return copied
	default:
		return cloneReflectValue(reflect.ValueOf(value)).Interface()
	}
}

func cloneReflectValue(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copied := cloneReflectValue(value.Elem())
		result := reflect.New(value.Type()).Elem()
		result.Set(copied)
		return result
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copied := cloneReflectValue(value.Elem())
		result := reflect.New(value.Type().Elem())
		result.Elem().Set(copied)
		return result
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			result.SetMapIndex(cloneReflectValue(iterator.Key()), cloneReflectValue(iterator.Value()))
		}
		return result
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			result.Index(i).Set(cloneReflectValue(value.Index(i)))
		}
		return result
	case reflect.Array:
		result := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			result.Index(i).Set(cloneReflectValue(value.Index(i)))
		}
		return result
	default:
		return value
	}
}

// TokenVerifier is supplied by the host. It must complete signature and time
// validation before returning an immutable claims snapshot. MapToken does not
// implement or replace those checks.
type TokenVerifier func(ctx context.Context, rawToken string) (Claims, error)

// VerifyFunc is an alias kept for callers that prefer verifier terminology.
type VerifyFunc = TokenVerifier
