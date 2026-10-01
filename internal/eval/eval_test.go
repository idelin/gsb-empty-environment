package eval

import (
	"strings"
	"testing"
)

func lateFixture() *Fixture {
	f := &Fixture{
		Name:          "late",
		EvaluateAfter: 3,
		Expected: &Node{Kind: KindSpan, Name: "req", Children: []*Node{
			{Kind: KindSpan, Name: "db"},
			{Kind: KindSpan, Name: "cache"},
		}},
		Events: []Event{
			{Type: "start", ID: "s1", Name: "req"},
			{Type: "start", ID: "s2", Name: "db", Parent: "s1"},
			{Type: "end", ID: "s1"},
			{Type: "end", ID: "s2"},
			{Type: "start", ID: "s3", Name: "cache", Parent: "s1"},
			{Type: "end", ID: "s3"},
		},
	}
	if err := f.Validate(); err != nil {
		panic(err)
	}
	return f
}

func TestLateSpanRevisesScore(t *testing.T) {
	reports := Replay(lateFixture())
	if len(reports) != 4 {
		t.Fatalf("got %d reports, want 4", len(reports))
	}
	initial := reports[0]
	if initial.Counts.Missing != 1 || initial.Counts.Unclosed != 1 {
		t.Fatalf("initial counts = %+v", initial.Counts)
	}
	// revision#1: db end arrives -> unclosed resolved, still missing cache.
	r1 := reports[1]
	if r1.Counts.Unclosed != 0 || r1.Counts.Missing != 1 {
		t.Fatalf("r1 counts = %+v", r1.Counts)
	}
	joined := strings.Join(r1.Explanations, "\n")
	if !strings.Contains(joined, "unclosed 1 -> 0") {
		t.Fatalf("r1 explanations missing unclosed delta: %v", r1.Explanations)
	}
	// revision#2: cache starts late -> missing resolved.
	r2 := reports[2]
	if r2.Counts.Missing != 0 {
		t.Fatalf("r2 counts = %+v", r2.Counts)
	}
	joined = strings.Join(r2.Explanations, "\n")
	if !strings.Contains(joined, "missing 1 -> 0") || !strings.Contains(joined, "score") {
		t.Fatalf("r2 explanations: %v", r2.Explanations)
	}
	// final: clean 100.
	final := reports[3]
	if final.Score != 100 {
		t.Fatalf("final score = %v", final.Score)
	}
}

func TestScoreFormula(t *testing.T) {
	c := Counts{RequiredTotal: 4, Matched: 3, Duplicates: 1, Unclosed: 1}
	// 100*(3/4 - (0.5+0.25)/4) = 100*(0.75-0.1875) = 56.25
	if got := Score(c, false); got != 56.25 {
		t.Fatalf("Score = %v, want 56.25", got)
	}
	if got := Score(Counts{RequiredTotal: 1, Matched: 1}, true); got != RootCap {
		t.Fatalf("root-missing cap = %v, want %v", got, RootCap)
	}
	if got := Score(Counts{RequiredTotal: 2, Matched: 2, Duplicates: 100}, false); got != 0 {
		t.Fatalf("score must clamp at 0, got %v", got)
	}
}

func TestReplayAllFixtures(t *testing.T) {
	files := []string{
		"../../fixtures/checkout_ok.json",
		"../../fixtures/missing_root.json",
		"../../fixtures/duplicates.json",
		"../../fixtures/unclosed_vs_missing.json",
		"../../fixtures/late_arrival.json",
		"../../fixtures/choice_both_branches.json",
		"../../fixtures/budget_exceeded.json",
	}
	for _, path := range files {
		f, err := LoadFixture(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		reports := Replay(f)
		if len(reports) == 0 {
			t.Fatalf("%s: no reports", path)
		}
	}
	// Spot-check headline verdicts.
	mustScore := func(path string, want float64) {
		f, _ := LoadFixture(path)
		reps := Replay(f)
		got := reps[len(reps)-1].Score
		if !approx(got, want) {
			t.Fatalf("%s final score = %v, want %v", path, got, want)
		}
	}
	mustScore("../../fixtures/checkout_ok.json", 100)
	mustScore("../../fixtures/late_arrival.json", 100)
	mustScore("../../fixtures/choice_both_branches.json", 75)

	f, _ := LoadFixture("../../fixtures/budget_exceeded.json")
	if r := Replay(f)[0]; !r.Indeterminate {
		t.Fatalf("budget fixture should be indeterminate")
	}
	f, _ = LoadFixture("../../fixtures/missing_root.json")
	if r := Replay(f)[0]; !r.RootMissing {
		t.Fatalf("missing-root fixture should flag critical root")
	}
}
