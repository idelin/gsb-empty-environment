package netsec

import (
	"sort"
	"strings"
)

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneValue(value)
	}
	return out
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneValue(item)
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	case []map[string]any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneMap(item)
		}
		return out
	default:
		return value
	}
}

func asMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	default:
		return nil, false
	}
}

func asAnySlice(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []string:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = item
		}
		return out, true
	case []map[string]any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = item
		}
		return out, true
	default:
		return nil, false
	}
}

func toStrings(value any) ([]string, bool) {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil, true
		}
		return []string{typed}, true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			if strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out, true
	case []string:
		return append([]string(nil), typed...), true
	default:
		return nil, false
	}
}

func joinField(prefix, key string) string {
	if prefix == "" {
		return key
	}
	if key == "" {
		return prefix
	}
	return prefix + "." + key
}

func sortedMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func mergeAggPlans(dest, src map[string]*AggPlan) {
	for name, plan := range src {
		dest[name] = plan
	}
}
