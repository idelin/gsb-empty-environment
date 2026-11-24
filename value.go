package viewopt

import (
	"fmt"
	"sort"
)

// Value is a SQL-style nullable 64-bit integer.
//
// The zero value is the non-null integer 0.  SQL NULL is represented by
// Value{Null: true}.  NULL in a grouping key is a real, independent group; it
// is not equal to any non-null value.
type Value struct {
	V    int64
	Null bool
}

// Int returns a non-null value.
func Int(v int64) Value { return Value{V: v} }

// Null returns the SQL NULL value.
func Null() Value { return Value{Null: true} }

// IsNull reports whether v is SQL NULL.
func (v Value) IsNull() bool { return v.Null }

// Compare defines a total order on values.  NULL sorts before every non-null
// value; non-null values are ordered numerically.  The order is used only for
// deterministic output and counterexample tie-breaking.
func (v Value) Compare(o Value) int {
	if v.Null != o.Null {
		if v.Null {
			return -1
		}
		return 1
	}
	if v.Null {
		return 0
	}
	switch {
	case v.V < o.V:
		return -1
	case v.V > o.V:
		return 1
	default:
		return 0
	}
}

func (v Value) String() string {
	if v.Null {
		return "NULL"
	}
	return fmt.Sprintf("%d", v.V)
}

// Key is a grouping key over at most three dimensions.  Components that are
// not used by a particular grouping projection are left as NULL.
type Key [3]Value

// K builds a key, padding missing dimensions with NULL.
func K(values ...Value) Key {
	var k Key
	for i := range k {
		k[i] = Null()
	}
	for i := 0; i < len(values) && i < len(k); i++ {
		k[i] = values[i]
	}
	return k
}

// Compare orders keys component by component.
func (k Key) Compare(o Key) int {
	for i := range k {
		if c := k[i].Compare(o[i]); c != 0 {
			return c
		}
	}
	return 0
}

func (k Key) String() string {
	return fmt.Sprintf("(%s,%s,%s)", k[0], k[1], k[2])
}

// Row is one base-table row.
//
// Filter is the scalar integer coordinate used by interval filters.  A view
// spec may describe any integer-linear expression; the expression's value for
// this row is what belongs in Filter.  Measure is nullable: SUM and
// COUNT(measure) ignore NULL measures, while COUNT(*) counts the row.
type Row struct {
	Key     Key
	Filter  int64
	Measure Value
}

// Compare orders rows lexicographically by key, filter coordinate and measure.
func (r Row) Compare(o Row) int {
	if c := r.Key.Compare(o.Key); c != 0 {
		return c
	}
	switch {
	case r.Filter < o.Filter:
		return -1
	case r.Filter > o.Filter:
		return 1
	}
	return r.Measure.Compare(o.Measure)
}

func (r Row) String() string {
	return fmt.Sprintf("%s@%d=%s", r.Key, r.Filter, r.Measure)
}

// Table is a multiset of rows.  Repeated rows are allowed.
type Table []Row

// Sorted returns a sorted copy of t.
func (t Table) Sorted() Table {
	out := append(Table(nil), t...)
	sort.Slice(out, func(i, j int) bool { return out[i].Compare(out[j]) < 0 })
	return out
}

// CompareTables orders tables first by row count, then lexicographically by
// their sorted row sequence.  It is the tie-breaker used for counterexamples.
func CompareTables(a, b Table) int {
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	as, bs := a.Sorted(), b.Sorted()
	for i := range as {
		if c := as[i].Compare(bs[i]); c != 0 {
			return c
		}
	}
	return 0
}

func (t Table) String() string {
	s := t.Sorted()
	out := "["
	for i, r := range s {
		if i > 0 {
			out += ", "
		}
		out += r.String()
	}
	return out + "]"
}
