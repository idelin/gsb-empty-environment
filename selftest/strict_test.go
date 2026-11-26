package selftest_test

import (
	"testing"

	temporal "prescreen.local/ps-db7888bb15"
)

// By default UpdatePortion performs the exact per-row split: spanning two
// old adjacent records yields left, middle, middle, right (four rows). The
// report additionally exposes the coalesced view and callers can opt into
// CoalesceAdjacent for the committed state.
func TestAccStrictSplitDefaultAndMergedView(t *testing.T) {
	tbl := temporal.NewTable("strict", "k")
	if tbl.CoalesceAdjacent {
		t.Fatal("strict split should be the default")
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tbl.Insert(temporal.Row{ID: "r1", Key: temporal.Key{"k": 1}, Range: ir(0, 5), Payload: "old"}))
	must(tbl.Insert(temporal.Row{ID: "r2", Key: temporal.Key{"k": 1}, Range: ir(5, 10), Payload: "old"}))

	rep, err := tbl.UpdatePortionReport(temporal.Key{"k": 1}, ir(2, 8), "new")
	if err != nil {
		t.Fatal(err)
	}

	// Committed state is the strict four-piece split.
	wantStrict := []string{"[0,2)", "[2,5)", "[5,8)", "[8,10)"}
	if got := rangesOf(tbl.Rows); len(got) != 4 {
		t.Fatalf("strict split should yield 4 rows, got %v", got)
	} else {
		for i := range wantStrict {
			if got[i] != wantStrict[i] {
				t.Fatalf("strict piece %d = %s want %s (all %v)", i, got[i], wantStrict[i], got)
			}
		}
	}
	if rep.UpdatedRows != 2 || rep.RemnantRows != 2 {
		t.Fatalf("counts wrong: updated=%d remnants=%d", rep.UpdatedRows, rep.RemnantRows)
	}

	// Coalesced view merges the two adjacent new-payload middles.
	mergedRanges := rangesOf(rep.MergedRows)
	found := false
	for _, s := range mergedRanges {
		if s == "[2,8)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("merged view should contain [2,8): %v", mergedRanges)
	}

	// Opting in makes the committed state coalesce as well.
	tbl.CoalesceAdjacent = true
	if err := tbl.UpdatePortion(temporal.Key{"k": 1}, ir(2, 8), "new"); err != nil {
		t.Fatal(err)
	}
	// Note: current rows are already split once; re-applying [2,8) "new" is a
	// no-op geometrically on the new middles but still valid. Build a fresh
	// table to assert the committed merged shape cleanly.
	fresh := temporal.NewTable("strict2", "k")
	fresh.CoalesceAdjacent = true
	must(fresh.Insert(temporal.Row{Key: temporal.Key{"k": 1}, Range: ir(0, 5), Payload: "old"}))
	must(fresh.Insert(temporal.Row{Key: temporal.Key{"k": 1}, Range: ir(5, 10), Payload: "old"}))
	if err := fresh.UpdatePortion(temporal.Key{"k": 1}, ir(2, 8), "new"); err != nil {
		t.Fatal(err)
	}
	wantMerged := []string{"[0,2)", "[2,8)", "[8,10)"}
	if got := rangesOf(fresh.Rows); len(got) != len(wantMerged) {
		t.Fatalf("merged commit = %v, want %v", got, wantMerged)
	} else {
		for i := range wantMerged {
			if got[i] != wantMerged[i] {
				t.Fatalf("merged piece %d = %s want %s", i, got[i], wantMerged[i])
			}
		}
	}
}

// A target matching no rows leaves the committed slice untouched.
func TestAccNoopUpdateUntouched(t *testing.T) {
	tbl := temporal.NewTable("noop", "k")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tbl.Insert(temporal.Row{ID: "a", Key: temporal.Key{"k": 1}, Range: ir(0, 4)}))
	before := tbl.Snapshot()
	if err := tbl.UpdatePortion(temporal.Key{"k": 1}, ir(100, 200), "x"); err != nil {
		t.Fatalf("noop update: %v", err)
	}
	if len(tbl.Rows) != len(before) {
		t.Fatalf("noop changed row count: %d vs %d", len(tbl.Rows), len(before))
	}
	if tbl.Rows[0].Range.String() != before[0].Range.String() {
		t.Fatal("noop update altered range")
	}
}
