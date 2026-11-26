package selftest_test

import (
	"errors"
	"strconv"
	"testing"

	temporal "prescreen.local/ps-db7888bb15"
)

// Rows that only carry Fields (no explicit Key) must participate in exclusion
// and FK checks through the table's KeyColumns.
func TestAccFieldsOnlyKeys(t *testing.T) {
	tbl := temporal.NewTable("cfg", "tenant", "acct")
	err := tbl.InsertRows(
		temporal.Row{Range: ir(0, 10), Fields: temporal.Fields{"tenant": 1, "acct": "a", "v": "x"}},
		temporal.Row{Range: ir(10, 20), Fields: temporal.Fields{"tenant": 1, "acct": "a", "v": "y"}},
	)
	if err != nil {
		t.Fatalf("adjacent fields-keyed rows should pass: %v", err)
	}
	err = tbl.Insert(temporal.Row{Range: ir(9, 11), Fields: temporal.Fields{"tenant": 1, "acct": "a"}})
	if !errors.Is(err, temporal.ErrOverlap) {
		t.Fatalf("expected overlap via fields key, got %v", err)
	}

	// Selector as an ordered tuple mapped to KeyColumns.
	err = tbl.UpdatePortion([]any{1, "a"}, ir(2, 4), "z")
	if err != nil {
		t.Fatalf("tuple-key update: %v", err)
	}
	// r1 splits into [0,2),[2,4)z,[4,10); r2 [10,20) is untouched. The two
	// adjacent old pieces keep separate because their Fields (v=x vs v=y)
	// differ.
	want := []string{"[0,2)", "[2,4)", "[4,10)", "[10,20)"}
	if got := rangesOf(tbl.Rows); len(got) != len(want) {
		t.Fatalf("fields-keyed split wrong: %v", got)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("piece %d = %s want %s (all %v)", i, got[i], want[i], got)
			}
		}
	}
}

// Continuous timestamp portion update respects point/open boundaries.
func TestAccContinuousUpdatePortion(t *testing.T) {
	const ts int64 = 1_700_000_000_000_000
	tbl := temporal.NewTable("contracts", "tenant")
	row := temporal.Row{ID: "c", Key: temporal.Key{"tenant": 9}, Range: tr(ts, ts+100, true, false), Payload: "v1"}
	if err := tbl.Insert(row); err != nil {
		t.Fatal(err)
	}
	// Update [ts+40, ts+60).
	err := tbl.UpdatePortion(temporal.Key{"tenant": 9}, tr(ts+40, ts+60, true, false), "v2")
	if err != nil {
		t.Fatal(err)
	}
	got := rangesOf(tbl.Rows)
	want := []string{
		"[" + itoa(ts) + "," + itoa(ts+40) + ")",
		"[" + itoa(ts+40) + "," + itoa(ts+60) + ")",
		"[" + itoa(ts+60) + "," + itoa(ts+100) + ")",
	}
	if len(got) != 3 {
		t.Fatalf("want 3 pieces, got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("piece %d = %s want %s (all %v)", i, got[i], want[i], got)
		}
	}

	// Point update [ts+50,ts+50] replaces exactly that instant; because the
	// surrounding middle is [ts+40,ts+60) with a different payload state,
	// run on a fresh table to keep assertions simple.
	t2 := temporal.NewTable("p", "k")
	if err := t2.Insert(temporal.Row{Key: temporal.Key{"k": 1}, Range: tr(0, 10, true, true), Payload: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := t2.UpdatePortion(temporal.Key{"k": 1}, temporal.Point(5), "b"); err != nil {
		t.Fatal(err)
	}
	// [0,10] - [5,5] leaves [0,5) and (5,10]; middle [5,5].
	byPayload := map[string]string{}
	for _, r := range t2.Rows {
		byPayload[r.Payload.(string)] = r.Range.String()
	}
	if byPayload["b"] != "[5,5]" {
		t.Fatalf("point middle wrong: %v", byPayload)
	}
	var aRanges []string
	for _, r := range t2.Rows {
		if r.Payload == "a" {
			aRanges = append(aRanges, r.Range.String())
		}
	}
	if len(aRanges) != 2 || aRanges[0] != "[0,5)" || aRanges[1] != "(5,10]" {
		t.Fatalf("point split remnants wrong: %v", aRanges)
	}
}

// Executor 3-arg/4-arg dispatch and package-level helpers.
func TestAccExecutorEntryPoints(t *testing.T) {
	ex := temporal.NewExecutor()
	ex.CreateTable("cfg", "tenant")
	// Update on an empty table is a valid no-op.
	if err := ex.UpdatePortion(temporal.Key{"tenant": 1}, ir(0, 5), "x"); err != nil {
		t.Fatalf("noop update should pass validation: %v", err)
	}

	tbl := ex.Table("cfg")
	if err := tbl.Insert(temporal.Row{Key: temporal.Key{"tenant": 1}, Range: ir(0, 10)}); err != nil {
		t.Fatal(err)
	}
	// 3-arg executor form targets default/sole table.
	if err := ex.UpdatePortion(temporal.Key{"tenant": 1}, ir(2, 3), "p"); err != nil {
		t.Fatalf("executor 3-arg: %v", err)
	}
	if len(tbl.Rows) != 3 {
		t.Fatalf("expected split, got %d rows", len(tbl.Rows))
	}

	// 4-arg named form.
	if err := ex.UpdatePortion("cfg", temporal.Key{"tenant": 1}, ir(4, 5), "q"); err != nil {
		t.Fatalf("executor 4-arg: %v", err)
	}

	// Package-level default runtime.
	temporal.ResetDefault()
	temporal.CreateTable("global_t", "k")
	if err := temporal.Default().Table("global_t").Insert(temporal.Row{Key: temporal.Key{"k": "z"}, Range: ir(0, 4)}); err != nil {
		t.Fatal(err)
	}
	if err := temporal.UpdatePortion(temporal.Key{"k": "z"}, ir(1, 2), "g"); err != nil {
		t.Fatalf("package UpdatePortion: %v", err)
	}
}

func itoa(x int64) string {
	return strconv.FormatInt(x, 10)
}
