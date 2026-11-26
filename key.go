package temporal

import (
	"reflect"
	"sort"
)

// Key is the multi-column equality tuple used by exclusion constraints
// (e.g. {"tenant_id": 7}) and by foreign-key matching. A nil or empty Key
// means a single global partition (no equality columns).
type Key map[string]any

// KeyOf builds a Key from alternating column/value arguments:
// KeyOf("tenant", 7, "acct", "a1").
func KeyOf(kv ...any) Key {
	k := Key{}
	for i := 0; i+1 < len(kv); i += 2 {
		name, _ := kv[i].(string)
		k[name] = kv[i+1]
	}
	return k
}

// Clone returns a shallow copy of the key map.
func (k Key) Clone() Key {
	if k == nil {
		return Key{}
	}
	out := make(Key, len(k))
	for name, v := range k {
		out[name] = v
	}
	return out
}

// Get returns the value for column name.
func (k Key) Get(name string) (any, bool) {
	if k == nil {
		return nil, false
	}
	v, ok := k[name]
	return v, ok
}

// tupleKey is an ordered key tuple. It is used when callers supply slice
// keys together with a table's ordered KeyColumns.
type tupleKey []any

// CoerceKey normalizes the many key shapes accepted by the executor API:
// Key, map[string]any, map of other value types, struct pointers/maps via
// reflection, ordered slices/arrays (paired with columns when available),
// and scalars (paired with the first/only key column).
func CoerceKey(k any, columns ...[]string) Key {
	cols := []string(nil)
	if len(columns) > 0 {
		cols = columns[0]
	}
	if k == nil {
		return Key{}
	}
	switch v := k.(type) {
	case Key:
		return v.Clone()
	case map[string]any:
		return Key(v).Clone()
	case *Key:
		if v == nil {
			return Key{}
		}
		return v.Clone()
	}

	rv := reflect.ValueOf(k)
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return Key{}
		}
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.Map:
		out := Key{}
		iter := rv.MapRange()
		for iter.Next() {
			out[reflectValueString(iter.Key())] = iter.Value().Interface()
		}
		return out
	case reflect.Slice, reflect.Array:
		out := Key{}
		n := rv.Len()
		for i := 0; i < n; i++ {
			name := "k" + itoa(i)
			if i < len(cols) {
				name = cols[i]
			}
			out[name] = rv.Index(i).Interface()
		}
		return out
	case reflect.Struct:
		return structToKey(rv)
	default:
		out := Key{}
		name := "key"
		if len(cols) > 0 {
			name = cols[0]
		}
		out[name] = k
		return out
	}
}

func reflectValueString(v reflect.Value) string {
	if v.Kind() == reflect.String {
		return v.String()
	}
	return formatAny(v.Interface())
}

func structToKey(rv reflect.Value) Key {
	t := rv.Type()
	out := Key{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name := f.Tag.Get("key")
		if name == "" {
			name = f.Name
		}
		out[name] = rv.Field(i).Interface()
	}
	return out
}

// KeysEqual compares two key tuples with numeric coercion so that int(1),
// int64(1) and float64(1) compare equal.
func KeysEqual(a, b any) bool {
	ka := CoerceKey(a)
	kb := CoerceKey(b)
	if len(ka) != len(kb) {
		return false
	}
	for name, av := range ka {
		bv, ok := kb[name]
		if !ok || !valuesEqual(av, bv) {
			return false
		}
	}
	return true
}

// KeyMatches reports whether rowKey contains equal values for every entry in
// selector. A nil/empty selector matches every row.
func KeyMatches(rowKey, selector any) bool {
	sel := CoerceKey(selector)
	if len(sel) == 0 {
		return true
	}
	row := CoerceKey(rowKey)
	for name, sv := range sel {
		rv, ok := row[name]
		if !ok || !valuesEqual(rv, sv) {
			return false
		}
	}
	return true
}

// valuesEqual compares scalar values with cross-kind numeric support.
func valuesEqual(a, b any) bool {
	if ae, ok := numericFloat(a); ok {
		if be, ok2 := numericFloat(b); ok2 {
			return ae == be
		}
	}
	return reflect.DeepEqual(a, b)
}

func numericFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

// SortedKeyNames returns key column names in sorted order for stable output.
func SortedKeyNames(k Key) []string {
	names := make([]string, 0, len(k))
	for n := range k {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
