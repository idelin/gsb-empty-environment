package diag

import "testing"

func mustEdit(t *testing.T, d *Document, e Edit) {
	t.Helper()
	if err := d.ApplyEdit(e); err != nil {
		t.Fatalf("ApplyEdit(%+v): %v", e, err)
	}
}

func mapOK(t *testing.T, d *Document, from FileVersion, r Range) Range {
	t.Helper()
	m, ok := d.MapRange(from, r)
	if !ok {
		t.Fatalf("MapRange(v%d, %+v) unexpectedly invalidated", from, r)
	}
	return m
}

func mapDropped(t *testing.T, d *Document, from FileVersion, r Range) {
	t.Helper()
	if m, ok := d.MapRange(from, r); ok {
		t.Fatalf("MapRange(v%d, %+v) = %+v, want invalidated", from, r, m)
	}
}

func TestInsertBeforeRangeShifts(t *testing.T) {
	d := NewDocument("hello world")
	mustEdit(t, d, Edit{Start: 0, End: 0, Text: ">> "})
	if got := mapOK(t, d, 1, Range{6, 11}); got != (Range{9, 14}) {
		t.Fatalf("got %+v", got)
	}
}

func TestInsertInsideRangeInvalidates(t *testing.T) {
	d := NewDocument("hello world")
	mustEdit(t, d, Edit{Start: 7, End: 7, Text: "cruel "})
	mapDropped(t, d, 1, Range{6, 11})
}

func TestInsertAtRangeBoundaries(t *testing.T) {
	d := NewDocument("abcdef")
	mustEdit(t, d, Edit{Start: 2, End: 2, Text: "XX"})
	// Range ends exactly at the insertion point: untouched, unshifted.
	if got := mapOK(t, d, 1, Range{0, 2}); got != (Range{0, 2}) {
		t.Fatalf("end-boundary: got %+v", got)
	}
	// Range starts exactly at the insertion point: untouched, shifted.
	if got := mapOK(t, d, 1, Range{2, 4}); got != (Range{4, 6}) {
		t.Fatalf("start-boundary: got %+v", got)
	}
}

func TestDeleteOverlappingRangeInvalidates(t *testing.T) {
	cases := []Range{
		{0, 3}, // overlaps the left edge
		{2, 6}, // fully contains the deletion
		{3, 4}, // fully inside the deletion
		{4, 8}, // overlaps the right edge
		{2, 5}, // exact match of the deleted region
	}
	for _, r := range cases {
		d := NewDocument("0123456789")
		mustEdit(t, d, Edit{Start: 2, End: 5, Text: ""})
		mapDropped(t, d, 1, r)
	}
}

func TestDeleteBeforeRangeShiftsLeft(t *testing.T) {
	d := NewDocument("0123456789")
	mustEdit(t, d, Edit{Start: 1, End: 4, Text: ""})
	if got := mapOK(t, d, 1, Range{6, 8}); got != (Range{3, 5}) {
		t.Fatalf("got %+v", got)
	}
	if d.Text() != "0456789" {
		t.Fatalf("text: got %q", d.Text())
	}
}

func TestReplaceInsideRangeInvalidates(t *testing.T) {
	d := NewDocument("hello world")
	mustEdit(t, d, Edit{Start: 6, End: 11, Text: "there"})
	mapDropped(t, d, 1, Range{6, 11})
	if got := mapOK(t, d, 1, Range{0, 5}); got != (Range{0, 5}) {
		t.Fatalf("untouched range: got %+v", got)
	}
}

func TestEditChainMapsAcrossMultipleVersions(t *testing.T) {
	d := NewDocument("a.b.c.")                         // a=0 .=1 b=2 .=3 c=4 .=5
	mustEdit(t, d, Edit{Start: 0, End: 0, Text: ">>"}) // v2: ">>a.b.c."
	mustEdit(t, d, Edit{Start: 4, End: 5, Text: ""})   // v3: delete "b" -> ">>a..c."
	mustEdit(t, d, Edit{Start: 6, End: 6, Text: "!"})  // v4: ">>a..c!."
	// The version-1 range of the second "." (index 3) survives every
	// edit: +2 from the insertion, -1 from the deletion before it.
	if got := mapOK(t, d, 1, Range{3, 4}); got != (Range{4, 5}) {
		t.Fatalf("chain mapping: got %+v", got)
	}
	// The version-1 range of "b" (index 2) is deleted in v3.
	mapDropped(t, d, 1, Range{2, 3})
	// Unknown versions are rejected.
	mapDropped(t, d, 99, Range{0, 1})
}

func TestUntouchedDiagnosticKeepsIdentityThroughEdits(t *testing.T) {
	d := NewDocument("foo;bar;baz")
	st := NewStore()
	st.Refresh("lint", d.Version(), []Diagnostic{
		{RuleID: "semi", Range: Range{3, 4}, Severity: SeverityHint, Message: "semicolon"},
		{RuleID: "word", Range: Range{8, 11}, Severity: SeverityWarning, Message: "baz"},
	})
	// Edit only the "bar" region: "foo;QUUX;baz" (delta +1).
	mustEdit(t, d, Edit{Start: 4, End: 7, Text: "QUUX"})
	snap := st.Snapshot(d)
	if len(snap) != 2 {
		t.Fatalf("both diagnostics are untouched and must survive, got %+v", snap)
	}
	semi, word := snap[0], snap[1]
	if semi.RuleID != "semi" || semi.Range != (Range{3, 4}) {
		t.Fatalf("semi: got %+v", semi)
	}
	if word.RuleID != "word" || word.Range != (Range{9, 12}) {
		t.Fatalf("word: got %+v", word)
	}
	// Identity is preserved: original version and original ranges.
	if semi.Origin != (Key{Version: 1, RuleID: "semi", Range: Range{3, 4}}) {
		t.Errorf("semi identity changed: %+v", semi.Origin)
	}
	if word.Origin != (Key{Version: 1, RuleID: "word", Range: Range{8, 11}}) {
		t.Errorf("word identity changed: %+v", word.Origin)
	}
}
