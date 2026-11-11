package netsec

import (
	"fmt"
	"sort"
)

// Options configures a Diff run.
type Options struct {
	// Conditions is the number of boolean conditions referenced by the
	// plans, 0..MaxConditions. All 2^Conditions assignments are enumerated.
	Conditions int
	// Normalize maps a plan path to its canonical identity. The admission
	// host owns path identity: two paths denote the same capability iff
	// their normalized forms are equal. Nil means identity.
	Normalize func(path string) string
	// IsDangerous optionally flags capabilities the host considers
	// dangerous. Dangerous capabilities unreachable under every assignment
	// are listed in Report.UnreachableDangerous and never counted as
	// additions.
	IsDangerous func(Capability) bool
	// MaxSteps caps interpreter steps per assignment to bound nested
	// fixed-count loops. Zero uses a default of 1<<20.
	MaxSteps int
}

const defaultMaxSteps = 1 << 20

// CapabilityDiff describes one capability the new plan adds, together with
// the condition assignments under which the addition is effective.
type CapabilityDiff struct {
	Capability Capability
	// AddedUnder lists the assignments under which the capability is
	// reachable in the new plan but not in the old plan.
	AddedUnder []Assignment
	// Dangerous reports whether Options.IsDangerous flags this capability.
	Dangerous bool
}

// AssignmentReport gives, for one condition assignment, the newly added
// capabilities and every normalized path feasible in the new plan.
type AssignmentReport struct {
	Assignment Assignment
	Added      []Capability
	// FeasiblePaths lists normalized read/write paths reachable in the new
	// plan under this assignment.
	FeasiblePaths []string
}

// Report is the result of Diff.
type Report struct {
	Conditions int
	// Added groups newly added capabilities by capability.
	Added []CapabilityDiff
	// ByAssignment lists per-assignment detail for assignments that add at
	// least one capability.
	ByAssignment []AssignmentReport
	// FeasiblePaths is the union of normalized paths feasible in the new
	// plan over all assignments.
	FeasiblePaths []string
	// UnreachableDangerous lists dangerous capabilities syntactically
	// present in the new plan but unreachable under every assignment, as
	// verified by enumeration; they are excluded from Added.
	UnreachableDangerous []Capability
}

// HasAdditions reports whether the upgrade adds any capability.
func (r *Report) HasAdditions() bool { return len(r.Added) > 0 }

// Diff compares oldPlan against newPlan and reports the capabilities the
// upgrade adds. Every condition assignment is enumerated: a capability is
// added under an assignment iff the new plan reaches it and the old plan
// does not. Unreachable operations, dangerous or not, never count.
func Diff(oldPlan, newPlan Node, opts Options) (*Report, error) {
	if opts.Conditions < 0 || opts.Conditions > MaxConditions {
		return nil, fmt.Errorf("netsec: conditions %d out of range [0, %d]", opts.Conditions, MaxConditions)
	}
	if opts.Normalize == nil {
		opts.Normalize = func(p string) string { return p }
	}
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = defaultMaxSteps
	}
	for _, plan := range []struct {
		name string
		n    Node
	}{{"old", oldPlan}, {"new", newPlan}} {
		vars, err := planVars(plan.n)
		if err != nil {
			return nil, fmt.Errorf("netsec: %s plan: %w", plan.name, err)
		}
		if vars>>uint(opts.Conditions) != 0 {
			return nil, fmt.Errorf("netsec: %s plan references condition variables beyond %d declared conditions", plan.name, opts.Conditions)
		}
	}

	total := 1 << uint(opts.Conditions)
	addedUnder := map[Capability][]Assignment{}
	var byAssignment []AssignmentReport
	feasible := map[string]struct{}{}
	reachableNew := map[Capability]struct{}{}

	for i := 0; i < total; i++ {
		a := Assignment(i)
		oldCaps, err := run(oldPlan, a, opts)
		if err != nil {
			return nil, fmt.Errorf("netsec: old plan under assignment %0*b: %w", opts.Conditions, i, err)
		}
		newCaps, err := run(newPlan, a, opts)
		if err != nil {
			return nil, fmt.Errorf("netsec: new plan under assignment %0*b: %w", opts.Conditions, i, err)
		}
		var added []Capability
		paths := map[string]struct{}{}
		for c := range newCaps {
			reachableNew[c] = struct{}{}
			if c.Kind == ReadPath || c.Kind == WritePath {
				paths[c.Path] = struct{}{}
				feasible[c.Path] = struct{}{}
			}
			if _, ok := oldCaps[c]; !ok {
				added = append(added, c)
				addedUnder[c] = append(addedUnder[c], a)
			}
		}
		if len(added) > 0 {
			sortCaps(added)
			byAssignment = append(byAssignment, AssignmentReport{
				Assignment:    a,
				Added:         added,
				FeasiblePaths: sortedKeys(paths),
			})
		}
	}

	added := make([]CapabilityDiff, 0, len(addedUnder))
	for c, when := range addedUnder {
		d := CapabilityDiff{Capability: c, AddedUnder: when}
		if opts.IsDangerous != nil && opts.IsDangerous(c) {
			d.Dangerous = true
		}
		added = append(added, d)
	}
	sort.Slice(added, func(i, j int) bool {
		return capLess(added[i].Capability, added[j].Capability)
	})

	var unreachable []Capability
	if opts.IsDangerous != nil {
		syntactic := map[Capability]struct{}{}
		if err := collectSyntactic(newPlan, opts.Normalize, syntactic); err != nil {
			return nil, fmt.Errorf("netsec: new plan: %w", err)
		}
		for c := range syntactic {
			if !opts.IsDangerous(c) {
				continue
			}
			if _, ok := reachableNew[c]; !ok {
				unreachable = append(unreachable, c)
			}
		}
		sortCaps(unreachable)
	}

	return &Report{
		Conditions:           opts.Conditions,
		Added:                added,
		ByAssignment:         byAssignment,
		FeasiblePaths:        sortedKeys(feasible),
		UnreachableDangerous: unreachable,
	}, nil
}

func capLess(a, b Capability) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	return a.Func < b.Func
}

func sortCaps(caps []Capability) {
	sort.Slice(caps, func(i, j int) bool { return capLess(caps[i], caps[j]) })
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
