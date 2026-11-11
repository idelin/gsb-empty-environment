package netsec

import (
	"fmt"
	"sort"
)

// PairResult keeps every use/revoke pair of one credential. No pair is
// discarded: ambiguous or contradictory ones stay in the report with their
// narrowed windows and (when available) exhaustive enumeration counts.
type PairResult struct {
	Cred     string
	UseID    string
	RevokeID string

	// Verdict is the analytic conclusion derived from narrowed windows and
	// trusted sequence reachability.
	Verdict Verdict

	// NarrowedUse/NarrowedRevoke are the windows after sequence-based
	// narrowing.
	NarrowedUse    Interval
	NarrowedRevoke Interval

	// UseFeasible/RevokeFeasible are false when the corresponding window
	// collapsed under the constraints.
	UseFeasible    bool
	RevokeFeasible bool

	// Analytic feasibility of each strict ordering and of same-time
	// occurrence, computed over the full constraint system.
	BeforeFeasible   bool
	AfterFeasible    bool
	SameTimeFeasible bool

	// EnumChecked is true when exhaustive small-domain enumeration ran.
	// Counts hold the numbers of feasible assignments in which the usage
	// precedes, follows, or coincides with the revocation.
	EnumChecked bool
	EnumSkipped bool
	EnumBefore  int64
	EnumAfter   int64
	EnumSame    int64

	// Mismatch is set when enumeration contradicts the analytic verdict;
	// a matching Conflict is emitted and both conclusions are retained.
	Mismatch bool
}

// Options tunes the exhaustive cross-check.
type Options struct {
	// MaxEnumNodes skips enumeration for credential groups larger than this.
	MaxEnumNodes int
	// MaxEnumAssignments bounds the number of full assignments explored;
	// exceeding it skips the cross-check rather than failing the analysis.
	MaxEnumAssignments int64
}

// Option configures Correlate.
type Option func(*Options)

// WithMaxEnumNodes overrides the default enumeration node budget (8).
func WithMaxEnumNodes(n int) Option {
	return func(o *Options) { o.MaxEnumNodes = n }
}

// WithMaxEnumAssignments overrides the default enumeration assignment
// budget (100000).
func WithMaxEnumAssignments(n int64) Option {
	return func(o *Options) { o.MaxEnumAssignments = n }
}

func defaultOptions() Options {
	return Options{MaxEnumNodes: 8, MaxEnumAssignments: 100000}
}

// Report is the full correlation output.
type Report struct {
	Pairs     []PairResult
	Conflicts []Conflict
}

// Correlate correlates usage and revocation events.
//
// Events are grouped by credential; within each group the trusted feed
// sequences form strict-ordering constraints that first narrow every event's
// possible-occurrence interval. Each use/revoke pair is then classified as
// necessarily-before, necessarily-after, possibly-straddling, or unknown
// (inconsistent input). All pairs and all conflicts are retained. When the
// time domain is small enough, exhaustive enumeration independently
// re-derives every verdict; mismatches are reported, never silently resolved.
func Correlate(events []Event, opts ...Option) (*Report, error) {
	o := defaultOptions()
	for _, apply := range opts {
		apply(&o)
	}
	if err := validate(events); err != nil {
		return nil, err
	}

	groupIDs := make([]string, 0)
	groups := make(map[string][]Event)
	for _, evt := range events {
		if _, ok := groups[evt.Cred]; !ok {
			groupIDs = append(groupIDs, evt.Cred)
		}
		groups[evt.Cred] = append(groups[evt.Cred], evt)
	}
	sort.Strings(groupIDs)

	report := &Report{}
	for _, cred := range groupIDs {
		if err := correlateCredential(cred, groups[cred], o, report); err != nil {
			return nil, err
		}
	}
	return report, nil
}

func correlateCredential(cred string, events []Event, o Options, report *Report) error {
	g, conflicts, err := buildConstraintGraph(events)
	if err != nil {
		return err
	}
	report.Conflicts = append(report.Conflicts, conflicts...)

	uses, revokes := collectByKind(g)
	sort.Slice(uses, func(i, j int) bool { return g.nodes[uses[i]].evt.ID < g.nodes[uses[j]].evt.ID })
	sort.Slice(revokes, func(i, j int) bool { return g.nodes[revokes[i]].evt.ID < g.nodes[revokes[j]].evt.ID })

	pairs := make([]pairIndices, 0, len(uses)*len(revokes))
	for _, u := range uses {
		for _, r := range revokes {
			pairs = append(pairs, pairIndices{use: u, revoke: r})
		}
	}

	base := len(report.Pairs)
	for _, p := range pairs {
		report.Pairs = append(report.Pairs, classify(g, cred, p))
	}

	enum, skipped := enumerateGroup(g, pairs, o)
	for k := range pairs {
		pr := &report.Pairs[base+k]
		pr.EnumSkipped = skipped
		if skipped {
			continue
		}
		pr.EnumChecked = true
		pr.EnumBefore = enum[k].before
		pr.EnumAfter = enum[k].after
		pr.EnumSame = enum[k].same

		want := verdictFromCounts(enum[k])
		if want != pr.Verdict {
			pr.Mismatch = true
			report.Conflicts = append(report.Conflicts, Conflict{
				Kind:    ConflictCheckMismatch,
				EventID: pr.UseID,
				Detail: fmt.Sprintf("credential %q pair (%s vs %s): analytic %s, enumeration %s",
					cred, pr.UseID, pr.RevokeID, pr.Verdict, want),
			})
		}
	}
	return nil
}

type pairIndices struct {
	use    int
	revoke int
}

func collectByKind(g *constraintGraph) (uses, revokes []int) {
	for i, n := range g.nodes {
		switch n.evt.Kind {
		case KindUse:
			uses = append(uses, i)
		case KindRevoke:
			revokes = append(revokes, i)
		}
	}
	return uses, revokes
}

// classify derives the analytic verdict for one pair.
func classify(g *constraintGraph, cred string, p pairIndices) PairResult {
	u, r := g.nodes[p.use], g.nodes[p.revoke]
	pr := PairResult{
		Cred:           cred,
		UseID:          u.evt.ID,
		RevokeID:       r.evt.ID,
		NarrowedUse:    u.window,
		NarrowedRevoke: r.window,
		UseFeasible:    u.feasible,
		RevokeFeasible: r.feasible,
	}
	if !u.feasible || !r.feasible {
		pr.Verdict = VerdictUnknown
		return pr
	}

	switch {
	case g.canReach(p.use, p.revoke):
		// Trusted sequence forces the usage strictly before revocation.
		pr.BeforeFeasible = true
		pr.Verdict = VerdictBefore
		return pr
	case g.canReach(p.revoke, p.use):
		// Trusted sequence forces the revocation strictly before usage.
		pr.AfterFeasible = true
		pr.Verdict = VerdictAfter
		return pr
	}

	// Incomparable events: inspect the narrowed windows. A feasible strict
	// ordering exists iff some tick of the predecessor leaves room for a
	// later tick of the successor. No reverse path exists here, so no other
	// chain can invalidate the bound check.
	pr.BeforeFeasible = inc(u.window.Lo) <= r.window.Hi
	pr.AfterFeasible = inc(r.window.Lo) <= u.window.Hi
	pr.SameTimeFeasible = intervalsOverlap(u.window, r.window)

	switch {
	case pr.BeforeFeasible && !pr.AfterFeasible:
		pr.Verdict = VerdictBefore
	case pr.AfterFeasible && !pr.BeforeFeasible:
		pr.Verdict = VerdictAfter
	default:
		// Both orders feasible, or only same-time occurrence is feasible.
		pr.Verdict = VerdictStraddle
	}
	return pr
}

func intervalsOverlap(a, b Interval) bool {
	return a.Lo <= b.Hi && b.Lo <= a.Hi
}

func verdictFromCounts(c enumCounts) Verdict {
	total := c.before + c.after + c.same
	if total == 0 {
		return VerdictUnknown
	}
	if c.before > 0 && c.after == 0 && c.same == 0 {
		return VerdictBefore
	}
	if c.after > 0 && c.before == 0 && c.same == 0 {
		return VerdictAfter
	}
	return VerdictStraddle
}

func validate(events []Event) error {
	seen := make(map[string]struct{}, len(events))
	for _, evt := range events {
		if evt.ID == "" {
			return fmt.Errorf("netsec: event with empty id")
		}
		if _, ok := seen[evt.ID]; ok {
			return fmt.Errorf("netsec: duplicate event id %q", evt.ID)
		}
		seen[evt.ID] = struct{}{}
		if evt.Cred == "" {
			return fmt.Errorf("netsec: event %q has empty credential", evt.ID)
		}
		if evt.Kind != KindUse && evt.Kind != KindRevoke {
			return fmt.Errorf("netsec: event %q has unknown kind %d", evt.ID, evt.Kind)
		}
		if evt.Window().Empty() {
			return fmt.Errorf("netsec: event %q has empty raw window [%d,%d]",
				evt.ID, evt.Lo, evt.Hi)
		}
	}
	return nil
}
