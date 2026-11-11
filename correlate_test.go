package netsec

import "testing"

func evt(id, feed string, seq int64, kind EventKind, cred string, lo, hi int64) Event {
	return Event{ID: id, Feed: feed, Seq: seq, Kind: kind, Cred: cred, Lo: lo, Hi: hi}
}

func findPair(t *testing.T, rep *Report, useID, revokeID string) PairResult {
	t.Helper()
	for _, p := range rep.Pairs {
		if p.UseID == useID && p.RevokeID == revokeID {
			return p
		}
	}
	t.Fatalf("pair %s/%s not found", useID, revokeID)
	return PairResult{}
}

func TestCorrelateVerdicts(t *testing.T) {
	cases := []struct {
		name    string
		events  []Event
		use     string
		revoke  string
		verdict Verdict
	}{
		{
			name: "necessarily before via disjoint windows",
			events: []Event{
				evt("u1", "f-a", 1, KindUse, "c1", 1, 2),
				evt("r1", "f-b", 1, KindRevoke, "c1", 5, 8),
			},
			use: "u1", revoke: "r1", verdict: VerdictBefore,
		},
		{
			name: "necessarily after via disjoint windows",
			events: []Event{
				evt("r1", "f-a", 1, KindRevoke, "c1", 1, 3),
				evt("u1", "f-b", 1, KindUse, "c1", 4, 6),
			},
			use: "u1", revoke: "r1", verdict: VerdictAfter,
		},
		{
			name: "straddle on overlapping windows",
			events: []Event{
				evt("u1", "f-a", 1, KindUse, "c1", 3, 7),
				evt("r1", "f-b", 1, KindRevoke, "c1", 4, 6),
			},
			use: "u1", revoke: "r1", verdict: VerdictStraddle,
		},
		{
			name: "same feed seq forces before despite raw overlap",
			events: []Event{
				evt("u1", "f-a", 1, KindUse, "c1", 1, 5),
				evt("r1", "f-a", 2, KindRevoke, "c1", 1, 5),
			},
			use: "u1", revoke: "r1", verdict: VerdictBefore,
		},
		{
			name: "same feed seq forces after despite arrival order",
			events: []Event{
				evt("r1", "f-a", 1, KindRevoke, "c1", 1, 5),
				evt("u1", "f-a", 2, KindUse, "c1", 1, 5),
			},
			use: "u1", revoke: "r1", verdict: VerdictAfter,
		},
		{
			name: "narrowing flips straddle into necessarily after",
			events: []Event{
				// Raw u1/r1 windows overlap, but r1's successor r2 forces
				// r1's upper bound below u1's lower bound.
				evt("u1", "f-a", 1, KindUse, "c1", 3, 5),
				evt("r1", "f-b", 1, KindRevoke, "c1", 1, 5),
				evt("r2", "f-b", 2, KindRevoke, "c1", 3, 3),
			},
			use: "u1", revoke: "r1", verdict: VerdictAfter,
		},
		{
			name: "only same tick feasible is straddle not before after",
			events: []Event{
				evt("u1", "f-a", 1, KindUse, "c1", 4, 4),
				evt("r1", "f-b", 1, KindRevoke, "c1", 4, 4),
			},
			use: "u1", revoke: "r1", verdict: VerdictStraddle,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := Correlate(tc.events)
			if err != nil {
				t.Fatalf("Correlate: %v", err)
			}
			p := findPair(t, rep, tc.use, tc.revoke)
			if p.Verdict != tc.verdict {
				t.Fatalf("verdict = %s, want %s", p.Verdict, tc.verdict)
			}
			if !p.EnumChecked || p.Mismatch {
				t.Fatalf("enumeration check missing or mismatched: %+v", p)
			}
		})
	}
}

func TestNarrowingBounds(t *testing.T) {
	events := []Event{
		evt("e0", "f", 1, KindRevoke, "c", 0, 10),
		evt("e1", "f", 2, KindUse, "c", 0, 10),
		evt("e2", "f", 3, KindRevoke, "c", 0, 10),
	}
	rep, err := Correlate(events)
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	p01 := findPair(t, rep, "e1", "e0")
	if p01.NarrowedRevoke.Lo != 0 || p01.NarrowedRevoke.Hi != 8 {
		t.Fatalf("e0 window = %v, want [0 8]", p01.NarrowedRevoke)
	}
	if p01.NarrowedUse.Lo != 1 || p01.NarrowedUse.Hi != 9 {
		t.Fatalf("e1 window = %v, want [1 9]", p01.NarrowedUse)
	}
	p12 := findPair(t, rep, "e1", "e2")
	if p12.NarrowedRevoke.Lo != 2 || p12.NarrowedRevoke.Hi != 10 {
		t.Fatalf("e2 window = %v, want [2 10]", p12.NarrowedRevoke)
	}
	if p01.Verdict != VerdictAfter || p12.Verdict != VerdictBefore {
		t.Fatalf("verdicts = %s/%s, want after/before", p01.Verdict, p12.Verdict)
	}
}

func TestInconsistentWindowConflictRetained(t *testing.T) {
	events := []Event{
		evt("u1", "f", 1, KindUse, "c", 5, 5),
		evt("r1", "f", 2, KindRevoke, "c", 5, 5),
	}
	rep, err := Correlate(events)
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	var sawConflict bool
	for _, c := range rep.Conflicts {
		if c.Kind == ConflictInconsistentWindow {
			sawConflict = true
		}
	}
	if !sawConflict {
		t.Fatalf("expected inconsistent-window conflict, got %+v", rep.Conflicts)
	}
	if len(rep.Pairs) != 1 || rep.Pairs[0].Verdict != VerdictUnknown {
		t.Fatalf("pair must be retained as unknown, got %+v", rep.Pairs)
	}
	if rep.Pairs[0].EnumChecked || !rep.Pairs[0].EnumSkipped {
		t.Fatalf("enumeration must be skipped on infeasible group")
	}
}

func TestDuplicateSequenceRetained(t *testing.T) {
	events := []Event{
		evt("u1", "f", 1, KindUse, "c", 1, 4),
		evt("u2", "f", 1, KindUse, "c", 1, 4),
		evt("r1", "g", 1, KindRevoke, "c", 3, 6),
	}
	rep, err := Correlate(events)
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	var sawConflict bool
	for _, c := range rep.Conflicts {
		if c.Kind == ConflictDuplicateSeq {
			sawConflict = true
		}
	}
	if !sawConflict {
		t.Fatalf("expected duplicate-seq conflict")
	}
	if len(rep.Pairs) != 2 {
		t.Fatalf("both conflicting events must be retained, got %d pairs", len(rep.Pairs))
	}
}

func TestMultipleCredentialsAndFeeds(t *testing.T) {
	events := []Event{
		evt("u-a", "f1", 1, KindUse, "credA", 1, 3),
		evt("r-a", "f2", 1, KindRevoke, "credA", 2, 4),
		evt("r-b", "f3", 1, KindRevoke, "credB", 0, 8),
		evt("u-b", "f3", 2, KindUse, "credB", 9, 9),
	}
	rep, err := Correlate(events)
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	if len(rep.Pairs) != 2 {
		t.Fatalf("want 2 credential-separated pairs, got %d", len(rep.Pairs))
	}
	if p := findPair(t, rep, "u-a", "r-a"); p.Verdict != VerdictStraddle {
		t.Fatalf("credA = %s, want straddle", p.Verdict)
	}
	if p := findPair(t, rep, "u-b", "r-b"); p.Verdict != VerdictAfter {
		t.Fatalf("credB = %s, want necessarily-after", p.Verdict)
	}
}

func TestEnumerationBudgetSkips(t *testing.T) {
	events := []Event{
		evt("u1", "f", 1, KindUse, "c", 1, 3),
		evt("r1", "g", 1, KindRevoke, "c", 1, 3),
	}
	rep, err := Correlate(events, WithMaxEnumNodes(1))
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	p := rep.Pairs[0]
	if !p.EnumSkipped || p.EnumChecked {
		t.Fatalf("expected skipped enumeration, got %+v", p)
	}
	if p.Verdict != VerdictStraddle {
		t.Fatalf("analytic verdict must still be produced, got %s", p.Verdict)
	}

	rep, err = Correlate(events, WithMaxEnumAssignments(1))
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	if !rep.Pairs[0].EnumSkipped {
		t.Fatalf("assignment budget must trigger skip")
	}
}

func TestInvalidInput(t *testing.T) {
	cases := map[string][]Event{
		"duplicate id": {
			evt("x", "f", 1, KindUse, "c", 1, 2),
			evt("x", "g", 1, KindRevoke, "c", 1, 2),
		},
		"empty raw window": {
			evt("x", "f", 1, KindUse, "c", 5, 4),
		},
		"unknown kind": {
			{ID: "x", Feed: "f", Seq: 1, Kind: EventKind(9), Cred: "c", Lo: 1, Hi: 2},
		},
		"empty credential": {
			evt("x", "f", 1, KindUse, "", 1, 2),
		},
	}
	for name, events := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Correlate(events); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}
