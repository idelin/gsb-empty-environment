package grouping

import "testing"

var emptyAggs = []Aggregate{
	{Name: "cnt", Func: Count},
	{Name: "cntx", Func: Count, Column: "x"},
	{Name: "s", Func: Sum, Column: "x"},
	{Name: "av", Func: Avg, Column: "x"},
	{Name: "mn", Func: Min, Column: "x"},
	{Name: "mx", Func: Max, Column: "x"},
}

func TestEmptyTableGrandTotal(t *testing.T) {
	// GROUP BY () on 0 rows: exactly 1 row, COUNT=0, the rest NULL,
	// GROUPING all ones.
	res := Execute(nil, Query{
		Aggregates: emptyAggs,
		Grouping:   []GroupingCall{{Name: "g", Columns: []string{"a", "b"}}},
	})
	if len(res.Sets) != 1 || len(res.Sets[0].Rows) != 1 {
		t.Fatalf("GROUP BY () on empty table must emit 1 row, got %+v", res.Sets)
	}
	r := res.Sets[0].Rows[0]
	if r["cnt"] != int64(0) || r["cntx"] != int64(0) {
		t.Fatalf("COUNT on empty input = %v/%v, want 0/0", r["cnt"], r["cntx"])
	}
	for _, name := range []string{"s", "av", "mn", "mx"} {
		if r[name] != nil {
			t.Fatalf("%s on empty input = %v, want NULL", name, r[name])
		}
	}
	if r["g"] != uint64(3) {
		t.Fatalf("GROUPING(a,b) on () = %v, want 3", r["g"])
	}
}

func TestEmptyTablePlainColumn(t *testing.T) {
	// GROUP BY a on 0 rows: 0 rows.
	res := Execute(nil, Query{
		GroupBy:    []Element{Col("a")},
		Aggregates: emptyAggs,
	})
	if len(res.Sets) != 1 || len(res.Sets[0].Rows) != 0 {
		t.Fatalf("GROUP BY a on empty table must emit 0 rows, got %+v", res.Sets)
	}
}

func TestEmptyTableRollup(t *testing.T) {
	// ROLLUP(a) on 0 rows: only the () set survives -> exactly 1 row.
	res := Execute(nil, Query{
		GroupBy:    []Element{Rollup(Col("a"))},
		Aggregates: emptyAggs,
		Grouping:   []GroupingCall{{Name: "g", Columns: []string{"a"}}},
	})
	if len(res.Sets) != 2 {
		t.Fatalf("ROLLUP(a) sets = %d, want 2", len(res.Sets))
	}
	if len(res.Sets[0].Rows) != 0 {
		t.Fatalf("set (a) on empty table must emit 0 rows, got %d", len(res.Sets[0].Rows))
	}
	if len(res.Sets[1].Rows) != 1 {
		t.Fatalf("set () on empty table must emit 1 row, got %d", len(res.Sets[1].Rows))
	}
	r := res.Sets[1].Rows[0]
	if r["cnt"] != int64(0) || r["s"] != nil || r["g"] != uint64(1) {
		t.Fatalf("rollup total row wrong: %v", r)
	}
}

func TestEmptyTableGroupingSetsAndCube(t *testing.T) {
	// GROUPING SETS((a), ()) on 0 rows: exactly 1 row.
	res := Execute(nil, Query{
		GroupBy:    []Element{GroupingSets([]string{"a"}, []string{})},
		Aggregates: emptyAggs,
	})
	if got := len(res.AllRows()); got != 1 {
		t.Fatalf("GROUPING SETS((a),()) on empty table rows = %d, want 1", got)
	}
	// CUBE(a, b) on 0 rows: only () survives -> exactly 1 row.
	res = Execute(nil, Query{
		GroupBy:    []Element{Cube(Col("a"), Col("b"))},
		Aggregates: emptyAggs,
	})
	if len(res.Sets) != 4 {
		t.Fatalf("CUBE(a,b) sets = %d, want 4", len(res.Sets))
	}
	if got := len(res.AllRows()); got != 1 {
		t.Fatalf("CUBE(a,b) on empty table rows = %d, want 1", got)
	}
}

func TestEmptyTableFilterAndDistinct(t *testing.T) {
	// FILTER and DISTINCT on the empty grand total: COUNT=0, SUM=NULL.
	res := Execute(nil, Query{
		Aggregates: []Aggregate{
			{Name: "c", Func: Count, Column: "x", Distinct: true,
				Filter: func(Row) bool { return true }},
			{Name: "s", Func: Sum, Column: "x", Distinct: true,
				Filter: func(Row) bool { return true }},
		},
	})
	r := res.Sets[0].Rows[0]
	if r["c"] != int64(0) || r["s"] != nil {
		t.Fatalf("empty filtered/distinct aggregates wrong: %v", r)
	}
}
