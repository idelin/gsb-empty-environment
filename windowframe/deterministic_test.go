package windowframe

import "testing"

func iv(v int64) Value { return Int(v) }
func nv() Value        { return Null() }

func assertValue(t *testing.T, label string, got, want Value) {
	t.Helper()
	if !Equal(got, want) {
		t.Fatalf("%s: got %s want %s", label, valueString(got), valueString(want))
	}
}

func assertInt64s(t *testing.T, label string, got []Value, want ...int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: length got %d want %d", label, len(got), len(want))
	}
	for i := range want {
		assertValue(t, label, got[i], iv(want[i]))
	}
}

func assertNulls(t *testing.T, label string, got []Value) {
	t.Helper()
	for i, v := range got {
		if !v.IsNull() {
			t.Fatalf("%s position %d: got %s want NULL", label, i, valueString(v))
		}
	}
}

// RANGE with a NULL current row can only contain the NULL peer group, even
// when numeric value offsets would otherwise reach non-null rows.
func TestRangeNullCurrentRowStaysInsideNullPeerGroup(t *testing.T) {
	rows := [][]Value{
		{nv()},
		{iv(2)},
		{nv()},
		{iv(1)},
		{iv(7)},
	}
	spec := WindowSpec{
		Order: []OrderSpec{{Column: 0, Direction: Asc, Nulls: NullsFirst}},
		Frame: &FrameSpec{
			Mode:  RangeFrame,
			Start: Preceding(iv(5)),
			End:   boundPtr(Following(iv(5))),
		},
	}
	got, err := Evaluate(rows, spec, FuncExpr{Func: FuncSum, Column: 0})
	if err != nil {
		t.Fatal(err)
	}
	// Original rows: NULL, 2, NULL, 1, 7. Sorted order (NULLS FIRST):
	// NULL, NULL, 1, 2, 7. NULL rows only ever see the two NULL peers.
	assertNulls(t, "sum/null-rows", []Value{got[0], got[2]})
	assertValue(t, "sum/2", got[1], iv(10)) // frame [-3..7] intersects 1,2,7
	assertValue(t, "sum/1", got[3], iv(3))  // frame [-4..6] intersects 1,2
	assertValue(t, "sum/7", got[4], iv(9))  // frame [2..12] intersects 2,7 -> 9

	count, err := Evaluate(rows, spec, FuncExpr{Func: FuncCount, CountAll: true})
	if err != nil {
		t.Fatal(err)
	}
	assertValue(t, "count/null-row-0", count[0], iv(2))
	assertValue(t, "count/null-row-2", count[2], iv(2))
	assertValue(t, "count/1", count[3], iv(2))
	assertValue(t, "count/7", count[4], iv(2))
}

// EXCLUDE GROUP on a single-row frame empties it: COUNT -> 0, everything else
// -> NULL, including FIRST/LAST/NTH VALUE.
func TestExcludeGroupEmptyFrame(t *testing.T) {
	rows := [][]Value{{iv(1)}, {iv(2)}, {iv(3)}}
	spec := WindowSpec{
		Order: []OrderSpec{{Column: 0}},
		Frame: &FrameSpec{
			Mode:      RowsFrame,
			Start:     CurrentRow(),
			Exclusion: ExcludeGroup,
		},
	}
	count, err := Evaluate(rows, spec, FuncExpr{Func: FuncCount, CountAll: true})
	if err != nil {
		t.Fatal(err)
	}
	assertInt64s(t, "count", count, 0, 0, 0)

	for _, kind := range []FuncKind{FuncSum, FuncAvg, FuncMin, FuncMax, FuncFirstValue, FuncLastValue} {
		out, err := Evaluate(rows, spec, FuncExpr{Func: kind, Column: 0})
		if err != nil {
			t.Fatal(err)
		}
		assertNulls(t, kind.String()+" empty frame", out)
	}
	nth, err := Evaluate(rows, spec, FuncExpr{Func: FuncNthValue, Column: 0, N: 1})
	if err != nil {
		t.Fatal(err)
	}
	assertNulls(t, "nth empty frame", nth)
}

// EXCLUDE TIES removes other peers but keeps the current row; EXCLUDE GROUP
// removes the current row too.
func TestExcludeTiesVsGroup(t *testing.T) {
	rows := [][]Value{{iv(5)}, {iv(5)}, {iv(9)}}
	base := func(ex Exclusion) WindowSpec {
		return WindowSpec{
			Order: []OrderSpec{{Column: 0}},
			Frame: &FrameSpec{
				Mode:      RowsFrame,
				Start:     UnboundedPreceding(),
				End:       boundPtr(UnboundedFollowing()),
				Exclusion: ex,
			},
		}
	}
	ties, err := Evaluate(rows, base(ExcludeTies), FuncExpr{Func: FuncCount, CountAll: true})
	if err != nil {
		t.Fatal(err)
	}
	assertInt64s(t, "ties", ties, 2, 2, 3) // each 5 keeps itself plus the 9; the lone 9 keeps all rows

	group, err := Evaluate(rows, base(ExcludeGroup), FuncExpr{Func: FuncCount, CountAll: true})
	if err != nil {
		t.Fatal(err)
	}
	assertInt64s(t, "group", group, 1, 1, 2) // 5 rows keep only the 9; the 9 keeps both 5s

	cur, err := Evaluate(rows, base(ExcludeCurrentRow), FuncExpr{Func: FuncCount, CountAll: true})
	if err != nil {
		t.Fatal(err)
	}
	assertInt64s(t, "current row", cur, 2, 2, 2)
}

// GROUPS treats all NULLs as one peer group and honours NULLS FIRST/LAST.
func TestGroupsNullPeerGroup(t *testing.T) {
	rows := [][]Value{{nv()}, {iv(1)}, {nv()}, {iv(1)}, {iv(2)}}
	specFirst := WindowSpec{
		Order: []OrderSpec{{Column: 0, Nulls: NullsFirst}},
		Frame: &FrameSpec{
			Mode:  GroupsFrame,
			Start: CurrentRow(),
			End:   boundPtr(Following(iv(1))),
		},
	}
	first, err := Evaluate(rows, specFirst, FuncExpr{Func: FuncCount, CountAll: true})
	if err != nil {
		t.Fatal(err)
	}
	// Sorted groups: [NULL,NULL],[1,1],[2]. NULL rows see groups 0 and 1.
	assertValue(t, "first/NULL-0", first[0], iv(4))
	assertValue(t, "first/NULL-2", first[2], iv(4))
	assertValue(t, "first/1-1", first[1], iv(3))
	assertValue(t, "first/1-3", first[3], iv(3))
	assertValue(t, "first/2", first[4], iv(1))

	specLast := specFirst
	specLast.Order[0].Nulls = NullsLast
	last, err := Evaluate(rows, specLast, FuncExpr{Func: FuncCount, CountAll: true})
	if err != nil {
		t.Fatal(err)
	}
	// Sorted groups: [1,1],[2],[NULL,NULL]. NULL rows see only the NULL group.
	assertValue(t, "last/NULL-0", last[0], iv(2))
	assertValue(t, "last/NULL-2", last[2], iv(2))
	assertValue(t, "last/1-1", last[1], iv(3))
	assertValue(t, "last/1-3", last[3], iv(3))
	assertValue(t, "last/2", last[4], iv(3))
}

func TestDefaultFrames(t *testing.T) {
	rows := [][]Value{{iv(3)}, {iv(1)}, {iv(2)}}

	ordered := WindowSpec{Order: []OrderSpec{{Column: 0}}}
	sum, err := Evaluate(rows, ordered, FuncExpr{Func: FuncSum, Column: 0})
	if err != nil {
		t.Fatal(err)
	}
	// Sorted 1,2,3: running totals mapped back to original row positions.
	assertValue(t, "ordered/3", sum[0], iv(6))
	assertValue(t, "ordered/1", sum[1], iv(1))
	assertValue(t, "ordered/2", sum[2], iv(3))

	whole, err := Evaluate(rows, WindowSpec{}, FuncExpr{Func: FuncSum, Column: 0})
	if err != nil {
		t.Fatal(err)
	}
	assertInt64s(t, "whole partition", whole, 6, 6, 6)
}

func TestInvertedRowsFrameIsEmptyNotReordered(t *testing.T) {
	rows := [][]Value{{iv(1)}, {iv(2)}, {iv(3)}, {iv(4)}}
	spec := WindowSpec{
		Order: []OrderSpec{{Column: 0}},
		Frame: &FrameSpec{
			Mode:  RowsFrame,
			Start: Following(iv(2)),
			End:   boundPtr(Following(iv(1))),
		},
	}
	count, err := Evaluate(rows, spec, FuncExpr{Func: FuncCount, CountAll: true})
	if err != nil {
		t.Fatal(err)
	}
	assertInt64s(t, "inverted count", count, 0, 0, 0, 0)
	sum, err := Evaluate(rows, spec, FuncExpr{Func: FuncSum, Column: 0})
	if err != nil {
		t.Fatal(err)
	}
	assertNulls(t, "inverted sum", sum)
}

func TestRankingAndNtile(t *testing.T) {
	rows := [][]Value{{iv(5)}, {iv(5)}, {iv(9)}, {iv(9)}, {iv(9)}, {iv(12)}}
	spec := WindowSpec{Order: []OrderSpec{{Column: 0}}}

	rank, err := Evaluate(rows, spec, FuncExpr{Func: FuncRank})
	if err != nil {
		t.Fatal(err)
	}
	assertInt64s(t, "rank", rank, 1, 1, 3, 3, 3, 6)

	dense, err := Evaluate(rows, spec, FuncExpr{Func: FuncDenseRank})
	if err != nil {
		t.Fatal(err)
	}
	assertInt64s(t, "dense rank", dense, 1, 1, 2, 2, 2, 3)

	rowNum, err := Evaluate(rows, spec, FuncExpr{Func: FuncRowNumber})
	if err != nil {
		t.Fatal(err)
	}
	assertInt64s(t, "row number", rowNum, 1, 2, 3, 4, 5, 6)

	ntile, err := Evaluate(rows, spec, FuncExpr{Func: FuncNtile, N: 4})
	if err != nil {
		t.Fatal(err)
	}
	assertInt64s(t, "ntile", ntile, 1, 1, 2, 2, 3, 4)
}

func TestRangeDescendingOffsets(t *testing.T) {
	rows := [][]Value{{iv(1)}, {iv(3)}, {iv(5)}, {iv(8)}}
	spec := WindowSpec{
		Order: []OrderSpec{{Column: 0, Direction: Desc}},
		Frame: &FrameSpec{
			Mode:  RangeFrame,
			Start: Preceding(iv(2)),
			End:   boundPtr(CurrentRow()),
		},
	}
	sum, err := Evaluate(rows, spec, FuncExpr{Func: FuncSum, Column: 0})
	if err != nil {
		t.Fatal(err)
	}
	// RANGE PRECEDING 2 includes every key in [K-2, K]; results are indexed by
	// the original rows (keys 1, 3, 5, 8), independent of the DESC ordering.
	assertValue(t, "key 1", sum[0], iv(17)) // {1,3,5,8} all reach 1 via the window
	assertValue(t, "key 3", sum[1], iv(16)) // {3,5,8}
	assertValue(t, "key 5", sum[2], iv(13)) // {5,8}
	assertValue(t, "key 8", sum[3], iv(8))  // {8}
}

func TestPartitionNullsGroupTogether(t *testing.T) {
	rows := [][]Value{
		{nv(), iv(1)},
		{iv(7), iv(2)},
		{nv(), iv(4)},
	}
	spec := WindowSpec{PartitionColumns: []int{0}}
	sum, err := Evaluate(rows, spec, FuncExpr{Func: FuncSum, Column: 1})
	if err != nil {
		t.Fatal(err)
	}
	assertValue(t, "null partition 0", sum[0], iv(5))
	assertValue(t, "null partition 2", sum[2], iv(5))
	assertValue(t, "value partition", sum[1], iv(2))
}

func TestLagLeadAndIgnoreNulls(t *testing.T) {
	rows := [][]Value{{iv(1)}, {nv()}, {iv(3)}, {iv(4)}}
	// NULLS FIRST makes the sorted order deterministic: 1, NULL, 3, 4.
	spec := WindowSpec{Order: []OrderSpec{{Column: 0, Nulls: NullsFirst}}}

	lag, err := Evaluate(rows, spec, FuncExpr{Func: FuncLag, Column: 0, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	assertValue(t, "lag 0", lag[0], nv())
	assertValue(t, "lag NULL row", lag[1], nv()) // NULL sorts first, so it has no predecessor
	assertValue(t, "lag 3 row", lag[2], iv(1))
	assertValue(t, "lag 4 row", lag[3], iv(3))

	leadDefault, err := Evaluate(rows, spec, FuncExpr{
		Func: FuncLead, Column: 0, Offset: 1, HasDefault: true, Default: iv(-1),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertValue(t, "lead default last", leadDefault[3], iv(-1)) // last sorted row (4) has no successor
	assertValue(t, "lead NULL over 1", leadDefault[1], iv(1))   // NULL's successor is the 1
	assertValue(t, "lead 1 over 3", leadDefault[0], iv(3))

	lagIgnore, err := Evaluate(rows, spec, FuncExpr{Func: FuncLag, Column: 0, Offset: 1, IgnoreNulls: true})
	if err != nil {
		t.Fatal(err)
	}
	// The 3 row skips the NULL and finds 1; the 4 row finds the prior non-null 3.
	assertValue(t, "lag ignore 3 row", lagIgnore[2], iv(1))
	assertValue(t, "lag ignore 4 row", lagIgnore[3], iv(3))
	assertValue(t, "lag ignore NULL row", lagIgnore[1], nv())
}

func TestValidationErrors(t *testing.T) {
	rows := [][]Value{{iv(1)}, {iv(2)}}

	if _, err := Evaluate(rows, WindowSpec{}, FuncExpr{Func: FuncSum, Column: 9}); err == nil {
		t.Fatal("expected column index error")
	}

	negativeFrame := WindowSpec{
		Order: []OrderSpec{{Column: 0}},
		Frame: &FrameSpec{Mode: RowsFrame, Start: Preceding(iv(-1))},
	}
	if _, err := Evaluate(rows, negativeFrame, FuncExpr{Func: FuncSum, Column: 0}); err == nil {
		t.Fatal("expected negative offset error")
	}

	groupsNoOrder := WindowSpec{Frame: &FrameSpec{Mode: GroupsFrame, Start: CurrentRow()}}
	if _, err := Evaluate(rows, groupsNoOrder, FuncExpr{Func: FuncCount, CountAll: true}); err == nil {
		t.Fatal("expected GROUPS without ORDER BY error")
	}

	rangeTwoOrders := WindowSpec{
		Order: []OrderSpec{{Column: 0}, {Column: 0}},
		Frame: &FrameSpec{Mode: RangeFrame, Start: Preceding(iv(1))},
	}
	if _, err := Evaluate(rows, rangeTwoOrders, FuncExpr{Func: FuncSum, Column: 0}); err == nil {
		t.Fatal("expected RANGE value offset with two ORDER BY columns error")
	}

	if _, err := Evaluate(rows, WindowSpec{}, FuncExpr{Func: FuncNtile, N: 0}); err == nil {
		t.Fatal("expected NTILE zero error")
	}
	if _, err := Evaluate(rows, WindowSpec{}, FuncExpr{Func: FuncNthValue, Column: 0, N: 0}); err == nil {
		t.Fatal("expected NTH_VALUE zero error")
	}

	floatOffset := WindowSpec{
		Order: []OrderSpec{{Column: 0}},
		Frame: &FrameSpec{Mode: GroupsFrame, Start: Preceding(Float(1.5))},
	}
	if _, err := Evaluate(rows, floatOffset, FuncExpr{Func: FuncCount, CountAll: true}); err == nil {
		t.Fatal("expected non-integer GROUPS offset error")
	}
}

func TestEmptyInput(t *testing.T) {
	out, err := Evaluate(nil, WindowSpec{}, FuncExpr{Func: FuncCount, CountAll: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty output, got %v", out)
	}
}
