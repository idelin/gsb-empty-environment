package temporal

import "sort"

// ExclusionConstraint is the in-process analogue of
//
//	EXCLUDE USING gist (tenant WITH =, valid_during WITH &&)
//
// EqualityColumns are compared with equality; two rows sharing the same key
// tuple must not have overlapping Range values. Empty ranges never overlap
// and therefore never violate the constraint, but they can be reported
// separately via CheckEmptyRanges.
type ExclusionConstraint struct {
	// Name identifies the constraint in violation reports.
	Name string
	// Table is the relation the constraint applies to.
	Table string
	// EqualityColumns are the "WITH =" columns, e.g. ["tenant_id"].
	EqualityColumns []string
}

// CheckExclusion scans rows and returns every overlapping same-key pair.
// Rows may belong to Table; when c.Table is empty all rows are considered.
func CheckExclusion(rows []Row, c ExclusionConstraint) []*ExclusionViolation {
	var violations []*ExclusionViolation

	indexed := make([]Row, len(rows))
	for i, r := range rows {
		indexed[i] = r
		if r.Key == nil && len(c.EqualityColumns) > 0 && r.Fields != nil {
			indexed[i].Key = r.EffectiveKey(c.EqualityColumns)
		}
	}

	for i := 0; i < len(indexed); i++ {
		ri := indexed[i]
		if c.Table != "" && ri.Table != "" && ri.Table != c.Table {
			continue
		}
		riRange := ri.Range.Normalize()
		if riRange.IsEmpty() {
			continue
		}
		keyI := ri.EffectiveKey(c.EqualityColumns)

		for j := i + 1; j < len(indexed); j++ {
			rj := indexed[j]
			if c.Table != "" && rj.Table != "" && rj.Table != c.Table {
				continue
			}
			rjRange := rj.Range.Normalize()
			if rjRange.IsEmpty() {
				continue
			}
			keyJ := rj.EffectiveKey(c.EqualityColumns)
			if !KeysEqual(keyI, keyJ) {
				continue
			}
			ov := Intersect(riRange, rjRange)
			if ov.IsEmpty() {
				continue
			}
			name := c.Name
			if name == "" {
				name = "exclusion_constraint"
			}
			violations = append(violations, &ExclusionViolation{
				Constraint: name,
				Table:      c.Table,
				Key:        keyI,
				Left:       RowRef{ID: ri.ID, Index: i, Range: riRange},
				Right:      RowRef{ID: rj.ID, Index: j, Range: rjRange},
				Overlap:    ov,
			})
		}
	}

	sort.SliceStable(violations, func(a, b int) bool {
		if violations[a].Left.Index != violations[b].Left.Index {
			return violations[a].Left.Index < violations[b].Left.Index
		}
		return violations[a].Right.Index < violations[b].Right.Index
	})
	return violations
}

// ValidateExclusion returns an aggregated error (matching ErrOverlap via
// errors.Is) for every violated constraint.
func ValidateExclusion(rows []Row, constraints ...ExclusionConstraint) error {
	ve := &ValidationError{}
	for _, c := range constraints {
		ve.Exclusion = append(ve.Exclusion, CheckExclusion(rows, c)...)
	}
	return ve.OrNil()
}

// CheckEmptyRanges returns an error for every row whose normalized range is
// empty. UpdatePortion never creates such rows; this backs the "reject empty
// remnants" guarantee for inserted/imported data.
func CheckEmptyRanges(table string, rows []Row) []EmptyRangeError {
	var out []EmptyRangeError
	for i, r := range rows {
		if r.Range.IsEmpty() {
			out = append(out, EmptyRangeError{
				Table: table,
				Ref:   RowRef{ID: r.ID, Index: i, Range: r.Range},
			})
		}
	}
	return out
}
