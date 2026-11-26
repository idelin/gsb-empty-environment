package temporal

import (
	"fmt"
	"sort"
)

// Table is an in-memory temporal relation.
//
// UpdatePortion implements UPDATE ... FOR PORTION OF: every row matching the
// key selector and overlapping the target range is split into at most a left
// remnant (old values), an updated middle segment (newPayload) and a right
// remnant (old values). Empty pieces are never stored. The candidate result
// is validated before it replaces the committed rows; any exclusion,
// empty-range or foreign-key coverage violation aborts the replacement, so
// the table stays exactly as it was (rollback).
type Table struct {
	// Name is the relation name used in reports and schema routing.
	Name string

	// KeyColumns are the equality-key columns (WITH =). When empty, Key
	// fields on rows are used directly.
	KeyColumns []string

	// Rows is the committed row set.
	Rows []Row

	// RejectEmpty makes Insert/UpdatePortion fail on empty validity ranges.
	// Defaults to true via NewTable.
	RejectEmpty bool

	// CoalesceAdjacent merges same-key, same-payload adjacent segments after
	// a portion update. The raw split is always available in LastReport.
	CoalesceAdjacent bool

	// LastReport describes the most recent UpdatePortion call on this table.
	LastReport *PortionReport

	schema *Schema
}

// NewTable creates an empty temporal table with equality key columns.
func NewTable(name string, keyColumns ...string) *Table {
	return &Table{
		Name:        name,
		KeyColumns:  keyColumns,
		Rows:        []Row{},
		RejectEmpty: true,
	}
}

// PortionReport records the exact split outcome of one UpdatePortion call.
type PortionReport struct {
	// Target is the normalized FOR PORTION range.
	Target Range
	// MatchedIndexes are committed-row indexes that overlapped Target.
	MatchedIndexes []int
	// Segments are the raw per-row pieces (left, middle, right order).
	Segments []Row
	// Rows are the final candidate rows (after optional coalescing).
	Rows []Row
	// MergedRows is always the adjacent-coalesced view of Rows.
	MergedRows []Row
	// UpdatedRows counts updated middle segments.
	UpdatedRows int
	// RemnantRows counts non-empty left/right remnants.
	RemnantRows int
}

// Len returns the number of final rows.
func (r *PortionReport) Len() int { return len(r.Rows) }

// effectiveExclusion builds the equality+overlap constraint for the table.
func (t *Table) effectiveExclusion() ExclusionConstraint {
	return ExclusionConstraint{
		Name:            t.Name + "_exclude",
		Table:           t.Name,
		EqualityColumns: t.KeyColumns,
	}
}

// Insert appends a row after normalization, rejecting empty ranges and
// same-key overlaps. On violation the row is not appended.
func (t *Table) Insert(row Row) error {
	if t.Rows == nil {
		t.Rows = []Row{}
	}
	row.Table = orDefault(row.Table, t.Name)
	row.Range = row.Range.Normalize()
	if row.Key == nil && len(t.KeyColumns) > 0 {
		row.Key = row.EffectiveKey(t.KeyColumns)
	}

	candidate := make([]Row, 0, len(t.Rows)+1)
	candidate = append(candidate, t.Rows...)
	candidate = append(candidate, row)

	if t.RejectEmpty && row.Range.IsEmpty() {
		return EmptyRangeError{Table: t.Name, Ref: RowRef{ID: row.ID, Index: len(t.Rows), Range: row.Range}}
	}
	// Writes are guarded against empty ranges and same-key overlap
	// immediately. Cross-table foreign-key coverage is deferred to
	// UpdatePortion / Schema.Validate so fixtures may be loaded in any
	// order.
	if err := t.validateInsert(candidate); err != nil {
		return err
	}
	t.Rows = candidate
	return nil
}

// validateInsert checks emptiness and the table exclusion rule only.
func (t *Table) validateInsert(candidate []Row) error {
	ve := &ValidationError{}
	if t.RejectEmpty {
		ve.Empties = append(ve.Empties, CheckEmptyRanges(t.Name, candidate)...)
	}
	ve.Exclusion = append(ve.Exclusion, CheckExclusion(candidate, t.effectiveExclusion())...)
	if t.schema != nil {
		for _, c := range t.schema.Exclusions {
			all := t.schema.combinedRows(t.Name, candidate)
			ve.Exclusion = append(ve.Exclusion, CheckExclusion(all, c)...)
		}
	}
	return ve.OrNil()
}

// MustInsert inserts or panics; handy in tests and fixtures.
func (t *Table) MustInsert(row Row) *Table {
	if err := t.Insert(row); err != nil {
		panic(err)
	}
	return t
}

// InsertRows inserts several rows, stopping at the first error. Rows already
// inserted before the error remain (use a transaction for atomic batches).
func (t *Table) InsertRows(rows ...Row) error {
	for _, r := range rows {
		if err := t.Insert(r); err != nil {
			return err
		}
	}
	return nil
}

// UpdatePortion performs the transactional FOR PORTION update described in
// the package documentation. It returns a non-nil error matching ErrOverlap,
// ErrCoverage or ErrEmptyRange and leaves the table unchanged when the
// candidate result violates any constraint.
func (t *Table) UpdatePortion(key any, targetRange any, newPayload any) error {
	report, err := t.buildPortion(key, targetRange, newPayload)
	if err != nil {
		return err
	}
	if len(report.MatchedIndexes) == 0 {
		t.LastReport = report
		return nil
	}
	if err := t.validateCandidate(report.Rows); err != nil {
		t.LastReport = report
		return err
	}
	t.Rows = report.Rows
	t.LastReport = report
	return nil
}

// UpdatePortionReport is like UpdatePortion but returns the full report. On
// validation failure the report is still returned alongside the error and the
// table is left untouched.
func (t *Table) UpdatePortionReport(key any, targetRange any, newPayload any) (*PortionReport, error) {
	report, err := t.buildPortion(key, targetRange, newPayload)
	if err != nil {
		return nil, err
	}
	if len(report.MatchedIndexes) == 0 {
		t.LastReport = report
		return report, nil
	}
	if err := t.validateCandidate(report.Rows); err != nil {
		t.LastReport = report
		return report, err
	}
	t.Rows = report.Rows
	t.LastReport = report
	return report, nil
}

// ApplyPortion is an alias for UpdatePortionReport returning (rows, error).
func (t *Table) ApplyPortion(key any, targetRange any, newPayload any) ([]Row, error) {
	rep, err := t.UpdatePortionReport(key, targetRange, newPayload)
	if rep == nil {
		return nil, err
	}
	return rep.Rows, err
}

// UpdatePortionRows is an alias for ApplyPortion.
func (t *Table) UpdatePortionRows(key any, targetRange any, newPayload any) ([]Row, error) {
	return t.ApplyPortion(key, targetRange, newPayload)
}

func (t *Table) buildPortion(key any, targetRange any, newPayload any) (*PortionReport, error) {
	target, err := CoerceRange(targetRange)
	if err != nil {
		return nil, err
	}
	target = target.Normalize()
	selector := CoerceKey(key, t.KeyColumns)
	target = t.alignDomain(target)

	report := &PortionReport{Target: target}
	candidate := make([]Row, 0, len(t.Rows)+2)

	for idx, existing := range t.Rows {
		r := existing.Clone()
		r.Range = r.Range.Normalize()
		if r.Table == "" {
			r.Table = t.Name
		}
		rowKey := r.EffectiveKey(t.KeyColumns)

		if r.Range.IsEmpty() || !KeyMatches(rowKey, selector) || !r.Range.Overlaps(target) {
			candidate = append(candidate, r)
			continue
		}

		report.MatchedIndexes = append(report.MatchedIndexes, idx)

		// The middle segment is the exact intersection; Difference yields
		// at most a left and a right remnant. Classify each remnant by its
		// lower boundary relative to the middle.
		mid := Intersect(r.Range, target)
		var left, right []Range
		for _, piece := range Difference(r.Range, target) {
			if lowerBoundaryLess(piece, mid) {
				left = append(left, piece)
			} else {
				right = append(right, piece)
			}
		}

		for _, lr := range left {
			rem := existing.Clone()
			rem.Range = lr
			rem.Table = orDefault(rem.Table, t.Name)
			candidate = append(candidate, rem)
			report.Segments = append(report.Segments, rem)
			report.RemnantRows++
		}

		updated := existing.applyPayload(newPayload)
		updated.Range = mid
		updated.Table = orDefault(updated.Table, t.Name)
		candidate = append(candidate, updated)
		report.Segments = append(report.Segments, updated)
		report.UpdatedRows++

		for _, rr := range right {
			rem := existing.Clone()
			rem.Range = rr
			rem.Table = orDefault(rem.Table, t.Name)
			candidate = append(candidate, rem)
			report.Segments = append(report.Segments, rem)
			report.RemnantRows++
		}
	}

	if len(report.MatchedIndexes) == 0 {
		// Nothing matched: preserve the committed slice exactly.
		report.Rows = t.Rows
		return report, nil
	}

	if t.CoalesceAdjacent {
		candidate = MergeAdjacentRows(candidate, t.KeyColumns)
	} else {
		sortRowsByKey(candidate, t.KeyColumns)
	}
	report.Rows = candidate
	report.MergedRows = MergeAdjacentRows(candidate, t.KeyColumns)
	return report, nil
}

// UpsertPortion implements the alternative "replace the target span"
// semantics: overlapping same-key rows lose the target range via difference
// (left/right remnants only) and one fresh target row carrying newPayload is
// inserted. The result is validated and atomically committed.
func (t *Table) UpsertPortion(key any, targetRange any, newPayload any) error {
	target, err := CoerceRange(targetRange)
	if err != nil {
		return err
	}
	target = target.Normalize()
	selector := CoerceKey(key, t.KeyColumns)
	target = t.alignDomain(target)

	candidate := make([]Row, 0, len(t.Rows)+1)
	for _, existing := range t.Rows {
		r := existing.Clone()
		r.Range = r.Range.Normalize()
		rowKey := r.EffectiveKey(t.KeyColumns)
		if r.Range.IsEmpty() || !KeyMatches(rowKey, selector) || !r.Range.Overlaps(target) {
			candidate = append(candidate, r)
			continue
		}
		for _, piece := range Difference(r.Range, target) {
			rem := existing.Clone()
			rem.Range = piece
			candidate = append(candidate, rem)
		}
	}

	fresh := Row{Table: t.Name, Key: selector.Clone(), Range: target}
	fresh = fresh.applyPayload(newPayload)
	candidate = append(candidate, fresh)
	if t.CoalesceAdjacent {
		candidate = MergeAdjacentRows(candidate, t.KeyColumns)
	}
	if err := t.validateCandidate(candidate); err != nil {
		return err
	}
	t.Rows = candidate
	return nil
}

func (t *Table) validateCandidate(candidate []Row) error {
	ve := &ValidationError{}
	if t.RejectEmpty {
		ve.Empties = append(ve.Empties, CheckEmptyRanges(t.Name, candidate)...)
	}
	ve.Exclusion = append(ve.Exclusion, CheckExclusion(candidate, t.effectiveExclusion())...)

	if t.schema != nil {
		all := t.schema.combinedRows(t.Name, candidate)
		for _, c := range t.schema.Exclusions {
			ve.Exclusion = append(ve.Exclusion, CheckExclusion(all, c)...)
		}
		for _, fk := range t.schema.ForeignKeys {
			ve.Coverage = append(ve.Coverage, CheckForeignKeyCoverage(all, fk)...)
		}
	}
	return ve.OrNil()
}

// Validate checks the committed rows against emptiness, the table exclusion
// constraint and, when attached to a Schema, all schema constraints.
func (t *Table) Validate() error {
	return t.validateCandidate(t.Rows)
}

// Snapshot returns a copy of the rows for explicit transaction control.
func (t *Table) Snapshot() []Row {
	out := make([]Row, len(t.Rows))
	for i, r := range t.Rows {
		out[i] = r.Clone()
	}
	return out
}

// Restore replaces the table rows with a previously taken snapshot.
func (t *Table) Restore(rows []Row) {
	t.Rows = make([]Row, len(rows))
	for i, r := range rows {
		t.Rows[i] = r.Clone()
	}
}

// AllRanges returns the normalized ranges of all rows.
func (t *Table) AllRanges() []Range {
	out := make([]Range, 0, len(t.Rows))
	for _, r := range t.Rows {
		out = append(out, r.Range.Normalize())
	}
	return out
}

// RowsByKey returns rows whose key matches selector.
func (t *Table) RowsByKey(selector any) []Row {
	sel := CoerceKey(selector, t.KeyColumns)
	var out []Row
	for _, r := range t.Rows {
		if KeyMatches(r.EffectiveKey(t.KeyColumns), sel) {
			out = append(out, r)
		}
	}
	return out
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func sortRowsByKey(rows []Row, keyColumns []string) {
	sort.SliceStable(rows, func(i, j int) bool {
		ki := rows[i].EffectiveKey(keyColumns)
		kj := rows[j].EffectiveKey(keyColumns)
		if c := compareKeyTuples(ki, kj); c != 0 {
			return c < 0
		}
		return lowerBoundaryLess(rows[i].Range.Normalize(), rows[j].Range.Normalize())
	})
}

func compareKeyTuples(a, b Key) int {
	names := map[string]struct{}{}
	for n := range a {
		names[n] = struct{}{}
	}
	for n := range b {
		names[n] = struct{}{}
	}
	var sorted []string
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		av, ain := a[n]
		bv, bin := b[n]
		if !ain || !bin {
			if ain != bin {
				if ain {
					return 1
				}
				return -1
			}
			continue
		}
		if c := compareScalar(av, bv); c != 0 {
			return c
		}
	}
	return 0
}

func compareScalar(a, b any) int {
	af, aok := numericFloat(a)
	bf, bok := numericFloat(b)
	if aok && bok {
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		default:
			return 0
		}
	}
	as := formatAny(a)
	bs := formatAny(b)
	if as < bs {
		return -1
	}
	if as > bs {
		return 1
	}
	return 0
}

// CoerceRange accepts Range, *Range or a parseable string.
func CoerceRange(v any) (Range, error) {
	switch r := v.(type) {
	case Range:
		return r, nil
	case *Range:
		if r == nil {
			return Range{}, fmt.Errorf("temporal: nil range")
		}
		return *r, nil
	case string:
		return ParseRange(r)
	case fmt.Stringer:
		return ParseRange(r.String())
	default:
		return Range{}, fmt.Errorf("temporal: cannot use %T as Range", v)
	}
}

// alignDomain reinterprets target in the table's domain when they differ.
// The domain is inferred from existing rows; this makes zero-value Range
// literals (whose Kind defaults to Continuous) behave correctly against a
// discrete table built through struct literals.
func (t *Table) alignDomain(target Range) Range {
	domain := t.dominantKind()
	if domain == nil || *domain == target.Kind {
		return target
	}
	converted := target
	converted.Kind = *domain
	return converted.Normalize()
}

func (t *Table) dominantKind() *Kind {
	for _, r := range t.Rows {
		k := r.Range.Kind
		return &k
	}
	return nil
}
