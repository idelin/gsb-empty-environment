package selftest_test

import (
	"errors"
	"testing"

	temporal "prescreen.local/ps-db7888bb15"
)

// TestAccHeadlineSpanMergeAndFKRollback mirrors the headline acceptance:
// one FOR PORTION update spans several old records; the adjacent updated
// middles coalesce, but because the new payload re-keys them to a parent that
// does not cover the span, the foreign-key temporal coverage check rejects
// the whole statement and every original row is restored.
func TestAccHeadlineSpanMergeAndFKRollback(t *testing.T) {
	s := temporal.NewSchema()
	parent := s.CreateTable("contract", "tenant", "cid")
	child := s.CreateTable("entry", "tenant", "cid")
	s.AddForeignKey(temporal.ForeignKeyCoverage{
		Name:          "entry_contract_fk",
		ChildTable:    "entry",
		ChildColumns:  []string{"tenant", "cid"},
		ParentTable:   "contract",
		ParentColumns: []string{"tenant", "cid"},
	})

	// Parent 10 covers [0,100) via two adjacent rows; parent 20 has a short
	// patch only, so it cannot cover a wide re-keyed middle.
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	must(parent.Insert(temporal.Row{ID: "p1", Key: temporal.Key{"tenant": 1, "cid": 10}, Range: ir(0, 50)}))
	must(parent.Insert(temporal.Row{ID: "p2", Key: temporal.Key{"tenant": 1, "cid": 10}, Range: ir(50, 100)}))
	must(parent.Insert(temporal.Row{ID: "p3", Key: temporal.Key{"tenant": 1, "cid": 20}, Range: ir(45, 55)}))

	// Two adjacent child records.
	must(child.Insert(temporal.Row{ID: "e1", Key: temporal.Key{"tenant": 1, "cid": 10}, Range: ir(0, 50), Payload: "a"}))
	must(child.Insert(temporal.Row{ID: "e2", Key: temporal.Key{"tenant": 1, "cid": 10}, Range: ir(50, 100), Payload: "b"}))
	must(s.Validate())
	child.CoalesceAdjacent = true // exercise the adjacent-merge outcome

	before := child.Snapshot()

	// Span [20,80) across both records, re-keying the merged middle to cid 20.
	err := child.UpdatePortion(
		temporal.Key{"tenant": 1, "cid": 10},
		ir(20, 80),
		temporal.Fields{"cid": 20, "note": "rekeyed"},
	)
	if !errors.Is(err, temporal.ErrCoverage) {
		t.Fatalf("expected coverage rollback, got: %v", err)
	}

	// Rollback leaves exactly the original two rows and the schema valid.
	if len(child.Rows) != len(before) {
		t.Fatalf("row count changed after rollback: got %d want %d", len(child.Rows), len(before))
	}
	for i := range before {
		if !before[i].Range.Equals(child.Rows[i].Range) {
			t.Fatalf("row %d range mutated after rollback: %s vs %s", i, child.Rows[i].Range, before[i].Range)
		}
		if child.Rows[i].Payload != before[i].Payload {
			t.Fatalf("row %d payload mutated after rollback", i)
		}
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("schema invalid after rollback: %v", err)
	}

	// Same span WITHOUT re-keying is valid: two adjacent middles merge into
	// one [20,80) segment carrying the same payload.
	if err := child.UpdatePortion(
		temporal.Key{"tenant": 1, "cid": 10},
		ir(20, 80),
		"merged",
	); err != nil {
		t.Fatalf("covered spanning update should commit: %v", err)
	}
	got := rangesOf(child.Rows)
	want := []string{"[0,20)", "[20,80)", "[80,100)"}
	if len(got) != len(want) {
		t.Fatalf("expected merged 3 rows, got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %s want %s (all %v)", i, got[i], want[i], got)
		}
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("merged result violates constraints: %v", err)
	}

	// The merged middle is a single row with the new payload.
	mid := findByRange(child.Rows, "[20,80)")
	if mid == nil || mid.Payload != "merged" {
		t.Fatalf("merged middle missing/wrong payload: %+v", mid)
	}
}
