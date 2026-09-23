// Package gsbdiff implements baseline-summary based document diffs.
//
// Supported serialization scope (the "document model"):
//   - null, bool, float64 numbers (NaN/Inf rejected), string
//   - arrays ([]any) and objects (map[string]any) built from the above
//   - the document root must be an object or an array
//
// Decoding never executes host code: the wire format carries only
// length-prefixed plain data, and every declared length is validated
// against the remaining input before any allocation happens.
package gsbdiff

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// ErrUnsupportedValue reports a value outside the supported document model.
var ErrUnsupportedValue = errors.New("gsbdiff: unsupported value")

const maxDepth = 256

// Normalize converts a value into the canonical document-model
// representation (all Go integer/float kinds become float64) and rejects
// anything outside the supported scope.
func Normalize(v any) (any, error) {
	return normalize(v, 0)
}

func normalize(v any, depth int) (any, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("%w: nesting too deep", ErrUnsupportedValue)
	}
	switch t := v.(type) {
	case nil, bool, string:
		return t, nil
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return nil, fmt.Errorf("%w: NaN/Inf number", ErrUnsupportedValue)
		}
		return t, nil
	case float32:
		return normalize(float64(t), depth)
	case int:
		return float64(t), nil
	case int8:
		return float64(t), nil
	case int16:
		return float64(t), nil
	case int32:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case uint:
		return float64(t), nil
	case uint8:
		return float64(t), nil
	case uint16:
		return float64(t), nil
	case uint32:
		return float64(t), nil
	case uint64:
		return float64(t), nil
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			nv, err := normalize(e, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = nv
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			nv, err := normalize(e, depth+1)
			if err != nil {
				return nil, err
			}
			out[k] = nv
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedValue, v)
	}
}

// canonical renders a normalized value into deterministic bytes
// (encoding/json sorts object keys).
func canonical(v any) ([]byte, error) {
	return json.Marshal(v)
}

func isDocument(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return true
	}
	return false
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[k] = deepCopy(e)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, e := range t {
			s[i] = deepCopy(e)
		}
		return s
	default:
		return v
	}
}

func valuesEqual(a, b any) bool {
	switch av := a.(type) {
	case nil:
		return b == nil
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !valuesEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, e := range av {
			be, ok := bv[k]
			if !ok || !valuesEqual(e, be) {
				return false
			}
		}
		return true
	}
	return false
}
