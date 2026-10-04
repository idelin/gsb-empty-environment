package provenance

// RowDelta describes how one output row changes when a base row is deleted.
type RowDelta struct {
	Vals  []Value
	Old   *Poly // provenance before deletion
	New   *Poly // provenance after deletion (zero means the row disappears)
	Delta *Poly // New - Old; coefficients are <= 0
}

// DeleteVar incrementally computes the effect of deleting the base row
// tagged with variable id from an output relation.
//
// In the provenance semiring N[X], deletion of the row tagged x corresponds
// to the evaluation x = 0: every monomial mentioning x loses its witness, so
// each output polynomial p is replaced by p|_{x=0}. This avoids rerunning
// the query; the result is identical to direct recomputation on the reduced
// input (see the package tests, which cross-check exactly that).
//
// It returns the new output relation (rows whose provenance became zero are
// dropped) plus one RowDelta per affected output row.
func DeleteVar(out *Relation, id uint64) (newOut *Relation, deltas []RowDelta) {
	norm := out.Normalize()
	newOut = &Relation{Name: out.Name, Cols: out.Cols}
	for _, row := range norm.Rows {
		np := row.Prov.DropVar(id)
		if np.Equal(row.Prov) {
			newOut.Rows = append(newOut.Rows, &Row{Vals: row.Vals, Prov: row.Prov.Clone()})
			continue
		}
		deltas = append(deltas, RowDelta{
			Vals:  row.Vals,
			Old:   row.Prov.Clone(),
			New:   np.Clone(),
			Delta: np.Sub(row.Prov),
		})
		if !np.IsZero() {
			newOut.Rows = append(newOut.Rows, &Row{Vals: row.Vals, Prov: np})
		}
	}
	return newOut, deltas
}

// WithoutVar returns a copy of a base relation with the row tagged id
// removed. It is meant for rebuilding inputs when verifying an incremental
// deletion against direct recomputation.
func WithoutVar(r *Relation, id uint64) *Relation {
	out := &Relation{Name: r.Name, Cols: r.Cols}
	for _, row := range r.Rows {
		if row.Prov.Equal(VarPoly(id)) {
			continue
		}
		out.Rows = append(out.Rows, &Row{Vals: row.Vals, Prov: row.Prov.Clone()})
	}
	return out
}
