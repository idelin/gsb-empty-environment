package testsel

import (
	"fmt"
	"sort"
)

// Decision is the outcome of test selection.
type Decision struct {
	// RunAll is true when the change cannot be mapped to recorded
	// coverage (e.g. new functions with unknown coverage) and the whole
	// suite must run.
	RunAll bool
	// Selected tests must run again.
	Selected []TestID
	// Affected is the full reverse-dependency closure of the changed
	// functions over the union of the old and new call graphs.
	Affected []FuncID
	// Reasons explains the decision in human-readable form.
	Reasons []string
}

// Select decides which recorded tests must run again, given the previously
// stored snapshot and a freshly computed one.
func Select(old, new *Snapshot) *Decision {
	ch := Diff(old, new)
	dec := &Decision{}

	// New functions have no recorded coverage: mark everything.
	if len(ch.Added) > 0 {
		dec.RunAll = true
		dec.Reasons = append(dec.Reasons, fmt.Sprintf(
			"%d new function(s) with unknown coverage: %s",
			len(ch.Added), joinFuncs(ch.Added, 5)))
		return dec
	}

	roots := map[FuncID]bool{}
	root := func(id FuncID, why string) {
		if !roots[id] {
			roots[id] = true
			dec.Reasons = append(dec.Reasons, fmt.Sprintf("%s: %s", why, id))
		}
	}
	for _, f := range ch.Modified {
		root(f, "modified function")
	}
	for _, f := range ch.Deleted {
		root(f, "deleted function")
	}
	// A changed call edge with an unchanged caller body is unusual (build
	// tags, analyzer drift); treat the caller as changed conservatively.
	for _, e := range ch.EdgesAdded {
		root(e.Caller, "call edge added")
	}
	for _, e := range ch.EdgesRemoved {
		root(e.Caller, "call edge removed")
	}
	// Dynamic dispatch: if the target set of a method name changed, every
	// function that may call it dynamically is impacted even though no
	// static edge connects them.
	for caller := range dynamicCallers(old, new, ch.DynChanged) {
		root(caller, "dynamic dispatch targets changed")
	}

	rootList := make([]FuncID, 0, len(roots))
	for r := range roots {
		rootList = append(rootList, r)
	}
	affected := closure(rootList, reverseUnion(old.Edges, new.Edges))
	for f := range affected {
		dec.Affected = append(dec.Affected, f)
	}
	sortFuncs(dec.Affected)

	selected := map[TestID]bool{}
	coverage := mergeCoverage(old, new)
	for test, covered := range coverage {
		for _, f := range covered {
			if affected[f] {
				selected[test] = true
				break
			}
			// Stale coverage: the covered function no longer exists and
			// was never reconciled, so the recording cannot be trusted.
			if _, ok := new.Funcs[f]; !ok {
				selected[test] = true
				dec.Reasons = append(dec.Reasons, fmt.Sprintf(
					"stale coverage: %s covered unknown function %s", test, f))
				break
			}
		}
	}
	for t := range selected {
		dec.Selected = append(dec.Selected, t)
	}
	sort.Slice(dec.Selected, func(i, j int) bool { return dec.Selected[i] < dec.Selected[j] })
	sort.Strings(dec.Reasons)
	return dec
}

func mergeCoverage(old, new *Snapshot) map[TestID][]FuncID {
	out := map[TestID][]FuncID{}
	merge := func(cov map[TestID][]FuncID) {
		for t, fs := range cov {
			out[t] = append(out[t], fs...)
		}
	}
	merge(old.Coverage)
	merge(new.Coverage)
	return out
}

func joinFuncs(fs []FuncID, max int) string {
	s := ""
	for i, f := range fs {
		if i == max {
			s += fmt.Sprintf("... (+%d more)", len(fs)-max)
			break
		}
		if i > 0 {
			s += ", "
		}
		s += string(f)
	}
	return s
}
