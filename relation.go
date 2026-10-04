package provenance

import (
	"fmt"
	"sort"
	"strings"
)

// Value is a single cell value. Values must be usable with fmt's %#v verb,
// which is how row identity keys are derived.
type Value = any

// Row is a tuple together with its provenance polynomial.
type Row struct {
	Vals []Value
	Prov *Poly
}

// Relation is a multiset of rows. After Normalize, rows are distinct by
// value and each row's polynomial accumulates all duplicate contributions.
type Relation struct {
	Name string
	Cols []string
	Rows []*Row
}

// VarAllocator hands out fresh provenance symbols for base rows.
type VarAllocator struct{ next uint64 }

// NewVarAllocator returns an allocator whose first symbol is x1.
func NewVarAllocator() *VarAllocator { return &VarAllocator{next: 0} }

// Next returns a fresh variable id.
func (a *VarAllocator) Next() uint64 {
	a.next++
	return a.next
}

// NewBaseRelation creates a base relation in which every row is annotated
// with its own independent symbol.
func NewBaseRelation(alloc *VarAllocator, name string, cols []string, tuples [][]Value) *Relation {
	r := &Relation{Name: name, Cols: cols}
	for _, t := range tuples {
		r.Rows = append(r.Rows, &Row{Vals: t, Prov: VarPoly(alloc.Next())})
	}
	return r
}

// rowKey is a canonical identity key for a tuple.
func rowKey(vals []Value) string {
	var b strings.Builder
	for _, v := range vals {
		fmt.Fprintf(&b, "%#v\x00", v)
	}
	return b.String()
}

// Normalize merges rows with identical values, summing their provenance
// polynomials. This is where duplicate (multiset) contributions are folded
// into coefficients instead of being dropped.
func (r *Relation) Normalize() *Relation {
	byKey := map[string]*Row{}
	order := []string{}
	for _, row := range r.Rows {
		k := rowKey(row.Vals)
		if acc, ok := byKey[k]; ok {
			acc.Prov.AddInto(row.Prov)
		} else {
			byKey[k] = &Row{Vals: row.Vals, Prov: row.Prov.Clone()}
			order = append(order, k)
		}
	}
	out := &Relation{Name: r.Name, Cols: r.Cols}
	for _, k := range order {
		out.Rows = append(out.Rows, byKey[k])
	}
	return out
}

// Union computes the bag union of two relations and normalizes the result:
// tuples present in both inputs get the sum of both polynomials.
func Union(a, b *Relation) *Relation {
	out := &Relation{Name: a.Name + "+" + b.Name, Cols: a.Cols}
	out.Rows = append(out.Rows, a.Rows...)
	out.Rows = append(out.Rows, b.Rows...)
	return out.Normalize()
}

// Project projects every row onto the given column indexes (bag semantics)
// and normalizes: tuples that collapse onto the same image have their
// polynomials added.
func Project(r *Relation, colIdx ...int) *Relation {
	cols := make([]string, len(colIdx))
	for i, c := range colIdx {
		cols[i] = r.Cols[c]
	}
	out := &Relation{Name: "π(" + r.Name + ")", Cols: cols}
	for _, row := range r.Rows {
		vals := make([]Value, len(colIdx))
		for i, c := range colIdx {
			vals[i] = row.Vals[c]
		}
		out.Rows = append(out.Rows, &Row{Vals: vals, Prov: row.Prov})
	}
	return out.Normalize()
}

// Join equi-joins a and b on the given key columns. The output schema is
// a's columns followed by b's non-key columns. Each matched pair's
// provenance is the product of the inputs' polynomials.
func Join(a, b *Relation, aKeys, bKeys []int) *Relation {
	if len(aKeys) != len(bKeys) {
		panic("Join: key column count mismatch")
	}
	bNonKey := []int{}
	for j := range b.Cols {
		isKey := false
		for _, k := range bKeys {
			if j == k {
				isKey = true
				break
			}
		}
		if !isKey {
			bNonKey = append(bNonKey, j)
		}
	}
	cols := append([]string{}, a.Cols...)
	for _, j := range bNonKey {
		cols = append(cols, b.Cols[j])
	}

	index := map[string][]*Row{}
	for _, row := range b.Rows {
		key := make([]Value, len(bKeys))
		for i, k := range bKeys {
			key[i] = row.Vals[k]
		}
		rk := rowKey(key)
		index[rk] = append(index[rk], row)
	}

	out := &Relation{Name: a.Name + "⋈" + b.Name, Cols: cols}
	for _, ra := range a.Rows {
		key := make([]Value, len(aKeys))
		for i, k := range aKeys {
			key[i] = ra.Vals[k]
		}
		for _, rb := range index[rowKey(key)] {
			vals := make([]Value, 0, len(cols))
			vals = append(vals, ra.Vals...)
			for _, j := range bNonKey {
				vals = append(vals, rb.Vals[j])
			}
			out.Rows = append(out.Rows, &Row{Vals: vals, Prov: ra.Prov.Mul(rb.Prov)})
		}
	}
	return out.Normalize()
}

// polyByRowKey indexes a normalized relation's polynomials by tuple key.
func (r *Relation) polyByRowKey() map[string]*Poly {
	m := map[string]*Poly{}
	for _, row := range r.Normalize().Rows {
		m[rowKey(row.Vals)] = row.Prov
	}
	return m
}

// EqualRelation reports whether two relations have identical tuples with
// identical provenance polynomials.
func EqualRelation(a, b *Relation) bool {
	ma, mb := a.polyByRowKey(), b.polyByRowKey()
	if len(ma) != len(mb) {
		return false
	}
	for k, pa := range ma {
		pb, ok := mb[k]
		if !ok || !pa.Equal(pb) {
			return false
		}
	}
	return true
}

// String renders the relation as a sorted table of tuple -> polynomial.
func (r *Relation) String() string {
	lines := make([]string, 0, len(r.Rows))
	for _, row := range r.Normalize().Rows {
		lines = append(lines, fmt.Sprintf("%v  =>  %s", row.Vals, row.Prov))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
