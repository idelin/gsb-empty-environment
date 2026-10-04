package provenance

import (
	"fmt"
	"sort"
	"strings"
)

// Row is a tuple together with its provenance polynomial.
type Row struct {
	Values []string
	Prov   Poly
}

// Relation is a multiset of rows over a named schema.
type Relation struct {
	Schema []string
	Rows   []Row
}

// BaseRow is an input tuple with a stable identifier; the identifier is
// used as the row's provenance symbol.
type BaseRow struct {
	ID     string
	Values []string
}

// NewBaseRelation annotates every input row with its own symbol: the
// provenance of row id is the polynomial 1*id.
func NewBaseRelation(schema []string, rows []BaseRow) *Relation {
	rel := &Relation{Schema: append([]string(nil), schema...)}
	seen := make(map[string]bool)
	for _, r := range rows {
		if len(r.Values) != len(schema) {
			panic(fmt.Sprintf("provenance: row %q has %d values, schema has %d attributes", r.ID, len(r.Values), len(schema)))
		}
		if seen[r.ID] {
			panic(fmt.Sprintf("provenance: duplicate base row id %q", r.ID))
		}
		seen[r.ID] = true
		rel.Rows = append(rel.Rows, Row{
			Values: append([]string(nil), r.Values...),
			Prov:   Var(r.ID),
		})
	}
	return rel
}

func tupleKey(values []string) string {
	return strings.Join(values, "\x00")
}

// group sums the provenance of identical tuples, preserving first-seen order.
func group(schema []string, rows []Row) *Relation {
	out := &Relation{Schema: schema}
	index := make(map[string]int)
	for _, r := range rows {
		k := tupleKey(r.Values)
		if i, ok := index[k]; ok {
			out.Rows[i].Prov = out.Rows[i].Prov.Add(r.Prov)
		} else {
			index[k] = len(out.Rows)
			out.Rows = append(out.Rows, r)
		}
	}
	return out
}

func (r *Relation) col(attr string) int {
	for i, a := range r.Schema {
		if a == attr {
			return i
		}
	}
	panic(fmt.Sprintf("provenance: no attribute %q in schema %v", attr, r.Schema))
}

// Project performs multiset projection onto the given attributes: duplicate
// tuples are merged and their provenance polynomials are added.
func (r *Relation) Project(attrs ...string) *Relation {
	idx := make([]int, len(attrs))
	for i, a := range attrs {
		idx[i] = r.col(a)
	}
	rows := make([]Row, 0, len(r.Rows))
	for _, row := range r.Rows {
		vals := make([]string, len(idx))
		for i, j := range idx {
			vals[i] = row.Values[j]
		}
		rows = append(rows, Row{Values: vals, Prov: row.Prov})
	}
	return group(append([]string(nil), attrs...), rows)
}

// Union performs multiset union. Both relations must have the same set of
// attributes; columns of s are aligned to r by name. Identical tuples have
// their provenance polynomials added.
func (r *Relation) Union(s *Relation) *Relation {
	perm := make([]int, len(r.Schema))
	for i, a := range r.Schema {
		perm[i] = s.col(a)
	}
	if len(s.Schema) != len(r.Schema) {
		panic(fmt.Sprintf("provenance: union schema mismatch %v vs %v", r.Schema, s.Schema))
	}
	rows := make([]Row, 0, len(r.Rows)+len(s.Rows))
	rows = append(rows, r.Rows...)
	for _, row := range s.Rows {
		vals := make([]string, len(perm))
		for i, j := range perm {
			vals[i] = row.Values[j]
		}
		rows = append(rows, Row{Values: vals, Prov: row.Prov})
	}
	return group(append([]string(nil), r.Schema...), rows)
}

// Join computes the natural join over the attributes common to both
// schemas. The provenance of a joined row is the product of the inputs'
// polynomials; identical result tuples are merged by addition.
func (r *Relation) Join(s *Relation) *Relation {
	sHas := make(map[string]bool)
	for _, a := range s.Schema {
		sHas[a] = true
	}
	var common []string
	for _, a := range r.Schema {
		if sHas[a] {
			common = append(common, a)
		}
	}
	rIdx := make([]int, len(common))
	sIdx := make([]int, len(common))
	for i, a := range common {
		rIdx[i] = r.col(a)
		sIdx[i] = s.col(a)
	}
	// Result schema: r's attributes followed by s's non-common attributes.
	schema := append([]string(nil), r.Schema...)
	var sRest []int
	for j, a := range s.Schema {
		if !contains(r.Schema, a) {
			schema = append(schema, a)
			sRest = append(sRest, j)
		}
	}
	// Hash index on s's join key.
	type entry struct {
		vals []string
		prov Poly
	}
	index := make(map[string][]entry)
	for _, row := range s.Rows {
		key := make([]string, len(sIdx))
		for i, j := range sIdx {
			key[i] = row.Values[j]
		}
		index[tupleKey(key)] = append(index[tupleKey(key)], entry{vals: row.Values, prov: row.Prov})
	}
	var rows []Row
	for _, row := range r.Rows {
		key := make([]string, len(rIdx))
		for i, j := range rIdx {
			key[i] = row.Values[j]
		}
		for _, e := range index[tupleKey(key)] {
			vals := make([]string, 0, len(schema))
			vals = append(vals, row.Values...)
			for _, j := range sRest {
				vals = append(vals, e.vals[j])
			}
			rows = append(rows, Row{Values: vals, Prov: row.Prov.Mul(e.prov)})
		}
	}
	return group(schema, rows)
}

// Delete incrementally applies the deletion of the base row annotated with
// symbol: every provenance term mentioning the symbol vanishes (the symbol
// is substituted by 0). Rows whose provenance becomes zero are removed.
// It does not recompute the query.
func (r *Relation) Delete(symbol string) *Relation {
	out := &Relation{Schema: append([]string(nil), r.Schema...)}
	for _, row := range r.Rows {
		p := row.Prov.DropSymbol(symbol)
		if p.IsZero() {
			continue
		}
		out.Rows = append(out.Rows, Row{Values: row.Values, Prov: p})
	}
	return out
}

// DeleteDelta returns, per row, the provenance contribution lost when the
// base row annotated with symbol is deleted (the negative delta). The
// result aligns with r.Rows by index; a zero polynomial means the output
// row is unaffected.
func (r *Relation) DeleteDelta(symbol string) []Poly {
	deltas := make([]Poly, len(r.Rows))
	for i, row := range r.Rows {
		deltas[i] = row.Prov.TermsContaining(symbol)
	}
	return deltas
}

// Lookup returns the provenance polynomial of the first row matching the
// given tuple, and whether such a row exists.
func (r *Relation) Lookup(values ...string) (Poly, bool) {
	k := tupleKey(values)
	for _, row := range r.Rows {
		if tupleKey(row.Values) == k {
			return row.Prov, true
		}
	}
	return Zero(), false
}

// SortedRows returns the rows ordered by tuple for deterministic output.
func (r *Relation) SortedRows() []Row {
	rows := append([]Row(nil), r.Rows...)
	sort.Slice(rows, func(i, j int) bool {
		return tupleKey(rows[i].Values) < tupleKey(rows[j].Values)
	})
	return rows
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
