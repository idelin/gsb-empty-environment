package spaneval

import "fmt"

// ScoreFormula is the public scoring rule.
//
//	score = clamp(1 - (missing + 0.5*duplicates + 0.5*unfinished) / total, 0, 1)
//
// total is the number of required slots in the instantiated expectation
// (conditions applied, one branch per choice, plus required descendants of
// matched optional slots). Because matching enforces parent-child
// structure, a structural error shows up as missing AND duplicate at once;
// extra spans can never cancel a missing slot by name count alone. A
// missing required root caps the score at 0.2 and flags Critical.
const ScoreFormula = "score = clamp(1 - (missing + 0.5*duplicates + 0.5*unfinished)/total, 0, 1); missing required root => critical, score capped at 0.2"

// Report is the result of one evaluation.
type Report struct {
	TraceID         string      `json:"trace_id"`
	Status          string      `json:"status"` // "final" | "undecided"
	Formula         string      `json:"formula"`
	Total           int         `json:"total"`
	Matched         int         `json:"matched"`
	Missing         int         `json:"missing"`
	Duplicates      int         `json:"duplicates"`
	Unfinished      int         `json:"unfinished"`
	Pending         int         `json:"pending"`
	MissingRatio    float64     `json:"missing_ratio"`
	DuplicateRatio  float64     `json:"duplicate_ratio"`
	UnfinishedRatio float64     `json:"unfinished_ratio"`
	Score           float64     `json:"score"`
	ScoreRange      *[2]float64 `json:"score_range,omitempty"`
	Critical        bool        `json:"critical"`
	Notes           []string    `json:"notes,omitempty"`
}

type evalDetail struct {
	report  *Report
	matched map[string]bool
	spans   map[string]*Span
}

// Evaluate replays events, maps spans onto the expected rule graph with
// minimum missing/duplicate cost, and scores the result. now/deadline
// decide whether unfinished spans are still within budget: while any are,
// the budget is undecided and no definitive score is given.
func Evaluate(root *Rule, events []Event, now, deadline int64) (*Report, error) {
	d, err := evaluate(root, events, now, deadline)
	if err != nil {
		return nil, err
	}
	return d.report, nil
}

func evaluate(root *Rule, events []Event, now, deadline int64) (*evalDetail, error) {
	if err := Validate(root); err != nil {
		return nil, err
	}
	spans, order, attrs, anomalies := Replay(events)
	slots := expandRule(root, attrs)

	kids := map[string][]*Span{}
	var roots []*Span
	for _, sp := range order {
		if _, ok := spans[sp.ParentID]; sp.ParentID == "" || !ok {
			roots = append(roots, sp) // true roots and orphans
			continue
		}
		kids[sp.ParentID] = append(kids[sp.ParentID], sp)
	}

	m := &matcher{kids: kids, limit: 2_000_000}
	o, assign, err := m.matchSiblings(slots, roots)
	if err != nil {
		return nil, err
	}

	var notes []string
	critical := false
	for i, s := range slots {
		if !s.optional && assign[i] == -1 {
			critical = true
			notes = append(notes, fmt.Sprintf("required root slot %q missing: critical regardless of matched children", slotLabel(s)))
		}
	}
	if anomalies > 0 {
		notes = append(notes, fmt.Sprintf("ignored %d duplicate/unknown events (stable span identity)", anomalies))
	}

	total := o.activated
	for _, s := range slots {
		total += requiredSize(s)
	}
	denom := total
	if denom == 0 {
		denom = 1
	}
	missingRatio := float64(o.missing) / float64(denom)
	unfinRatio := float64(o.unfinished) / float64(denom)

	score := func(missing, dup, unfinished int) float64 {
		v := 1 - (float64(missing)+0.5*float64(dup)+0.5*float64(unfinished))/float64(denom)
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		if critical && v > 0.2 {
			v = 0.2
		}
		return v
	}

	matchedSet := map[string]bool{}
	pending := 0
	for _, id := range o.matched {
		matchedSet[id] = true
		if sp := spans[id]; !sp.Ended && now <= deadline {
			pending++
		}
	}
	// Duplicates are counted globally: any replayed span matched to no slot
	// is a duplicate, including descendants of unmatched spans. Duplicates
	// never raise Matched, so extra spans cannot buy completeness.
	dup := len(spans) - len(matchedSet)
	expired := o.unfinished - pending
	if expired > 0 {
		notes = append(notes, fmt.Sprintf("%d span(s) started but never ended and budget expired: unfinished, not missing", expired))
	}

	rep := &Report{
		Status:          "final",
		Formula:         ScoreFormula,
		Total:           total,
		Matched:         total - o.missing,
		Missing:         o.missing,
		Duplicates:      dup,
		Unfinished:      o.unfinished,
		Pending:         pending,
		MissingRatio:    missingRatio,
		DuplicateRatio:  float64(dup) / float64(denom),
		UnfinishedRatio: unfinRatio,
		Score:           score(o.missing, dup, o.unfinished),
		Critical:        critical,
		Notes:           notes,
	}
	for _, sp := range order {
		if sp.TraceID != "" {
			rep.TraceID = sp.TraceID
			break
		}
	}
	if pending > 0 {
		// Budget undecided: pending spans may still finish, and late spans
		// may still fill missing slots, so only a range is reported.
		rep.Status = "undecided"
		lo := rep.Score
		hi := score(o.missing, dup, o.unfinished-pending)
		rep.ScoreRange = &[2]float64{lo, hi}
		notes = append(notes,
			fmt.Sprintf("%d span(s) still within budget: no definitive score until deadline", pending),
			"late arrivals may still fill missing slots; range assumes no new spans")
		rep.Notes = notes
	}
	return &evalDetail{report: rep, matched: matchedSet, spans: spans}, nil
}

func slotLabel(s *slot) string {
	if s.choice {
		return "<choice>"
	}
	return s.name
}
