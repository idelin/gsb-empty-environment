package snapshotpage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// Direction is the sort direction of a single field.
type Direction int

const (
	Asc Direction = iota
	Desc
)

// NullOrder controls where NULL (missing) values are placed.
// It is explicit and independent of Direction, like SQL NULLS FIRST/LAST.
type NullOrder int

const (
	NullsLast NullOrder = iota
	NullsFirst
)

// SortField describes ordering by one record field.
type SortField struct {
	Name  string
	Dir   Direction
	Nulls NullOrder
}

// SortSpec is the full multi-field ordering. The unique record ID is always
// appended implicitly as the final ascending tie-breaker, so any spec yields
// a total order.
type SortSpec []SortField

// hash returns a stable fingerprint of the spec. Cursors embed it so a cursor
// created under one ordering is rejected when the ordering changes.
func (s SortSpec) hash() string {
	type canonField struct {
		Name  string `json:"n"`
		Dir   int    `json:"d"`
		Nulls int    `json:"z"`
	}
	fields := make([]canonField, len(s))
	for i, f := range s {
		fields[i] = canonField{Name: f.Name, Dir: int(f.Dir), Nulls: int(f.Nulls)}
	}
	raw, _ := json.Marshal(fields)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:16])
}

// key is the full sort key of a record: the field values in spec order plus
// the unique ID tie-breaker. Cursors store a key, never a row position, so
// inserts/updates/deletes of other rows (including the boundary row itself)
// cannot disturb pagination.
type key struct {
	Values []any
	ID     string
}

// keyOf extracts the sort key of a record under the spec.
func (s SortSpec) keyOf(rec Record) key {
	values := make([]any, len(s))
	for i, f := range s {
		values[i] = rec.Fields[f.Name]
	}
	return key{Values: values, ID: rec.ID}
}

// compareKeys is the total order over keys: field by field with explicit null
// placement and per-field direction, then ascending ID as tie-breaker.
func (s SortSpec) compareKeys(a, b key) int {
	for i, f := range s {
		av, bv := a.Values[i], b.Values[i]
		// Null placement is independent of direction, like SQL NULLS
		// FIRST/LAST: descending order must not move NULLs around.
		aNil, bNil := av == nil, bv == nil
		if aNil || bNil {
			switch {
			case aNil && bNil:
				continue
			case aNil:
				if f.Nulls == NullsFirst {
					return -1
				}
				return 1
			default:
				if f.Nulls == NullsFirst {
					return 1
				}
				return -1
			}
		}
		c := compareValues(av, bv)
		if c == 0 {
			continue
		}
		if f.Dir == Desc {
			c = -c
		}
		return c
	}
	switch {
	case a.ID < b.ID:
		return -1
	case a.ID > b.ID:
		return 1
	default:
		return 0
	}
}

// compareValues orders two non-nil field values.
// Supported types: string, int64, float64, bool.
func compareValues(a, b any) int {
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		if !ok {
			panic(fmt.Sprintf("snapshotpage: mixed types %T and %T in sort field", a, b))
		}
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		}
	case int64:
		bv, ok := b.(int64)
		if !ok {
			panic(fmt.Sprintf("snapshotpage: mixed types %T and %T in sort field", a, b))
		}
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		}
	case float64:
		bv, ok := b.(float64)
		if !ok {
			panic(fmt.Sprintf("snapshotpage: mixed types %T and %T in sort field", a, b))
		}
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		}
	case bool:
		bv, ok := b.(bool)
		if !ok {
			panic(fmt.Sprintf("snapshotpage: mixed types %T and %T in sort field", a, b))
		}
		switch {
		case !av && bv:
			return -1
		case av && !bv:
			return 1
		}
	default:
		panic(fmt.Sprintf("snapshotpage: unsupported sort value type %T", a))
	}
	return 0
}

// sortRecords orders records by the spec's total order.
func (s SortSpec) sortRecords(recs []Record) {
	sort.SliceStable(recs, func(i, j int) bool {
		return s.compareKeys(s.keyOf(recs[i]), s.keyOf(recs[j])) < 0
	})
}
