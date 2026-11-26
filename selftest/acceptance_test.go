// Package acceptance_test contains end-to-end acceptance coverage for the
// temporal range algebra and the constraint/rollback engine. It imports the
// module exactly as an external relational executor would.
package selftest_test

import (
	"errors"
	"testing"

	temporal "prescreen.local/ps-db7888bb15"
)

func ir(lo, hi int64) temporal.Range { return temporal.NewIntRange(lo, hi) }
func tr(lo, hi int64, li, ui bool) temporal.Range {
	return temporal.NewTimeRange(lo, hi, li, ui)
}

// 1. Discrete normalization, including the (3,4) -> empty trap.
func TestAccDiscreteNormalization(t *testing.T) {
	cases := []struct {
		name  string
		in    temporal.Range
		want  string
		empty bool
	}{
		{"open-open (3,4) is empty", temporal.NewDiscreteRange(3, 4, false, false), "empty", true},
		{"closed-open [3,4) is singleton 3", temporal.NewDiscreteRange(3, 4, true, false), "[3,4)", false},
		{"closed-closed [3,3] is singleton 3 -> [3,4)", temporal.NewDiscreteRange(3, 3, true, true), "[3,4)", false},
		{"open-open (3,5) -> [4,5)", temporal.NewDiscreteRange(3, 5, false, false), "[4,5)", false},
		{"open-closed (3,4] -> [4,5)", temporal.NewDiscreteRange(3, 4, false, true), "[4,5)", false},
		{"closed-closed [3,4] -> [3,5)", temporal.NewDiscreteRange(3, 4, true, true), "[3,5)", false},
		{"degenerate [3,3) empty", temporal.NewDiscreteRange(3, 3, true, false), "empty", true},
		{"degenerate (3,3) empty", temporal.NewDiscreteRange(3, 3, false, false), "empty", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.String(); got != tc.want {
				t.Fatalf("normalize = %s, want %s", got, tc.want)
			}
			if tc.in.IsEmpty() != tc.empty {
				t.Fatalf("IsEmpty = %v, want %v", tc.in.IsEmpty(), tc.empty)
			}
		})
	}

	// (3,4) contains no integer and therefore overlaps nothing.
	empty := temporal.MustParseIntRange("(3,4)")
	if !empty.IsEmpty() {
		t.Fatal("(3,4) must normalize to empty for discrete integers")
	}
	if empty.Overlaps(ir(3, 5)) || empty.Overlaps(temporal.Empty(temporal.Discrete)) {
		t.Fatal("empty discrete range must not overlap anything")
	}

	// Singleton membership.
	singleton := temporal.IntPoint(7) // [7,8)
	for _, x := range []int64{7} {
		if !singleton.Contains(x) {
			t.Fatalf("IntPoint(7) should contain 7")
		}
	}
	if singleton.Contains(8) || singleton.Contains(6) {
		t.Fatal("IntPoint(7) must contain exactly 7")
	}
}

// 2. Continuous single-point boundary semantics.
func TestAccContinuousPointBoundaries(t *testing.T) {
	const t0 int64 = 1_700_000_000_000_000 // microseconds

	if !temporal.Point(t0).Equals(tr(t0, t0, true, true)) {
		t.Fatal("Point must equal [t,t]")
	}
	for name, r := range map[string]temporal.Range{
		"(t,t]": tr(t0, t0, false, true),
		"[t,t)": tr(t0, t0, true, false),
		"(t,t)": tr(t0, t0, false, false),
	} {
		if !r.IsEmpty() {
			t.Fatalf("%s must be empty", name)
		}
	}

	point := temporal.Point(t0)
	closedOpenNext := tr(t0, t0+1, true, false) // [t, t+1)
	openNext := tr(t0, t0+1, false, false)      // (t, t+1)
	openPrevClosed := tr(t0-1, t0, false, true) // (t-1, t]
	openPrevOpen := tr(t0-1, t0, false, false)  // (t-1, t)

	if !point.Overlaps(closedOpenNext) {
		t.Fatal("[t,t] must overlap [t,t+1)")
	}
	if point.Overlaps(openNext) {
		t.Fatal("[t,t] must NOT overlap (t,t+1)")
	}
	if !point.Overlaps(openPrevClosed) {
		t.Fatal("[t,t] must overlap (t-1,t] at point t")
	}
	if point.Overlaps(openPrevOpen) {
		t.Fatal("[t,t] must NOT overlap (t-1,t)")
	}

	// Adjacency boundary variants.
	if !temporal.Adjacent(tr(0, 1, true, false), tr(1, 2, true, false)) {
		t.Fatal("[0,1) and [1,2) must be adjacent")
	}
	if !temporal.Adjacent(tr(0, 1, true, true), tr(1, 2, false, true)) {
		t.Fatal("[0,1] and (1,2] must be adjacent (point 1 covered by left)")
	}
	if temporal.Adjacent(tr(0, 1, true, false), tr(1, 2, false, true)) {
		t.Fatal("[0,1) and (1,2] leave 1 uncovered: not adjacent")
	}
	if temporal.Adjacent(tr(0, 1, true, true), tr(1, 2, true, true)) {
		t.Fatal("[0,1] and [1,2] overlap at 1: not merely adjacent")
	}
}

// 3. Intersection / union / difference algebra on both domains.
func TestAccAlgebra(t *testing.T) {
	// Discrete difference producing two pieces.
	parts := temporal.Difference(ir(0, 10), ir(3, 7))
	assertRanges(t, parts, "[0,3)", "[7,10)")

	// Interior target on a bounded row: only a left remnant.
	parts = temporal.Difference(ir(0, 7), ir(5, 10))
	assertRanges(t, parts, "[0,5)")

	// Unbounded universe minus interior interval -> two unbounded half-lines.
	all := temporal.AllRange(temporal.Discrete)
	parts = temporal.Difference(all, ir(5, 10))
	if len(parts) != 2 {
		t.Fatalf("want 2 unbounded pieces, got %v", parts)
	}
	if parts[0].String() != "(-inf,5)" || parts[1].String() != "[10,+inf)" {
		t.Fatalf("unbounded split wrong: %v", parts)
	}

	// Interior interval minus universe -> nothing.
	if d := temporal.Difference(ir(5, 10), temporal.AllRange(temporal.Discrete)); len(d) != 0 {
		t.Fatalf("inner - all must be empty, got %v", d)
	}

	// Union coalesces adjacent and overlapping, keeps disjoint apart.
	assertRanges(t, temporal.Union(ir(0, 5), ir(5, 10)), "[0,10)")
	assertRanges(t, temporal.Union(ir(0, 5), ir(6, 10)), "[0,5)", "[6,10)")
	assertRanges(t, temporal.Union(ir(0, 6), ir(4, 10)), "[0,10)")

	// Continuous open-boundary difference flips inclusion correctly.
	cp := temporal.Difference(tr(0, 5, true, false), tr(5, 10, true, false))
	assertRanges(t, cp, "[0,5)")
	cp = temporal.Difference(tr(0, 5, true, true), tr(5, 10, false, false))
	// [0,5] - (5,10): boundary 5 belongs to left, so unchanged [0,5].
	assertRanges(t, cp, "[0,5]")

	// UnionAll merges a chain of adjacent parent ranges.
	merged := temporal.UnionAll([]temporal.Range{ir(50, 100), ir(0, 50), ir(200, 300), ir(100, 200)})
	assertRanges(t, merged, "[0,300)")
}

// 4. Exclusion constraint: multi-key equality + overlap; empties exempt.
func TestAccExclusionConstraint(t *testing.T) {
	rows := []temporal.Row{
		{ID: "a", Key: temporal.Key{"tenant": 1}, Range: ir(0, 10)},
		{ID: "b", Key: temporal.Key{"tenant": 1}, Range: ir(10, 20)}, // adjacent allowed
		{ID: "c", Key: temporal.Key{"tenant": 2}, Range: ir(5, 6)},   // different key allowed
		{ID: "d", Key: temporal.Key{"tenant": 1}, Range: temporal.Empty(temporal.Discrete)},
	}
	err := temporal.ValidateExclusion(rows, temporal.ExclusionConstraint{
		Name:            "tenant_period_excl",
		EqualityColumns: []string{"tenant"},
	})
	if err != nil {
		t.Fatalf("adjacent / other-key / empty rows must pass: %v", err)
	}

	bad := append(rows, temporal.Row{ID: "e", Key: temporal.Key{"tenant": 1}, Range: ir(9, 11)})
	err = temporal.ValidateExclusion(bad, temporal.ExclusionConstraint{EqualityColumns: []string{"tenant"}})
	if err == nil || !errors.Is(err, temporal.ErrOverlap) {
		t.Fatalf("expected ErrOverlap, got %v", err)
	}

	// Continuous point vs open/closed neighbour through the checker.
	pointRows := []temporal.Row{
		{ID: "p", Key: temporal.Key{"k": "x"}, Range: temporal.Point(100)},
		{ID: "q", Key: temporal.Key{"k": "x"}, Range: tr(100, 101, false, false)}, // (100,101)
	}
	if err := temporal.ValidateExclusion(pointRows, temporal.ExclusionConstraint{}); err != nil {
		t.Fatalf("point [100,100] must not overlap (100,101): %v", err)
	}
	pointRows[1].Range = tr(100, 101, true, false) // [100,101)
	if err := temporal.ValidateExclusion(pointRows, temporal.ExclusionConstraint{}); !errors.Is(err, temporal.ErrOverlap) {
		t.Fatalf("point [100,100] must overlap [100,101), got %v", err)
	}
}

//  5. UpdatePortion spanning multiple old records, adjacent merge and
//     preservation of unaffected fields.
func TestAccUpdatePortionSpansRowsAndMerges(t *testing.T) {
	tbl := temporal.NewTable("cfg", "tenant")
	tbl.CoalesceAdjacent = true
	mk := func(id, note string, lo, hi int64) temporal.Row {
		return temporal.Row{
			ID:      id,
			Key:     temporal.Key{"tenant": 7},
			Range:   ir(lo, hi),
			Payload: "old-" + id,
			Fields:  temporal.Fields{"tenant": 7, "note": note, "rev": 1},
		}
	}
	if err := tbl.InsertRows(mk("r1", "first", 0, 5), mk("r2", "second", 5, 10)); err != nil {
		t.Fatal(err)
	}

	err := tbl.UpdatePortion(temporal.Key{"tenant": 7}, ir(2, 8), "NEW")
	if err != nil {
		t.Fatalf("UpdatePortion: %v", err)
	}

	// Expected: [0,2) old-r1, [2,8) NEW (two adjacent middles merged),
	// [8,10) old-r2.
	if got := rangesOf(tbl.Rows); len(got) != 3 || got[0] != "[0,2)" || got[1] != "[2,8)" || got[2] != "[8,10)" {
		t.Fatalf("unexpected merged rows: %v", got)
	}

	// Unaffected fields survive on remnants.
	left := findByRange(tbl.Rows, "[0,2)")
	if left == nil || left.Fields["note"] != "first" || left.Payload != "old-r1" {
		t.Fatalf("left remnant did not preserve fields/payload: %+v", left)
	}
	right := findByRange(tbl.Rows, "[8,10)")
	if right == nil || right.Fields["note"] != "second" {
		t.Fatalf("right remnant did not preserve fields: %+v", right)
	}
	mid := findByRange(tbl.Rows, "[2,8)")
	if mid == nil || mid.Payload != "NEW" {
		t.Fatalf("merged middle missing new payload: %+v", mid)
	}

	// Raw report still exposes the exact four split segments.
	rep := tbl.LastReport
	if rep == nil || rep.UpdatedRows != 2 || rep.RemnantRows != 2 {
		t.Fatalf("report counts wrong: %+v", rep)
	}

	// Result set satisfies the exclusion constraint.
	if err := tbl.Validate(); err != nil {
		t.Fatalf("post-update table invalid: %v", err)
	}
}

// 6. Unbounded target portion splits an unbounded row into two half-lines.
func TestAccUpdatePortionUnboundedSplit(t *testing.T) {
	tbl := temporal.NewTable("u", "tenant")
	tbl.CoalesceAdjacent = false
	if err := tbl.Insert(temporal.Row{
		ID: "big", Key: temporal.Key{"tenant": 1}, Range: temporal.AllRange(temporal.Discrete), Payload: "a",
	}); err != nil {
		t.Fatal(err)
	}
	if err := tbl.UpdatePortion(temporal.Key{"tenant": 1}, ir(5, 10), "b"); err != nil {
		t.Fatal(err)
	}
	got := rangesOf(tbl.Rows)
	// (-inf,5) a, [5,10) b, [10,+inf) a
	want := []string{"(-inf,5)", "[5,10)", "[10,+inf)"}
	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %s, want %s (all: %v)", i, got[i], want[i], got)
		}
	}
	if tbl.Rows[0].Payload != "a" || tbl.Rows[1].Payload != "b" || tbl.Rows[2].Payload != "a" {
		t.Fatalf("payload routing wrong: %v", payloadsOf(tbl.Rows))
	}
}

// 7. Empty remnants are never produced, even for boundary-aligned updates.
func TestAccNoEmptyRemnants(t *testing.T) {
	tbl := temporal.NewTable("e", "k")
	if err := tbl.Insert(temporal.Row{ID: "r", Key: temporal.Key{"k": 1}, Range: ir(3, 7), Payload: "a"}); err != nil {
		t.Fatal(err)
	}
	// Target exactly equals the row: no remnants, one middle.
	if err := tbl.UpdatePortion(temporal.Key{"k": 1}, ir(3, 7), "b"); err != nil {
		t.Fatal(err)
	}
	if len(tbl.Rows) != 1 || tbl.Rows[0].Range.String() != "[3,7)" {
		t.Fatalf("equal-span update should yield one row, got %v", rangesOf(tbl.Rows))
	}
	for _, r := range tbl.Rows {
		if r.Range.IsEmpty() {
			t.Fatal("empty remnant stored")
		}
	}

	// Inserting an empty range is rejected.
	err := tbl.Insert(temporal.Row{Key: temporal.Key{"k": 1}, Range: temporal.MustParseIntRange("(3,4)")})
	if !errors.Is(err, temporal.ErrEmptyRange) {
		t.Fatalf("expected ErrEmptyRange, got %v", err)
	}
}

//  8. FK temporal coverage by adjacent/overlapping parents, including the
//     rollback when an update breaks coverage.
func TestAccFKCoverageAndRollback(t *testing.T) {
	schema, _, child := newFKSchema(t)

	// Baseline: child [0,100) covered by two adjacent parents.
	if err := schema.Validate(); err != nil {
		t.Fatalf("adjacent parents must cover child: %v", err)
	}
	if !temporal.CoveredBy(ir(0, 100), []temporal.Range{ir(0, 50), ir(50, 100)}) {
		t.Fatal("CoveredBy must merge adjacent parents")
	}

	before := child.Snapshot()

	// Move the [40,60) middle to parent id 20, which has no covering row.
	err := child.UpdatePortion(
		temporal.Key{"tenant": 1, "pid": 10},
		ir(40, 60),
		temporal.Fields{"pid": 20},
	)
	if err == nil {
		t.Fatal("expected foreign-key coverage violation")
	}
	if !errors.Is(err, temporal.ErrCoverage) {
		t.Fatalf("error should wrap ErrCoverage, got %v", err)
	}

	// Rollback: committed rows are byte-for-byte the pre-call snapshot.
	if len(child.Rows) != len(before) {
		t.Fatalf("rollback failed: got %d rows, want %d", len(child.Rows), len(before))
	}
	for i := range before {
		if !before[i].Range.Equals(child.Rows[i].Range) {
			t.Fatalf("row %d changed after failed update: %s vs %s", i, child.Rows[i].Range, before[i].Range)
		}
	}
	if err := schema.Validate(); err != nil {
		t.Fatalf("rolled-back state must validate: %v", err)
	}

	// A valid portion update commits and keeps coverage.
	if err := child.UpdatePortion(temporal.Key{"tenant": 1, "pid": 10}, ir(10, 20), "ok"); err != nil {
		t.Fatalf("covered update should commit: %v", err)
	}
	if err := schema.Validate(); err != nil {
		t.Fatalf("committed split should stay covered: %v", err)
	}
}

// 9. An update that would create same-key overlap is rejected and rolled back.
func TestAccExclusionRollback(t *testing.T) {
	tbl := temporal.NewTable("x", "tenant")
	if err := tbl.InsertRows(
		temporal.Row{ID: "a", Key: temporal.Key{"tenant": 1}, Range: ir(0, 10), Payload: "a"},
		temporal.Row{ID: "b", Key: temporal.Key{"tenant": 1}, Range: ir(20, 30), Payload: "b"},
		temporal.Row{ID: "c", Key: temporal.Key{"tenant": 2}, Range: ir(5, 8), Payload: "c"},
	); err != nil {
		t.Fatal(err)
	}
	before := tbl.Snapshot()

	// Moving the updated middle to tenant 2 creates an overlap with c.
	err := tbl.UpdatePortion(
		temporal.Key{"tenant": 1},
		ir(3, 9),
		temporal.Fields{"tenant": 2},
	)
	if !errors.Is(err, temporal.ErrOverlap) {
		t.Fatalf("portion creating overlap must roll back with ErrOverlap, got %v", err)
	}
	if len(tbl.Rows) != len(before) {
		t.Fatalf("failed portion must not mutate rows: got %d want %d", len(tbl.Rows), len(before))
	}
	for i := range before {
		if !before[i].Range.Equals(tbl.Rows[i].Range) {
			t.Fatalf("row %d changed after rollback", i)
		}
	}

	// Direct overlapping inserts are blocked as well.
	err = tbl.Insert(temporal.Row{ID: "d", Key: temporal.Key{"tenant": 1}, Range: ir(5, 6)})
	if !errors.Is(err, temporal.ErrOverlap) {
		t.Fatalf("overlapping insert must be blocked, got %v", err)
	}
	if len(tbl.Rows) != len(before) {
		t.Fatal("blocked insert must not mutate the table")
	}
}

// 10. Parsing documented textual forms.
func TestAccParse(t *testing.T) {
	tests := []struct {
		expr string
		kind temporal.Kind
		want string
	}{
		{"[3,4)", temporal.Discrete, "[3,4)"},
		{"(3,4)", temporal.Discrete, "empty"},
		{"[3,4]", temporal.Discrete, "[3,5)"},
		{"(-inf, 5)", temporal.Discrete, "(-inf,5)"},
		{"[10,+inf)", temporal.Discrete, "[10,+inf)"},
		{"empty", temporal.Discrete, "empty"},
		{"[100,101)", temporal.Continuous, "[100,101)"},
		{"(100,100]", temporal.Continuous, "empty"},
		{"[100,100]", temporal.Continuous, "[100,100]"},
	}
	for _, tc := range tests {
		r, err := temporal.ParseRange(tc.expr, tc.kind)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", tc.expr, err)
		}
		if r.String() != tc.want {
			t.Fatalf("ParseRange(%q) = %s, want %s", tc.expr, r.String(), tc.want)
		}
	}
}

// ---- helpers ----

func newFKSchema(t *testing.T) (*temporal.Schema, *temporal.Table, *temporal.Table) {
	t.Helper()
	s := temporal.NewSchema()
	parent := s.CreateTable("parent", "tenant", "id")
	child := s.CreateTable("child", "tenant", "pid")
	s.AddForeignKey(temporal.ForeignKeyCoverage{
		Name:          "child_pid_fkey",
		ChildTable:    "child",
		ChildColumns:  []string{"tenant", "pid"},
		ParentTable:   "parent",
		ParentColumns: []string{"tenant", "id"},
	})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	must(parent.Insert(temporal.Row{ID: "p1", Key: temporal.Key{"tenant": 1, "id": 10}, Range: ir(0, 50)}))
	must(parent.Insert(temporal.Row{ID: "p2", Key: temporal.Key{"tenant": 1, "id": 10}, Range: ir(50, 100)}))
	must(child.Insert(temporal.Row{ID: "c1", Key: temporal.Key{"tenant": 1, "pid": 10}, Range: ir(0, 100), Payload: "c"}))
	return s, parent, child
}

func assertRanges(t *testing.T, rs []temporal.Range, want ...string) {
	t.Helper()
	got := make([]string, len(rs))
	for i, r := range rs {
		got[i] = r.String()
	}
	if len(got) != len(want) {
		t.Fatalf("ranges = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("range %d = %q, want %q (all %v)", i, got[i], want[i], got)
		}
	}
}

func rangesOf(rows []temporal.Row) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Range.String())
	}
	return out
}

func payloadsOf(rows []temporal.Row) []any {
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Payload)
	}
	return out
}

func findByRange(rows []temporal.Row, s string) *temporal.Row {
	for i := range rows {
		if rows[i].Range.String() == s {
			return &rows[i]
		}
	}
	return nil
}
