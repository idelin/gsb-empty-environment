package diag

import (
	"reflect"
	"testing"
)

func snap1(st *Store) []Merged {
	return st.Snapshot(NewDocument("dummy text for merge tests"))
}

func TestMergeSameIdentityAcrossPhases(t *testing.T) {
	st := NewStore()
	st.Refresh("lint", 1, []Diagnostic{
		{RuleID: "r1", Range: Range{2, 5}, Severity: SeverityWarning, Message: "from lint"},
	})
	st.Refresh("syntax", 1, []Diagnostic{
		{RuleID: "r1", Range: Range{2, 5}, Severity: SeverityError, Message: "from syntax"},
	})
	got := snap1(st)
	if len(got) != 1 {
		t.Fatalf("expected 1 merged diagnostic, got %d: %+v", len(got), got)
	}
	m := got[0]
	if m.Severity != SeverityError {
		t.Errorf("severity: want max(error), got %v", m.Severity)
	}
	if m.Message != "from syntax" {
		t.Errorf("message: want highest-severity contribution, got %q", m.Message)
	}
	if !reflect.DeepEqual(m.Phases, []string{"lint", "syntax"}) {
		t.Errorf("phases: got %v", m.Phases)
	}
	wantKey := Key{Version: 1, RuleID: "r1", Range: Range{2, 5}}
	if m.Origin != wantKey {
		t.Errorf("origin: want %+v, got %+v", wantKey, m.Origin)
	}
}

func TestSameTextDifferentRangesNotMerged(t *testing.T) {
	st := NewStore()
	st.Refresh("lint", 1, []Diagnostic{
		{RuleID: "r1", Range: Range{0, 3}, Severity: SeverityWarning, Message: "same text"},
		{RuleID: "r1", Range: Range{7, 10}, Severity: SeverityWarning, Message: "same text"},
	})
	got := snap1(st)
	if len(got) != 2 {
		t.Fatalf("identical message at different ranges must not merge, got %+v", got)
	}
	if got[0].Range != (Range{0, 3}) || got[1].Range != (Range{7, 10}) {
		t.Errorf("ranges: got %+v", got)
	}
}

func TestSameRangeDifferentRuleNotMerged(t *testing.T) {
	st := NewStore()
	st.Refresh("lint", 1, []Diagnostic{
		{RuleID: "r1", Range: Range{0, 3}, Severity: SeverityWarning, Message: "a"},
		{RuleID: "r2", Range: Range{0, 3}, Severity: SeverityWarning, Message: "b"},
	})
	if got := snap1(st); len(got) != 2 {
		t.Fatalf("different rule ids must not merge, got %+v", got)
	}
}

func TestPhaseRefreshReplacesOnlyThatPhase(t *testing.T) {
	st := NewStore()
	st.Refresh("lint", 1, []Diagnostic{
		{RuleID: "r1", Range: Range{0, 1}, Severity: SeverityWarning, Message: "old lint"},
	})
	st.Refresh("syntax", 1, []Diagnostic{
		{RuleID: "r2", Range: Range{2, 3}, Severity: SeverityError, Message: "syntax"},
	})
	// Refresh lint with a new set; syntax must be untouched.
	st.Refresh("lint", 1, []Diagnostic{
		{RuleID: "r3", Range: Range{4, 5}, Severity: SeverityInfo, Message: "new lint"},
	})
	got := snap1(st)
	if len(got) != 2 {
		t.Fatalf("want 2 diagnostics, got %+v", got)
	}
	var sawR2, sawR3 bool
	for _, m := range got {
		switch m.RuleID {
		case "r1":
			t.Errorf("stale lint contribution survived refresh: %+v", m)
		case "r2":
			sawR2 = true
		case "r3":
			sawR3 = true
		}
	}
	if !sawR2 || !sawR3 {
		t.Errorf("want r2 (syntax) and r3 (new lint), got %+v", got)
	}
}

func TestRevokePhaseContributions(t *testing.T) {
	st := NewStore()
	st.Refresh("lint", 1, []Diagnostic{
		{RuleID: "r1", Range: Range{0, 1}, Severity: SeverityWarning, Message: "x"},
	})
	st.Refresh("syntax", 1, []Diagnostic{
		{RuleID: "r2", Range: Range{2, 3}, Severity: SeverityError, Message: "y"},
	})
	st.Refresh("lint", 1, nil) // source revoked
	got := snap1(st)
	if len(got) != 1 || got[0].RuleID != "r2" {
		t.Fatalf("revoked phase must contribute nothing, got %+v", got)
	}
}

func TestFileVersionUpdateReplacesOldIdentities(t *testing.T) {
	doc := NewDocument("abcdef")
	st := NewStore()
	st.Refresh("lint", 1, []Diagnostic{
		{RuleID: "r1", Range: Range{0, 1}, Severity: SeverityWarning, Message: "v1 finding"},
	})
	// Document advances to version 2.
	if err := doc.ApplyEdit(Edit{Start: 5, End: 5, Text: "z"}); err != nil {
		t.Fatal(err)
	}
	// Phase re-analyzes at version 2: old version-1 identities vanish.
	st.Refresh("lint", 2, []Diagnostic{
		{RuleID: "r1", Range: Range{1, 2}, Severity: SeverityInfo, Message: "v2 finding"},
	})
	got := st.Snapshot(doc)
	if len(got) != 1 {
		t.Fatalf("want 1 diagnostic, got %+v", got)
	}
	if got[0].Origin.Version != 2 || got[0].Origin.Range != (Range{1, 2}) {
		t.Errorf("old version identity not replaced: %+v", got[0].Origin)
	}
}

func TestDuplicateContributionsWithinResponse(t *testing.T) {
	st := NewStore()
	st.Refresh("lint", 1, []Diagnostic{
		{RuleID: "r1", Range: Range{0, 1}, Severity: SeverityInfo, Message: "weak"},
		{RuleID: "r1", Range: Range{0, 1}, Severity: SeverityError, Message: "strong"},
		{RuleID: "r1", Range: Range{0, 1}, Severity: SeverityWarning, Message: "mid"},
	})
	got := snap1(st)
	if len(got) != 1 {
		t.Fatalf("duplicate identities in one response must collapse, got %+v", got)
	}
	if got[0].Severity != SeverityError || got[0].Message != "strong" {
		t.Errorf("duplicates must keep max severity, got %+v", got[0])
	}
}
