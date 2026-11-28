package grouping

import "testing"

func TestGroupingBitmapRealNullVsSynthesized(t *testing.T) {
	input := []Row{
		{"a": nil, "b": int64(1)}, // real NULL key in a
		{"a": "x", "b": nil},      // real NULL key in b
		{"a": "x", "b": int64(2)},
	}
	q := Query{
		GroupBy: []Element{Rollup(Col("a"), Col("b"))},
		Grouping: []GroupingCall{
			{Name: "g_ab", Columns: []string{"a", "b"}},
			{Name: "g_ba", Columns: []string{"b", "a"}},
		},
		Aggregates: []Aggregate{{Name: "c", Func: Count}},
	}
	res := Execute(input, q)
	if len(res.Sets) != 3 {
		t.Fatalf("ROLLUP(a,b) must yield 3 sets, got %d", len(res.Sets))
	}

	// Set (a b): every real key participates, bitmap 00 even when the
	// key value itself is NULL.
	full := res.Sets[0]
	if len(full.Rows) != 3 {
		t.Fatalf("set (a b) rows = %d, want 3", len(full.Rows))
	}
	for _, r := range full.Rows {
		if r["g_ab"] != uint64(0) || r["g_ba"] != uint64(0) {
			t.Fatalf("set (a b): grouping = %v/%v, want 0/0 (row %v)", r["g_ab"], r["g_ba"], r)
		}
	}
	// The real-NULL key survives as a genuine NULL in the output.
	if v, ok := full.Rows[0]["a"]; v != nil || !ok {
		t.Fatalf("real NULL key lost: %v", full.Rows[0])
	}

	// Set (a): b is synthesized; b is the low bit of (a,b) and the
	// high bit of (b,a) -> 01 = 1 and 10 = 2.
	mid := res.Sets[1]
	if len(mid.Rows) != 2 {
		t.Fatalf("set (a) rows = %d, want 2", len(mid.Rows))
	}
	for _, r := range mid.Rows {
		if r["g_ab"] != uint64(1) || r["g_ba"] != uint64(2) {
			t.Fatalf("set (a): grouping = %v/%v, want 1/2", r["g_ab"], r["g_ba"])
		}
		if r["b"] != nil {
			t.Fatalf("set (a): b must be synthesized NULL, got %v", r["b"])
		}
	}
	// Real NULL a groups together and still reports bit 0 for a.
	if mid.Rows[0]["a"] != nil || mid.Rows[0]["c"] != int64(1) {
		t.Fatalf("real NULL a group wrong: %v", mid.Rows[0])
	}

	// Set (): both columns synthesized -> bitmap 11 / 11, one row.
	total := res.Sets[2]
	if len(total.Rows) != 1 {
		t.Fatalf("set () rows = %d, want 1", len(total.Rows))
	}
	tr := total.Rows[0]
	if tr["g_ab"] != uint64(3) || tr["g_ba"] != uint64(3) {
		t.Fatalf("set (): grouping = %v/%v, want 3/3", tr["g_ab"], tr["g_ba"])
	}
	if tr["a"] != nil || tr["b"] != nil || tr["c"] != int64(3) {
		t.Fatalf("grand total row wrong: %v", tr)
	}
}

func TestGroupingBitmapHighBitFirst(t *testing.T) {
	// GROUPING(c1, c2, c3): c1 is the most significant bit.
	set := []string{"c2"}
	if got := GroupingBitmap([]string{"c1", "c2", "c3"}, set); got != 5 { // 101
		t.Fatalf("got %d, want 5", got)
	}
	if got := GroupingBitmap([]string{"c1", "c2", "c3"}, nil); got != 7 { // 111
		t.Fatalf("got %d, want 7", got)
	}
	if got := GroupingBitmap([]string{"c1", "c2", "c3"}, []string{"c1", "c2", "c3"}); got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}
