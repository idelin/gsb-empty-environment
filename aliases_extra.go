package temporal

// Additional vocabulary aliases for the same Range value object.

// TemporalRange is an alias for Range.
type TemporalRange = Range

// Period is an alias for Range (SQL PERIOD / validity period vocabulary).
type Period = Range

// Span is an alias for Range.
type Span = Range

// Interval is an alias for Range.
type Interval = Range

// Validity is an alias for Range (valid_during vocabulary).
type Validity = Range

// NewPeriod builds a range via NewRange.
func NewPeriod(args ...any) Range { return NewRange(args...) }

// ParsePeriod parses a range expression.
func ParsePeriod(s string, kind ...Kind) (Range, error) { return ParseRange(s, kind...) }

// MustParsePeriod is the panicking ParsePeriod.
func MustParsePeriod(s string, kind ...Kind) Range { return MustParseRange(s, kind...) }

// Update is an alias carrier type name for executor-facing docs.
type Update = PortionReport

// RangeTable is an alias for Table.
type RangeTable = Table

// TemporalTable is an alias for Table.
type TemporalTable = Table

// Relation is an alias for Table.
type Relation = Table

// RangeExecutor is an alias for Executor.
type RangeExecutor = Executor

// NewRangeTable is an alias for NewTable.
func NewRangeTable(name string, keyColumns ...string) *Table { return NewTable(name, keyColumns...) }

// NewTemporalTable is an alias for NewTable.
func NewTemporalTable(name string, keyColumns ...string) *Table {
	return NewTable(name, keyColumns...)
}

// SplitForPortion is the pure row-splitting transformation behind
// Table.UpdatePortion: it returns the strict left/middle/right pieces for
// every matching row without validating or mutating any table.
func SplitForPortion(rows []Row, key any, target any, payload any, keyColumns ...string) ([]Row, error) {
	t := &Table{KeyColumns: keyColumns, Rows: rows, RejectEmpty: true}
	rep, err := t.buildPortion(key, target, payload)
	if err != nil {
		return nil, err
	}
	return rep.Rows, nil
}

// Range-prefixed algebra aliases.

// RangeIntersect is an alias for Intersect.
func RangeIntersect(a, b Range) Range { return Intersect(a, b) }

// RangeIntersection is an alias for Intersection.
func RangeIntersection(a, b Range) Range { return Intersection(a, b) }

// RangeUnion is an alias for Union.
func RangeUnion(a, b Range) []Range { return Union(a, b) }

// RangeDifference is an alias for Difference.
func RangeDifference(a, b Range) []Range { return Difference(a, b) }

// RangeDiff is an alias for Difference.
func RangeDiff(a, b Range) []Range { return Difference(a, b) }

// RangeAdjacent is an alias for Adjacent.
func RangeAdjacent(a, b Range) bool { return Adjacent(a, b) }

// RangeOverlap is an alias for Overlaps.
func RangeOverlap(a, b Range) bool { return Overlaps(a, b) }

// RangeOverlaps is an alias for Overlaps.
func RangeOverlaps(a, b Range) bool { return Overlaps(a, b) }

// HasOverlap is an alias for Overlaps.
func HasOverlap(a, b Range) bool { return Overlaps(a, b) }

// RangeContains reports whether a contains point x.
func RangeContains(a Range, x int64) bool { return a.Contains(x) }

// RangeCoveredBy is an alias for CoveredBy.
func RangeCoveredBy(child Range, covers []Range) bool { return CoveredBy(child, covers) }
