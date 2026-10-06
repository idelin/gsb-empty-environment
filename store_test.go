package diagnostics

import (
	"reflect"
	"testing"
)

func mustSubmit(t *testing.T, f *File, stage string, version uint64, diags ...Diagnostic) {
	t.Helper()
	if err := f.Submit(stage, version, diags); err != nil {
		t.Fatalf("Submit(%q, %d): %v", stage, version, err)
	}
}

func d(rule string, sev Severity, start, end int) Diagnostic {
	return Diagnostic{RuleID: rule, Severity: sev, Message: rule + " msg", Range: Range{start, end}}
}

func TestMergeDuplicateContributions(t *testing.T) {
	f := NewFile()
	mustSubmit(t, f, "lint", 0, d("R1", SeverityWarning, 5, 9))
	mustSubmit(t, f, "typecheck", 0, d("R1", SeverityError, 5, 9))

	got := f.Diagnostics()
	if len(got) != 1 {
		t.Fatalf("expected 1 merged diagnostic, got %d: %+v", len(got), got)
	}
	m := got[0]
	if m.Identity != (Identity{Version: 0, Range: Range{5, 9}, RuleID: "R1"}) {
		t.Fatalf("unexpected identity: %+v", m.Identity)
	}
	if m.Severity != SeverityError {
		t.Fatalf("severity = %v, want max(error)", m.Severity)
	}
	if !reflect.DeepEqual(m.Stages, []string{"lint", "typecheck"}) {
		t.Fatalf("stages = %v", m.Stages)
	}
}

func TestSameTextDifferentRangesNotMerged(t *testing.T) {
	f := NewFile()
	mustSubmit(t, f, "lint", 0,
		Diagnostic{RuleID: "R1", Severity: SeverityWarning, Message: "same text", Range: Range{2, 5}},
		Diagnostic{RuleID: "R1", Severity: SeverityWarning, Message: "same text", Range: Range{9, 12}},
	)
	if got := f.Diagnostics(); len(got) != 2 {
		t.Fatalf("identical text at different ranges must not merge, got %+v", got)
	}
}

func TestSameCurrentRangeDifferentVersionsNotMerged(t *testing.T) {
	f := NewFile()
	// Stage A analyzed version 0 and reported [5,8).
	mustSubmit(t, f, "A", 0, d("R1", SeverityWarning, 5, 8))
	// Two bytes are inserted at 0; A's diagnostic now sits at [7,10).
	f.ApplyEdits(Edit{0, 0, 2})
	// Stage B analyzed version 1 and reports the same rule at [7,10):
	// the current text range coincides with A's, but the identities
	// (analysis version + original range) differ, so no merge.
	mustSubmit(t, f, "B", 1, d("R1", SeverityError, 7, 10))

	got := f.Diagnostics()
	if len(got) != 2 {
		t.Fatalf("dedup by current text range is forbidden, got %+v", got)
	}
	for _, m := range got {
		if m.Current != (Range{7, 10}) {
			t.Fatalf("current range = %v, want [7,10)", m.Current)
		}
	}
	if got[0].Identity.Version != 0 || got[1].Identity.Version != 1 {
		t.Fatalf("identities must keep their analysis versions: %+v", got)
	}
}

func TestStageRefreshReplacesOnlyOwnContribution(t *testing.T) {
	f := NewFile()
	mustSubmit(t, f, "A", 0, d("R1", SeverityInfo, 1, 2), d("R2", SeverityInfo, 3, 4))
	mustSubmit(t, f, "B", 0, d("R3", SeverityInfo, 5, 6))
	// A refreshes: its old findings are replaced, B's are untouched.
	mustSubmit(t, f, "A", 0, d("R4", SeverityInfo, 7, 8))

	got := f.Diagnostics()
	var rules []string
	for _, m := range got {
		rules = append(rules, m.Identity.RuleID)
	}
	if !reflect.DeepEqual(rules, []string{"R3", "R4"}) {
		t.Fatalf("rules = %v, want [R3 R4]", rules)
	}
}

func TestSourceRevocation(t *testing.T) {
	f := NewFile()
	mustSubmit(t, f, "A", 0, d("R1", SeverityInfo, 1, 2))
	mustSubmit(t, f, "B", 0, d("R2", SeverityInfo, 3, 4))

	// Empty result set revokes the stage's contribution.
	mustSubmit(t, f, "A", 0)
	if got := f.Diagnostics(); len(got) != 1 || got[0].Identity.RuleID != "R2" {
		t.Fatalf("after empty submit: %+v", got)
	}
	// Explicit revoke removes the rest.
	f.Revoke("B")
	if got := f.Diagnostics(); len(got) != 0 {
		t.Fatalf("after revoke: %+v", got)
	}
	// Revoking an unknown stage is a no-op.
	f.Revoke("B")
}

func TestEditsInvalidateTouchedAndKeepIdentity(t *testing.T) {
	f := NewFile()
	mustSubmit(t, f, "A", 0,
		d("R1", SeverityWarning, 2, 5),
		d("R2", SeverityWarning, 10, 14),
	)
	// Delete [3,6): touches R1's content, R2 shifts left by 3.
	f.ApplyEdits(Edit{3, 6, 0})

	got := f.Diagnostics()
	if len(got) != 1 {
		t.Fatalf("expected only R2 to survive, got %+v", got)
	}
	m := got[0]
	if m.Identity != (Identity{Version: 0, Range: Range{10, 14}, RuleID: "R2"}) {
		t.Fatalf("survivor must keep its original identity, got %+v", m.Identity)
	}
	if m.Current != (Range{7, 11}) {
		t.Fatalf("current range = %v, want [7,11)", m.Current)
	}
}

func TestStaleResponseMappedForward(t *testing.T) {
	f := NewFile()
	f.ApplyEdits(Edit{0, 0, 5})   // v0 -> v1: insert 5 at 0
	f.ApplyEdits(Edit{20, 25, 1}) // v1 -> v2: replace [20,25) with 1 byte

	// A slow stage finally answers for version 0.
	mustSubmit(t, f, "slow", 0, d("R1", SeverityInfo, 10, 12))
	got := f.Diagnostics()
	if len(got) != 1 || got[0].Current != (Range{15, 17}) {
		t.Fatalf("stale diagnostic must map through the edit chain, got %+v", got)
	}
	if got[0].Identity.Version != 0 {
		t.Fatalf("identity keeps analysis version 0, got %+v", got[0].Identity)
	}
}

func TestFutureVersionRejected(t *testing.T) {
	f := NewFile()
	mustSubmit(t, f, "A", 0, d("R1", SeverityInfo, 1, 2))
	if err := f.Submit("A", 1, []Diagnostic{d("R9", SeverityInfo, 0, 1)}); err != ErrFutureVersion {
		t.Fatalf("err = %v, want ErrFutureVersion", err)
	}
	if got := f.Diagnostics(); len(got) != 1 || got[0].Identity.RuleID != "R1" {
		t.Fatalf("state must be unchanged after rejection, got %+v", got)
	}
}

func TestFullReanalysisAfterEdits(t *testing.T) {
	f := NewFile()
	mustSubmit(t, f, "lint", 0, d("R1", SeverityWarning, 2, 4))
	mustSubmit(t, f, "types", 0, d("R2", SeverityError, 8, 10))

	// Document edited twice; lint's finding is touched, types' survives.
	f.ApplyEdits(Edit{2, 4, 6}) // v0 -> v1: replaces R1's content
	f.ApplyEdits(Edit{0, 0, 1}) // v1 -> v2: insert 1 at 0

	// Full re-analysis at v2: lint now reports nothing, types reports a
	// fresh finding. Only the refreshing stages' contributions change.
	mustSubmit(t, f, "lint", 2)
	mustSubmit(t, f, "types", 2, d("R3", SeverityWarning, 20, 22))

	got := f.Diagnostics()
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Identity != (Identity{Version: 2, Range: Range{20, 22}, RuleID: "R3"}) {
		t.Fatalf("unexpected identity %+v", got[0].Identity)
	}
}

func TestStoreTracksFilesIndependently(t *testing.T) {
	s := NewStore()
	a := s.File("a.go")
	b := s.File("b.go")
	if a != s.File("a.go") {
		t.Fatal("File must return the same handle for the same path")
	}
	mustSubmit(t, a, "A", 0, d("R1", SeverityInfo, 0, 1))
	a.ApplyEdits(Edit{0, 0, 3})
	if b.Version() != 0 {
		t.Fatalf("b.go version = %d, want 0", b.Version())
	}
	mustSubmit(t, b, "B", 0, d("R2", SeverityInfo, 0, 1))
	s.Remove("a.go")
	if got := s.File("a.go").Diagnostics(); len(got) != 0 {
		t.Fatalf("removed file must come back empty, got %+v", got)
	}
}
