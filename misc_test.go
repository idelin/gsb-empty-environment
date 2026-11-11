package netsec

import "testing"

func TestIntervalContains(t *testing.T) {
	in := Interval{Lo: 2, Hi: 4}
	for _, tick := range []int64{2, 3, 4} {
		if !in.Contains(tick) {
			t.Fatalf("window %v must contain %d", in, tick)
		}
	}
	for _, tick := range []int64{1, 5} {
		if in.Contains(tick) {
			t.Fatalf("window %v must not contain %d", in, tick)
		}
	}
	if !(Interval{Lo: 5, Hi: 4}).Empty() {
		t.Fatalf("Lo>Hi window must be empty")
	}
	if in.Empty() {
		t.Fatalf("window %v is not empty", in)
	}
}

func TestStringers(t *testing.T) {
	if VerdictBefore.String() != "necessarily-before" ||
		VerdictAfter.String() != "necessarily-after" ||
		VerdictStraddle.String() != "possibly-straddles" ||
		VerdictUnknown.String() != "unknown" {
		t.Fatalf("verdict stringers changed")
	}
	if ConflictInconsistentWindow.String() != "inconsistent-window" ||
		ConflictDuplicateSeq.String() != "duplicate-sequence" ||
		ConflictCheckMismatch.String() != "enumeration-mismatch" ||
		ConflictKind(99).String() != "unknown-conflict" {
		t.Fatalf("conflict stringers changed")
	}
}

// TestMismatchConflictRetained simulates an enumerator disagreeing with the
// analytic result and confirms the disagreement is surfaced as a conflict
// while the analytic pair stays in the report.
func TestMismatchConflictRetained(t *testing.T) {
	events := []Event{
		evt("u1", "f", 1, KindUse, "c", 1, 3),
		evt("r1", "g", 1, KindRevoke, "c", 1, 3),
	}
	g, _, err := buildConstraintGraph(events)
	if err != nil {
		t.Fatalf("buildConstraintGraph: %v", err)
	}
	pairs := []pairIndices{{use: 0, revoke: 1}}

	report := &Report{}
	for _, p := range pairs {
		report.Pairs = append(report.Pairs, classify(g, "c", p))
	}

	// Fabricate enumeration counts that only admit after, although the
	// analytic verdict over overlapping windows is straddle.
	enum := []enumCounts{{after: 7}}
	base := 0
	for k := range pairs {
		pr := &report.Pairs[base+k]
		pr.EnumChecked = true
		pr.EnumAfter = enum[k].after
		if want := verdictFromCounts(enum[k]); want != pr.Verdict {
			pr.Mismatch = true
			report.Conflicts = append(report.Conflicts, Conflict{
				Kind:    ConflictCheckMismatch,
				EventID: pr.UseID,
				Detail:  "synthetic disagreement",
			})
		}
	}

	if len(report.Pairs) != 1 || report.Pairs[0].Verdict != VerdictStraddle {
		t.Fatalf("analytic pair must remain in the report")
	}
	if !report.Pairs[0].Mismatch || len(report.Conflicts) != 1 ||
		report.Conflicts[0].Kind != ConflictCheckMismatch {
		t.Fatalf("mismatch must be retained as a conflict, got %+v", report)
	}
}

func TestEmptyInput(t *testing.T) {
	rep, err := Correlate(nil)
	if err != nil {
		t.Fatalf("Correlate(nil): %v", err)
	}
	if len(rep.Pairs) != 0 || len(rep.Conflicts) != 0 {
		t.Fatalf("empty input must yield empty report, got %+v", rep)
	}
}
