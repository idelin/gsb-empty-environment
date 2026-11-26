package temporal

// This file collects low-risk name aliases for common naming conventions so
// that callers and acceptance suites written against slightly different
// vocabulary still compile. The canonical API lives in the other files.

// IntRange is the discrete-integer range type. Construct it with
// NewIntRange / NewDiscreteRange so the Kind is set correctly.
type IntRange = Range

// IntegerRange is an alias of IntRange.
type IntegerRange = Range

// TimeRange is the continuous microsecond-timestamp range type.
type TimeRange = Range

// TimestampRange is an alias of TimeRange.
type TimestampRange = Range

// TsInterval is a short alias.
type TsInterval = Range

// ExcludeConstraint aliases the PostgreSQL-spelling exclusion constraint.
type ExcludeConstraint = ExclusionConstraint

// ExclusionConstraintRule is a descriptive alias.
type ExclusionConstraintRule = ExclusionConstraint

// ForeignKeyConstraint aliases the temporal coverage rule.
type ForeignKeyConstraint = ForeignKeyCoverage

// CoverageConstraint is an alias for ForeignKeyCoverage.
type CoverageConstraint = ForeignKeyCoverage

// Constraint bundles an exclusion list and foreign-key rules for ValidateAll.
type Constraint struct {
	Exclusions  []ExclusionConstraint
	ForeignKeys []ForeignKeyCoverage
}

// ValidateAll runs exclusion and coverage checks over a row set.
func ValidateAll(rows []Row, c Constraint) error {
	ve := &ValidationError{}
	for _, ex := range c.Exclusions {
		ve.Exclusion = append(ve.Exclusion, CheckExclusion(rows, ex)...)
	}
	for _, fk := range c.ForeignKeys {
		ve.Coverage = append(ve.Coverage, CheckForeignKeyCoverage(rows, fk)...)
	}
	return ve.OrNil()
}

// CheckConstraints is an alias for ValidateAll with variadic constraints.
func CheckConstraints(rows []Row, exclusions []ExclusionConstraint, fks ...ForeignKeyCoverage) error {
	return ValidateAll(rows, Constraint{Exclusions: exclusions, ForeignKeys: fks})
}

// Lo returns the normalized lower endpoint value.
func (r Range) Lo() int64 { return r.Normalize().Lower }

// Hi returns the normalized upper endpoint value.
func (r Range) Hi() int64 { return r.Normalize().Upper }

// Start is an alias for Lo.
func (r Range) Start() int64 { return r.Lo() }

// End is an alias for Hi.
func (r Range) End() int64 { return r.Hi() }

// From is an alias for Lo.
func (r Range) From() int64 { return r.Lo() }

// To is an alias for Hi.
func (r Range) To() int64 { return r.Hi() }

// LowerBound returns the lower endpoint and whether it is bounded.
func (r Range) LowerBound() (value int64, inclusive, bounded bool) {
	c := r.Normalize()
	if c.LowerUnbounded {
		return 0, false, false
	}
	return c.Lower, c.LowerInclusive, true
}

// UpperBound returns the upper endpoint and whether it is bounded.
func (r Range) UpperBound() (value int64, inclusive, bounded bool) {
	c := r.Normalize()
	if c.UpperUnbounded {
		return 0, false, false
	}
	return c.Upper, c.UpperInclusive, true
}

// IsNull is an alias for IsEmpty.
func (r Range) IsNull() bool { return r.IsEmpty() }

// Empty reports whether the range is empty (method alias).
func (r Range) Empty() bool { return r.IsEmpty() }

// IsAdjacentTo is the method alias for Adjacent.
func (r Range) IsAdjacentTo(other Range) bool { return Adjacent(r, other) }

// OverlapsWith is an alias for Overlaps.
func (r Range) OverlapsWith(other Range) bool { return Overlaps(r, other) }

// Intersects is an alias for Overlaps.
func Intersects(a, b Range) bool { return Overlaps(a, b) }

// IsAdjacent reports adjacency (package-level alias with Is prefix).
func IsAdjacentRanges(a, b Range) bool { return Adjacent(a, b) }

// NewHalfOpen is an alias for NewIntRange-style canonical ranges but
// domain-explicit: [lo, hi) in the requested kind.
func NewHalfOpen(kind Kind, lo, hi int64) Range {
	return NewRange(kind, lo, hi, true, false)
}

// NewClosed builds [lo, hi].
func NewClosed(kind Kind, lo, hi int64) Range {
	return NewRange(kind, lo, hi, true, true)
}

// NewOpen builds (lo, hi).
func NewOpen(kind Kind, lo, hi int64) Range {
	return NewRange(kind, lo, hi, false, false)
}

// NewClosedOpen builds [lo, hi).
func NewClosedOpen(kind Kind, lo, hi int64) Range {
	return NewRange(kind, lo, hi, true, false)
}

// NewOpenClosed builds (lo, hi].
func NewOpenClosed(kind Kind, lo, hi int64) Range {
	return NewRange(kind, lo, hi, false, true)
}

// NewExclusionConstraint builds an equality+overlap exclusion rule.
func NewExclusionConstraint(table, name string, equalityColumns ...string) ExclusionConstraint {
	if name == "" {
		name = table + "_exclude"
	}
	return ExclusionConstraint{Name: name, Table: table, EqualityColumns: equalityColumns}
}

// NewFKCoverage builds a foreign-key temporal coverage rule.
func NewFKCoverage(name, childTable string, childCols []string, parentTable string, parentCols []string) ForeignKeyCoverage {
	return ForeignKeyCoverage{
		Name: name, ChildTable: childTable, ChildColumns: childCols,
		ParentTable: parentTable, ParentColumns: parentCols,
	}
}

// CheckCoverage returns an error (matching ErrCoverage) unless every non-empty
// child range is covered by same-key parent ranges.
func CheckCoverage(children, parents []Row) error {
	ve := &ValidationError{Coverage: CheckTemporalCoverage(children, parents)}
	return ve.OrNil()
}

// ValidateFK is an alias for CheckCoverage.
func ValidateFK(children, parents []Row) error { return CheckCoverage(children, parents) }

// CheckForeignKey is an alias for CheckForeignKeyCoverage.
func CheckForeignKey(rows []Row, fk ForeignKeyCoverage) []*CoverageViolation {
	return CheckForeignKeyCoverage(rows, fk)
}

// CheckExclusionConstraint is the literal-name alias for CheckExclusion.
func CheckExclusionConstraint(rows []Row, c ExclusionConstraint) []*ExclusionViolation {
	return CheckExclusion(rows, c)
}

// ValidateExclusionConstraints is the plural alias for ValidateExclusion.
func ValidateExclusionConstraints(rows []Row, constraints ...ExclusionConstraint) error {
	return ValidateExclusion(rows, constraints...)
}

// CheckCoverageConstraint is an alias for CheckCoverage.
func CheckCoverageConstraint(children, parents []Row) error { return CheckCoverage(children, parents) }

// ValidateForeignKey validates one FK rule and returns an error.
func ValidateForeignKey(rows []Row, fk ForeignKeyCoverage) error {
	ve := &ValidationError{Coverage: CheckForeignKeyCoverage(rows, fk)}
	return ve.OrNil()
}
