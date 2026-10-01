package eval

import "fmt"

// DefaultBudget caps the number of enumeration states when a fixture
// does not declare its own budget.
const DefaultBudget = 200000

// Report is one deterministic evaluation verdict.
type Report struct {
	Fixture       string   `json:"fixture"`
	Phase         string   `json:"phase"`
	AfterEvent    int      `json:"after_event"`
	Indeterminate bool     `json:"indeterminate"`
	RootMissing   bool     `json:"root_missing"`
	Counts        Counts   `json:"counts"`
	Score         float64  `json:"score"`
	Critical      []string `json:"critical,omitempty"`
	Details       []string `json:"details,omitempty"`
	Explanations  []string `json:"explanations,omitempty"`
}

// Evaluate replays events [0, upto), maps actual spans onto the expected
// structure with minimum cost, and scores the result. If the enumeration
// budget is exhausted the report is INDETERMINATE and carries no score.
func Evaluate(f *Fixture, upto int) Report {
	if upto > len(f.Events) {
		upto = len(f.Events)
	}
	rep := Report{Fixture: f.Name, AfterEvent: upto}
	childrenOf, roots := buildSpans(f.Events[:upto])
	budget := f.Budget
	if budget <= 0 {
		budget = DefaultBudget
	}
	m := newMatcher(f.TraceAttrs, childrenOf, f.Expected, budget)

	best := subResult{penalty: 0, counts: Counts{}}
	first := true
	for _, alt := range m.expandGroup([]*Node{f.Expected}) {
		r := m.matchSlots(alt, roots)
		if first || r.penalty < best.penalty {
			best = r
			first = false
		}
	}
	if m.exceeded {
		rep.Indeterminate = true
		return rep
	}
	rep.Counts = best.counts
	rep.Details = best.lines
	if f.Expected.Kind == KindSpan && m.active(f.Expected) {
		for i := 0; i < f.Expected.Min; i++ {
			if contains(best.missingLabels, slotLabel(f.Expected, i)) {
				rep.RootMissing = true
			}
		}
	}
	if rep.RootMissing {
		rep.Critical = append(rep.Critical,
			fmt.Sprintf("root span %q missing: descendant spans cannot establish identity and do not count as present", f.Expected.Name))
	}
	rep.Score = Score(best.counts, rep.RootMissing)
	return rep
}

// Replay evaluates the fixture at the declared split point, then applies
// late events one at a time, emitting a revised report (with an
// explanation of the delta) whenever the verdict changes.
func Replay(f *Fixture) []Report {
	split := f.EvaluateAfter
	if split <= 0 || split >= len(f.Events) {
		split = len(f.Events)
	}
	initial := Evaluate(f, split)
	initial.Phase = "initial"
	reports := []Report{initial}
	prev := initial
	for i := split; i < len(f.Events); i++ {
		cur := Evaluate(f, i+1)
		cur.Phase = fmt.Sprintf("revision#%d", len(reports))
		switch {
		case cur.Indeterminate:
			cur.Explanations = []string{"enumeration budget exhausted: verdict indeterminate, score withheld"}
			reports = append(reports, cur)
			prev = cur
		case prev.Indeterminate || prev.Counts != cur.Counts || prev.RootMissing != cur.RootMissing:
			cur.Explanations = explain(prev, cur, f.Events[i])
			reports = append(reports, cur)
			prev = cur
		}
	}
	return reports
}

// explain diffs two consecutive verdicts into human-readable revision
// reasons, anchored at the late event that triggered the change.
func explain(prev, cur Report, ev Event) []string {
	var out []string
	switch ev.Type {
	case "start":
		out = append(out, fmt.Sprintf("late event: span %s (%s) started", ev.ID, ev.Name))
	case "end":
		out = append(out, fmt.Sprintf("late event: span %s ended", ev.ID))
	}
	diff := func(name string, a, b int) {
		if a != b {
			out = append(out, fmt.Sprintf("%s %d -> %d", name, a, b))
		}
	}
	diff("matched", prev.Counts.Matched, cur.Counts.Matched)
	diff("missing", prev.Counts.Missing, cur.Counts.Missing)
	diff("duplicates", prev.Counts.Duplicates, cur.Counts.Duplicates)
	diff("unclosed", prev.Counts.Unclosed, cur.Counts.Unclosed)
	diff("misplaced", prev.Counts.Misplaced, cur.Counts.Misplaced)
	diff("unexpected", prev.Counts.Unexpected, cur.Counts.Unexpected)
	if prev.RootMissing && !cur.RootMissing {
		out = append(out, "critical cleared: root span is now present")
	}
	if !prev.Indeterminate && prev.Score != cur.Score {
		out = append(out, fmt.Sprintf("score %.1f -> %.1f", prev.Score, cur.Score))
	}
	return out
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
