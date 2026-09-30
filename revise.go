package spaneval

import "fmt"

// Revision explains how late telemetry changes a previous evaluation.
type Revision struct {
	Before  *Report  `json:"before"`
	After   *Report  `json:"after"`
	Changes []string `json:"changes"`
}

// Revise re-evaluates base+late events and explains the delta: which late
// spans filled expected slots, which are duplicates, which spans finished
// late, and how the score moved.
func Revise(root *Rule, base, late []Event, now, deadline int64) (*Revision, error) {
	before, err := evaluate(root, base, now, deadline)
	if err != nil {
		return nil, err
	}
	all := append(append([]Event{}, base...), late...)
	after, err := evaluate(root, all, now, deadline)
	if err != nil {
		return nil, err
	}

	var changes []string
	for _, ev := range late {
		switch ev.Type {
		case "span_start":
			if _, existed := before.spans[ev.SpanID]; existed {
				continue
			}
			sp := after.spans[ev.SpanID]
			if sp == nil {
				continue
			}
			if after.matched[ev.SpanID] {
				changes = append(changes, fmt.Sprintf("late span %q (%s) arrived and filled an expected slot", sp.Name, sp.SpanID))
			} else {
				changes = append(changes, fmt.Sprintf("late span %q (%s) arrived but matches no open slot: duplicate, completeness unchanged", sp.Name, sp.SpanID))
			}
		case "span_end":
			if sp, ok := before.spans[ev.SpanID]; ok && !sp.Ended {
				changes = append(changes, fmt.Sprintf("span %q (%s) finished late: no longer counted unfinished", sp.Name, sp.SpanID))
			}
		}
	}
	b, a := before.report, after.report
	changes = append(changes, fmt.Sprintf("missing %d->%d, duplicates %d->%d, unfinished %d->%d, score %.2f->%.2f",
		b.Missing, a.Missing, b.Duplicates, a.Duplicates, b.Unfinished, a.Unfinished, b.Score, a.Score))
	if b.Status != a.Status {
		changes = append(changes, fmt.Sprintf("status %s->%s", b.Status, a.Status))
	}
	return &Revision{Before: b, After: a, Changes: changes}, nil
}
