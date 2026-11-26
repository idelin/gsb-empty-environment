package temporal

import "reflect"

// Row is one temporal relation row.
//
// Range holds the validity interval. Key holds the equality-key column
// values used by exclusion constraints. Fields holds every column (including
// key columns and the payload columns); when a portion is split the unaffected
// fields are copied to remnant rows. Payload is an optional convenience
// carrier for the non-key data.
type Row struct {
	// ID is an optional stable row identifier used in violation reports.
	ID string

	// Table optionally names the relation the row belongs to.
	Table string

	// Key is the equality-key tuple. When nil, Key is derived from Fields
	// using the table's KeyColumns during validation.
	Key Key

	// Range is the validity interval (valid_during).
	Range Range

	// Payload is the arbitrary non-key value replaced on updated middles.
	Payload any

	// Fields contains all column values. It is deep-copied on every split so
	// remnant rows preserve unaffected fields without aliasing maps.
	Fields map[string]any
}

// Clone returns a deep-enough copy for copy-on-write splitting: the Fields
// map is duplicated and map/slice payloads are recursively copied. Scalar and
// struct payload values are copied by value.
func (r Row) Clone() Row {
	c := r
	if r.Key != nil {
		c.Key = r.Key.Clone()
	}
	if r.Fields != nil {
		c.Fields = make(map[string]any, len(r.Fields))
		for k, v := range r.Fields {
			c.Fields[k] = deepCopyValue(v)
		}
	}
	c.Payload = deepCopyValue(r.Payload)
	return c
}

func deepCopyValue(v any) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map:
		if rv.IsNil() {
			return v
		}
		out := reflect.MakeMap(rv.Type())
		iter := rv.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), reflect.ValueOf(deepCopyValue(iter.Value().Interface())))
		}
		return out.Interface()
	case reflect.Slice:
		if rv.IsNil() {
			return v
		}
		out := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out.Index(i).Set(reflect.ValueOf(deepCopyValue(rv.Index(i).Interface())))
		}
		return out.Interface()
	case reflect.Ptr:
		if rv.IsNil() {
			return v
		}
		cp := reflect.New(rv.Elem().Type())
		cp.Elem().Set(reflect.ValueOf(deepCopyValue(rv.Elem().Interface())))
		return cp.Interface()
	default:
		return v
	}
}

// EffectiveKey returns the row's explicit Key when set, otherwise extracts
// the table key columns from Fields.
func (r Row) EffectiveKey(keyColumns []string) Key {
	if len(r.Key) > 0 {
		return r.Key
	}
	k := Key{}
	for _, col := range keyColumns {
		if v, ok := r.Fields[col]; ok {
			k[col] = v
		}
	}
	return k
}

// applyPayload returns a copy of r representing an updated middle segment.
// If newPayload is a map it is merged into Fields (so unmentioned fields are
// preserved); otherwise Payload is replaced. When Fields is non-nil and the
// payload is a map, Payload is set to the merged Fields as a convenience.
func (r Row) applyPayload(newPayload any) Row {
	u := r.Clone()
	if patch, ok := asFieldMap(newPayload); ok {
		if u.Fields == nil {
			u.Fields = map[string]any{}
		}
		for k, v := range patch {
			u.Fields[k] = v
		}
		// Keep explicit Key in sync if key columns were patched.
		if u.Key != nil {
			for k, v := range patch {
				if _, isKey := u.Key[k]; isKey {
					u.Key[k] = v
				}
			}
		}
		u.Payload = newPayload
		return u
	}
	u.Payload = newPayload
	return u
}

func asFieldMap(p any) (map[string]any, bool) {
	switch m := p.(type) {
	case map[string]any:
		return m, true
	case Fields:
		return map[string]any(m), true
	case Key:
		return map[string]any(m), true
	}
	rv := reflect.ValueOf(p)
	if p == nil {
		return nil, false
	}
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil, false
		}
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String {
		out := map[string]any{}
		iter := rv.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = iter.Value().Interface()
		}
		return out, true
	}
	return nil, false
}

// Fields is a convenience alias for a column map.
type Fields map[string]any

// rowsCoalescible reports whether two rows may be coalesced: equal effective
// keys and equal value signature. The value signature is Payload when either
// row carries a non-nil Payload; otherwise the Fields maps are compared.
// Range is intentionally excluded.
func rowsCoalescible(a, b Row, keyColumns []string) bool {
	ka := a.EffectiveKey(keyColumns)
	kb := b.EffectiveKey(keyColumns)
	if !KeysEqual(ka, kb) {
		return false
	}
	if a.Payload != nil || b.Payload != nil {
		return reflect.DeepEqual(a.Payload, b.Payload)
	}
	return reflect.DeepEqual(a.Fields, b.Fields)
}

// MergeAdjacentRows coalesces rows that share key, payload and fields and
// whose ranges overlap or are adjacent. Empty rows are dropped. The result is
// sorted by key then lower boundary. This implements the "adjacent merge"
// expected after a portion update spans several old records carrying the same
// new payload.
func MergeAdjacentRows(rows []Row, keyColumns ...[]string) []Row {
	cols := []string(nil)
	if len(keyColumns) > 0 {
		cols = keyColumns[0]
	}

	items := make([]Row, 0, len(rows))
	for _, row := range rows {
		c := row.Clone()
		c.Range = c.Range.Normalize()
		if c.Range.IsEmpty() {
			continue
		}
		items = append(items, c)
	}
	if len(items) == 0 {
		return nil
	}

	sortRowsByKey(items, cols)

	merged := make([]Row, 0, len(items))
	for _, cur := range items {
		if len(merged) == 0 {
			merged = append(merged, cur)
			continue
		}
		last := &merged[len(merged)-1]
		if rowsCoalescible(*last, cur, cols) &&
			(Overlaps(last.Range, cur.Range) || Adjacent(last.Range, cur.Range)) {
			if u := Union(last.Range, cur.Range); len(u) == 1 {
				last.Range = u[0]
				continue
			}
		}
		merged = append(merged, cur)
	}
	return merged
}
