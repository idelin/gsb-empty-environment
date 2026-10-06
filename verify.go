package testsel

import (
	"fmt"
	"sort"
)

// IssueKind classifies consistency problems found by Verify.
type IssueKind string

const (
	// IssueDeletedFuncCovered: recorded coverage depends, directly or
	// transitively, on a function that no longer exists.
	IssueDeletedFuncCovered IssueKind = "deleted-func-covered"
	// IssueStaleCoverage: recorded coverage references a function that is
	// absent from the new snapshot and was never known.
	IssueStaleCoverage IssueKind = "stale-coverage"
	// IssueDynamicEdgeChanged: the dynamic-dispatch target set of a method
	// name changed.
	IssueDynamicEdgeChanged IssueKind = "dynamic-edge-changed"
	// IssueDanglingEdge: the new call graph references an unknown function.
	IssueDanglingEdge IssueKind = "dangling-edge"
	// IssueGhostEdgeChange: a static edge changed although the caller's
	// body hash did not.
	IssueGhostEdgeChange IssueKind = "ghost-edge-change"
)

// Issue is a single consistency finding.
type Issue struct {
	Kind   IssueKind
	Test   TestID
	Func   FuncID
	Detail string
}

func (i Issue) String() string {
	switch {
	case i.Test != "":
		return fmt.Sprintf("%s: %s (test %s)", i.Kind, i.Detail, i.Test)
	case i.Func != "":
		return fmt.Sprintf("%s: %s (%s)", i.Kind, i.Detail, i.Func)
	default:
		return fmt.Sprintf("%s: %s", i.Kind, i.Detail)
	}
}

// Verify reconciles an old snapshot against a new one using the full
// reverse-dependency closure over the union of both call graphs. It reports
// deleted functions that tests still depend on, changed dynamic-dispatch
// targets, stale coverage entries and structural inconsistencies.
func Verify(old, new *Snapshot) []Issue {
	var issues []Issue
	ch := Diff(old, new)

	// Deleted functions: compute every function (and therefore every test)
	// impacted through the union graph, not only direct coverage.
	impacted := closure(ch.Deleted, reverseUnion(old.Edges, new.Edges))
	impactedTests := map[TestID]bool{}
	for test, covered := range old.Coverage {
		for _, f := range covered {
			if impacted[f] {
				impactedTests[test] = true
				break
			}
		}
	}
	for _, f := range ch.Deleted {
		issues = append(issues, Issue{
			Kind: IssueDeletedFuncCovered,
			Func: f,
			Detail: fmt.Sprintf("function deleted; %d test(s) depend on it via the dependency closure",
				len(impactedTests)),
		})
	}

	// Stale coverage: covered function unknown to both snapshots.
	for test, covered := range old.Coverage {
		for _, f := range covered {
			if _, ok := new.Funcs[f]; ok {
				continue
			}
			if _, ok := old.Funcs[f]; ok {
				continue // a deleted function, reported above
			}
			issues = append(issues, Issue{
				Kind:   IssueStaleCoverage,
				Test:   test,
				Func:   f,
				Detail: "coverage references a function absent from both snapshots",
			})
		}
	}

	// Dynamic edges: report callers whose dispatch targets changed.
	for caller := range dynamicCallers(old, new, ch.DynChanged) {
		issues = append(issues, Issue{
			Kind:   IssueDynamicEdgeChanged,
			Func:   caller,
			Detail: fmt.Sprintf("dynamic dispatch targets changed: %v", ch.DynChanged),
		})
	}

	// Dangling edges in the new graph.
	for _, e := range new.Edges {
		if _, ok := new.Funcs[e.Caller]; !ok {
			issues = append(issues, Issue{Kind: IssueDanglingEdge, Func: e.Caller,
				Detail: fmt.Sprintf("edge %s -> %s has unknown caller", e.Caller, e.Callee)})
		}
		if _, ok := new.Funcs[e.Callee]; !ok {
			issues = append(issues, Issue{Kind: IssueDanglingEdge, Func: e.Callee,
				Detail: fmt.Sprintf("edge %s -> %s has unknown callee", e.Caller, e.Callee)})
		}
	}

	// Ghost edge changes: connectivity changed without a body change.
	ghost := map[FuncID]bool{}
	for _, e := range ch.EdgesAdded {
		ghost[e.Caller] = true
	}
	for _, e := range ch.EdgesRemoved {
		ghost[e.Caller] = true
	}
	for _, f := range ch.Modified {
		delete(ghost, f)
	}
	for f := range ghost {
		if _, ok := new.Funcs[f]; !ok {
			continue // caller itself was deleted
		}
		issues = append(issues, Issue{Kind: IssueGhostEdgeChange, Func: f,
			Detail: "call edges changed but function body is unchanged"})
	}

	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Kind != issues[j].Kind {
			return issues[i].Kind < issues[j].Kind
		}
		if issues[i].Func != issues[j].Func {
			return issues[i].Func < issues[j].Func
		}
		return issues[i].Test < issues[j].Test
	})
	return issues
}
