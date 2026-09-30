package spaneval

import (
	"math"
	"strings"
	"testing"
)

func pint(n int) *int { return &n }

func mustRules(t *testing.T, path string) *Rule {
	t.Helper()
	r, err := LoadRules(path)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	return r
}

func mustEvents(t *testing.T, path string) []Event {
	t.Helper()
	ev, err := LoadEvents(path)
	if err != nil {
		t.Fatalf("LoadEvents: %v", err)
	}
	return ev
}

func mustEval(t *testing.T, r *Rule, ev []Event, now, deadline int64) *Report {
	t.Helper()
	rep, err := Evaluate(r, ev, now, deadline)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return rep
}

func TestConditionEval(t *testing.T) {
	attrs := map[string]string{"env": "prod", "cache": "on"}
	for _, c := range []struct {
		when string
		want bool
	}{
		{"", true},
		{"env=prod", true},
		{"env=dev", false},
		{"cache!=off", true},
		{"cache!=on", false},
		{"missing=", true}, // absent attr reads as empty string
	} {
		if got := condOK(c.when, attrs); got != c.want {
			t.Errorf("condOK(%q) = %v, want %v", c.when, got, c.want)
		}
	}
}

func TestExpandRepeatChoiceCondition(t *testing.T) {
	root := &Rule{
		Name: "root",
		Children: []*Rule{
			{Name: "a", Min: pint(1), Max: pint(3)},
			{Name: "pick", Kind: "choice", Children: []*Rule{{Name: "x"}, {Name: "y"}}},
			{Name: "cond", When: "flag=on"},
		},
	}
	roots := expandRule(root, map[string]string{"flag": "on"})
	if len(roots) != 1 {
		t.Fatalf("want 1 root slot, got %d", len(roots))
	}
	slots := roots[0].children
	if len(slots) != 3+1+1 {
		t.Fatalf("want 5 child slots, got %d", len(slots))
	}
	if requiredSize(slots[0]) != 1 || !slots[2].optional {
		t.Errorf("repeat expansion: slot0 required=%d slot2 optional=%v", requiredSize(slots[0]), slots[2].optional)
	}
	choice := slots[3]
	if !choice.choice || len(choice.branches) != 2 || requiredSize(choice) != 2 {
		t.Errorf("choice slot: branches=%d requiredSize=%d", len(choice.branches), requiredSize(choice))
	}
	if got := expandRule(root, map[string]string{"flag": "off"})[0].children; len(got) != 4 {
		t.Fatalf("condition off: want 4 slots, got %d", len(got))
	}
}

func TestEvaluateOKFixture(t *testing.T) {
	rep := mustEval(t, mustRules(t, "testdata/rules.json"), mustEvents(t, "testdata/events_ok.json"), 1000, 950)
	if rep.Status != "final" || rep.Score != 1.0 {
		t.Fatalf("want final score 1.0, got %+v", rep)
	}
	if rep.Missing != 0 || rep.Duplicates != 0 || rep.Unfinished != 0 {
		t.Fatalf("unexpected ratios: %+v", rep)
	}
}

func TestMissingRootIsCritical(t *testing.T) {
	rep := mustEval(t, mustRules(t, "testdata/rules.json"), mustEvents(t, "testdata/events_missing_root.json"), 1000, 950)
	if !rep.Critical {
		t.Fatal("missing root with many children must be critical")
	}
	if rep.Score > 0.2 {
		t.Fatalf("critical score must be capped at 0.2, got %v", rep.Score)
	}
	if rep.Missing == 0 {
		t.Fatal("root slot must be reported missing")
	}
}

func TestDuplicateDoesNotImproveCompleteness(t *testing.T) {
	rep := mustEval(t, mustRules(t, "testdata/rules.json"), mustEvents(t, "testdata/events_dup.json"), 1000, 950)
	if rep.Duplicates != 1 {
		t.Fatalf("want 1 duplicate (3rd db.query beyond max=2), got %d", rep.Duplicates)
	}
	if rep.Matched > rep.Total {
		t.Fatalf("matched %d exceeds total %d: duplicates bought completeness", rep.Matched, rep.Total)
	}
	want := 1 - 0.5/6.0
	if math.Abs(rep.Score-want) > 1e-9 {
		t.Fatalf("score = %v, want %v", rep.Score, want)
	}
}

func TestSameSpanReplayedCountsOnce(t *testing.T) {
	events := []Event{
		{Type: "span_start", TraceID: "t", SpanID: "s1", Name: "root", TS: 1},
		{Type: "span_start", TraceID: "t", SpanID: "s1", Name: "root", TS: 2},
		{Type: "span_end", TraceID: "t", SpanID: "s1", TS: 3},
	}
	spans, _, _, anomalies := Replay(events)
	if len(spans) != 1 || anomalies != 1 {
		t.Fatalf("stable identity: spans=%d anomalies=%d", len(spans), anomalies)
	}
	rep := mustEval(t, &Rule{Name: "root"}, events, 10, 5)
	if rep.Score != 1.0 || rep.Duplicates != 0 {
		t.Fatalf("replayed same span must not change completeness: %+v", rep)
	}
}

func TestStructuralErrorNotOffsetByNameCount(t *testing.T) {
	// render.template exists but hangs under db.query instead of render:
	// the name count is perfect, yet the mapping must report both a
	// missing slot and a duplicate span.
	rep := mustEval(t, mustRules(t, "testdata/rules.json"), mustEvents(t, "testdata/events_wrong_parent.json"), 1000, 950)
	if rep.Missing != 1 || rep.Duplicates != 1 {
		t.Fatalf("structural error: missing=%d dup=%d, want 1/1", rep.Missing, rep.Duplicates)
	}
	if rep.Score >= 1.0 {
		t.Fatal("structural error was offset by name count")
	}
}

func TestUnfinishedDistinctFromMissing(t *testing.T) {
	rep := mustEval(t, mustRules(t, "testdata/rules.json"), mustEvents(t, "testdata/events_unfinished.json"), 1000, 950)
	if rep.Unfinished != 1 || rep.Missing != 0 {
		t.Fatalf("unfinished=%d missing=%d, want 1/0", rep.Unfinished, rep.Missing)
	}
	if rep.Status != "final" || rep.Pending != 0 {
		t.Fatalf("budget expired: want final, got %+v", rep)
	}
}

func TestUndecidedBudgetGivesNoDefinitiveScore(t *testing.T) {
	rep := mustEval(t, mustRules(t, "testdata/rules.json"), mustEvents(t, "testdata/events_unfinished.json"), 850, 950)
	if rep.Status != "undecided" || rep.Pending != 1 {
		t.Fatalf("want undecided with 1 pending, got %+v", rep)
	}
	if rep.ScoreRange == nil || rep.ScoreRange[1] <= rep.ScoreRange[0] {
		t.Fatalf("undecided must carry a widening score range: %+v", rep.ScoreRange)
	}
}

func TestExhaustiveMappingBeatsGreedyOrder(t *testing.T) {
	// Two sibling slots both named "x" with different required children.
	// Spans arrive in the wrong order for in-order greedy assignment;
	// only a global min-cost mapping matches all of them.
	root := &Rule{
		Name: "root",
		Children: []*Rule{
			{Name: "x", Children: []*Rule{{Name: "p"}}},
			{Name: "x", Children: []*Rule{{Name: "q"}}},
		},
	}
	events := []Event{
		{Type: "span_start", TraceID: "t", SpanID: "r", Name: "root", TS: 1},
		{Type: "span_start", TraceID: "t", SpanID: "x2", ParentID: "r", Name: "x", TS: 2},
		{Type: "span_start", TraceID: "t", SpanID: "q1", ParentID: "x2", Name: "q", TS: 3},
		{Type: "span_end", TraceID: "t", SpanID: "q1", TS: 4},
		{Type: "span_end", TraceID: "t", SpanID: "x2", TS: 5},
		{Type: "span_start", TraceID: "t", SpanID: "x1", ParentID: "r", Name: "x", TS: 6},
		{Type: "span_start", TraceID: "t", SpanID: "p1", ParentID: "x1", Name: "p", TS: 7},
		{Type: "span_end", TraceID: "t", SpanID: "p1", TS: 8},
		{Type: "span_end", TraceID: "t", SpanID: "x1", TS: 9},
		{Type: "span_end", TraceID: "t", SpanID: "r", TS: 10},
	}
	rep := mustEval(t, root, events, 20, 15)
	if rep.Missing != 0 || rep.Duplicates != 0 || rep.Score != 1.0 {
		t.Fatalf("min-cost mapping failed: %+v", rep)
	}
}

func TestFinishedSpanPreferredOverOpenOne(t *testing.T) {
	root := &Rule{Name: "root", Children: []*Rule{{Name: "a"}}}
	events := []Event{
		{Type: "span_start", TraceID: "t", SpanID: "r", Name: "root", TS: 1},
		{Type: "span_start", TraceID: "t", SpanID: "a1", ParentID: "r", Name: "a", TS: 2},
		{Type: "span_start", TraceID: "t", SpanID: "a2", ParentID: "r", Name: "a", TS: 3},
		{Type: "span_end", TraceID: "t", SpanID: "a1", TS: 4},
		{Type: "span_end", TraceID: "t", SpanID: "r", TS: 5},
	}
	rep := mustEval(t, root, events, 100, 90) // budget expired: a2 is finished-business
	if rep.Unfinished != 0 || rep.Duplicates != 1 {
		t.Fatalf("matcher should pair the finished span: %+v", rep)
	}
}

func TestChoiceMutuallyExclusive(t *testing.T) {
	rules := mustRules(t, "testdata/rules.json")
	base := mustEvents(t, "testdata/events_ok.json")
	// Add the other auth branch: both branches present => one duplicate.
	extra := Event{Type: "span_start", TraceID: "t1", SpanID: "s20", ParentID: "s1", Name: "auth.basic", TS: 130}
	end := Event{Type: "span_end", TraceID: "t1", SpanID: "s20", TS: 140}
	rep := mustEval(t, rules, append(append([]Event{}, base...), extra, end), 1000, 950)
	if rep.Duplicates != 1 || rep.Missing != 0 {
		t.Fatalf("both choice branches: dup=%d missing=%d, want 1/0", rep.Duplicates, rep.Missing)
	}
}

func TestConditionOffMakesSpanUnexpected(t *testing.T) {
	rules := mustRules(t, "testdata/rules.json")
	events := mustEvents(t, "testdata/events_ok.json")
	for i := range events {
		if events[i].Type == "trace_attrs" {
			events[i].Attrs["cache"] = "off"
		}
	}
	rep := mustEval(t, rules, events, 1000, 950)
	if rep.Duplicates != 1 {
		t.Fatalf("cache=off: cache.lookup must be an unexpected duplicate, got %+v", rep)
	}
	if rep.Total != 6 {
		t.Fatalf("condition off must shrink the expectation, total=%d want 6", rep.Total)
	}
}

func TestReviseOnLateArrival(t *testing.T) {
	rules := mustRules(t, "testdata/rules.json")
	base := mustEvents(t, "testdata/events_late_base.json")
	late := mustEvents(t, "testdata/events_late_arrival.json")
	rev, err := Revise(rules, base, late, 1000, 950)
	if err != nil {
		t.Fatalf("Revise: %v", err)
	}
	if rev.Before.Missing != 1 || rev.After.Missing != 0 {
		t.Fatalf("late span must fill the missing slot: %d -> %d", rev.Before.Missing, rev.After.Missing)
	}
	if rev.After.Duplicates != 1 {
		t.Fatalf("third db.query beyond max=2 must be a duplicate, got %d", rev.After.Duplicates)
	}
	if rev.After.Score <= rev.Before.Score {
		t.Fatalf("score must improve: %v -> %v", rev.Before.Score, rev.After.Score)
	}
	var filled, dupSeen bool
	for _, c := range rev.Changes {
		if strings.Contains(c, "filled an expected slot") {
			filled = true
		}
		if strings.Contains(c, "duplicate") {
			dupSeen = true
		}
	}
	if !filled || !dupSeen {
		t.Fatalf("revision must explain both outcomes: %v", rev.Changes)
	}
}

func TestSynthRoundTrip(t *testing.T) {
	rules := mustRules(t, "testdata/rules.json")
	attrs := map[string]string{"env": "prod", "cache": "on"}
	events := Synth(rules, "t9", attrs, SynthOptions{Seed: 42})
	rep := mustEval(t, rules, events, 1<<40, 1<<40-1)
	if rep.Score != 1.0 || rep.Status != "final" {
		t.Fatalf("synthesized clean trace must score 1.0: %+v", rep)
	}

	dropped := Synth(rules, "t9", attrs, SynthOptions{Seed: 42, Drop: map[string]bool{"db.query": true}})
	if rep := mustEval(t, rules, dropped, 1<<40, 1<<40-1); rep.Missing == 0 {
		t.Fatal("dropping db.query must produce missing slots")
	}
	open := Synth(rules, "t9", attrs, SynthOptions{Seed: 42, LeaveOpen: map[string]bool{"render": true}})
	if rep := mustEval(t, rules, open, 1<<40, 1<<40-1); rep.Unfinished == 0 {
		t.Fatal("leaving render open must produce unfinished spans")
	}
	dup := Synth(rules, "t9", attrs, SynthOptions{Seed: 42, Duplicate: map[string]bool{"render": true}})
	if rep := mustEval(t, rules, dup, 1<<40, 1<<40-1); rep.Duplicates == 0 {
		t.Fatal("duplicated render must be reported as duplicate")
	}
}
