package temporal

// ForeignKeyCoverage is a temporal referential-integrity rule: every
// non-empty validity interval of a child row must be completely covered by
// the union of adjacent or overlapping parent rows whose matched key tuple
// is equal.
//
// ChildColumns and ParentColumns are paired by position. Include the tenant
// equality column in both lists to model
// "child (tenant_id, parent_id) -> parent (tenant_id, id)".
type ForeignKeyCoverage struct {
	// Name identifies the constraint in reports.
	Name string

	// ChildTable is the referencing relation.
	ChildTable string
	// ChildColumns are the matching key columns in the child.
	ChildColumns []string

	// ParentTable is the referenced relation.
	ParentTable string
	// ParentColumns are the matching unique-key columns in the parent.
	// When empty, ChildColumns names are used on both sides.
	ParentColumns []string
}

func (f ForeignKeyCoverage) parentCols() []string {
	if len(f.ParentColumns) > 0 {
		return f.ParentColumns
	}
	return f.ChildColumns
}

// CheckForeignKeyCoverage evaluates one FK coverage rule against all rows.
func CheckForeignKeyCoverage(rows []Row, f ForeignKeyCoverage) []*CoverageViolation {
	parentCols := f.parentCols()
	name := f.Name
	if name == "" {
		name = "fk_temporal_coverage"
	}

	var parents []Row
	var children []Row
	for _, r := range rows {
		switch {
		case f.ParentTable != "" && r.Table == f.ParentTable:
			parents = append(parents, r)
		case f.ChildTable != "" && r.Table == f.ChildTable:
			children = append(children, r)
		case f.ParentTable == "" && f.ChildTable == "":
			// Unscoped: caller separates via CheckTemporalCoverage.
		}
	}

	var violations []*CoverageViolation
	for ci, child := range children {
		cr := child.Range.Normalize()
		if cr.IsEmpty() {
			// Empty child intervals impose no coverage requirement.
			continue
		}
		childTuple := tupleFromRow(child, f.ChildColumns)

		var coverRanges []Range
		for _, p := range parents {
			pr := p.Range.Normalize()
			if pr.IsEmpty() {
				continue
			}
			parentTuple := tupleFromRow(p, parentCols)
			if !tuplesMatch(childTuple, f.ChildColumns, parentTuple, parentCols) {
				continue
			}
			coverRanges = append(coverRanges, pr)
		}

		missing := MissingCoverage(cr, coverRanges)
		if len(missing) > 0 {
			violations = append(violations, &CoverageViolation{
				Constraint: name,
				ChildTable: f.ChildTable,
				Child:      RowRef{ID: child.ID, Index: ci, Range: cr},
				ChildKey:   childTuple,
				Missing:    missing,
			})
		}
	}
	return violations
}

func tupleFromRow(r Row, cols []string) Key {
	if len(cols) == 0 {
		return r.EffectiveKey(nil)
	}
	k := Key{}
	for _, c := range cols {
		if r.Key != nil {
			if v, ok := r.Key[c]; ok {
				k[c] = v
				continue
			}
		}
		if r.Fields != nil {
			if v, ok := r.Fields[c]; ok {
				k[c] = v
			}
		}
	}
	return k
}

// tuplesMatch compares paired columns positionally, tolerating different
// column names between child and parent sides.
func tuplesMatch(child Key, childCols []string, parent Key, parentCols []string) bool {
	if len(childCols) == 0 {
		return KeysEqual(child, parent)
	}
	pcols := parentCols
	if len(pcols) != len(childCols) {
		return false
	}
	for i, cc := range childCols {
		pc := pcols[i]
		cv, cok := child[cc]
		pv, pok := parent[pc]
		if cok != pok {
			return false
		}
		if cok && !valuesEqual(cv, pv) {
			return false
		}
	}
	return true
}

// CheckTemporalCoverage verifies that every non-empty child range is covered
// by same-key parent ranges (using Row.Key / EffectiveKey). It is the
// simplest form of the rule when child and parent share key column names.
func CheckTemporalCoverage(children, parents []Row) []*CoverageViolation {
	var violations []*CoverageViolation
	for ci, child := range children {
		cr := child.Range.Normalize()
		if cr.IsEmpty() {
			continue
		}
		ck := child.EffectiveKey(nil)
		var covers []Range
		for _, p := range parents {
			pr := p.Range.Normalize()
			if pr.IsEmpty() {
				continue
			}
			if KeysEqual(ck, p.EffectiveKey(nil)) {
				covers = append(covers, pr)
			}
		}
		if missing := MissingCoverage(cr, covers); len(missing) > 0 {
			violations = append(violations, &CoverageViolation{
				Constraint: "temporal_coverage",
				ChildTable: child.Table,
				Child:      RowRef{ID: child.ID, Index: ci, Range: cr},
				ChildKey:   ck,
				Missing:    missing,
			})
		}
	}
	return violations
}

// ValidateCoverage aggregates FK coverage violations into an error matching
// ErrCoverage via errors.Is.
func ValidateCoverage(rows []Row, fks ...ForeignKeyCoverage) error {
	ve := &ValidationError{}
	for _, fk := range fks {
		ve.Coverage = append(ve.Coverage, CheckForeignKeyCoverage(rows, fk)...)
	}
	return ve.OrNil()
}
