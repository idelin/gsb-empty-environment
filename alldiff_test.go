package alldiff

import (
	"reflect"
	"sort"
	"testing"
)

// Acceptance boundary: X1..X4 all share the domain {1, 2, 3}.
// Four variables cannot take distinct values from a 3-value pool, so the
// matching phase must report infeasibility instantly, before any search.
func TestAcceptanceFourVariablesThreeValues(t *testing.T) {
	vars := []Variable{
		{Name: "X1", Domain: []int{1, 2, 3}},
		{Name: "X2", Domain: []int{1, 2, 3}},
		{Name: "X3", Domain: []int{1, 2, 3}},
		{Name: "X4", Domain: []int{1, 2, 3}},
	}
	res := Propagate(vars)
	if res.Feasible {
		t.Fatalf("expected infeasible, got feasible with domains %v", res.Domains)
	}
	if res.Reason == "" {
		t.Fatal("expected a human-readable infeasibility reason")
	}
	if len(res.Matching) != 0 || res.Domains != nil {
		t.Fatal("infeasible result must not carry a matching or pruned domains")
	}
	t.Logf("infeasibility detected at matching stage: %s", res.Reason)
}

// Two variables pinned to the same singleton value can never differ.
func TestInfeasibleSingletonClash(t *testing.T) {
	res := Propagate([]Variable{
		{Name: "X1", Domain: []int{1}},
		{Name: "X2", Domain: []int{1}},
	})
	if res.Feasible {
		t.Fatal("expected infeasible for X1=X2={1}")
	}
}

// Classic SCC pruning: values 1 and 2 form a Hall set consumed by X1, X2,
// so edges X3-1 and X3-2 can never belong to a maximum matching.
func TestSCCFilteringPrunesHallSetEdges(t *testing.T) {
	res := Propagate([]Variable{
		{Name: "X1", Domain: []int{1, 2}},
		{Name: "X2", Domain: []int{1, 2}},
		{Name: "X3", Domain: []int{1, 2, 3, 4}},
		{Name: "X4", Domain: []int{3, 4, 5}},
	})
	if !res.Feasible {
		t.Fatalf("expected feasible, got reason: %s", res.Reason)
	}
	want := map[string][]int{
		"X1": {1, 2},
		"X2": {1, 2},
		"X3": {3, 4},
		"X4": {3, 4, 5},
	}
	if !reflect.DeepEqual(res.Domains, want) {
		t.Fatalf("pruned domains = %v, want %v", res.Domains, want)
	}
	wantRemoved := []Edge{{Variable: "X3", Value: 1}, {Variable: "X3", Value: 2}}
	if !reflect.DeepEqual(res.Removed, wantRemoved) {
		t.Fatalf("removed edges = %v, want %v", res.Removed, wantRemoved)
	}
	assertValidMatching(t, res, 4)
}

// When every variable can take every value, no edge is ever redundant.
func TestFullyInterchangeableDomainsKeepAllEdges(t *testing.T) {
	res := Propagate([]Variable{
		{Name: "X1", Domain: []int{1, 2, 3}},
		{Name: "X2", Domain: []int{1, 2, 3}},
		{Name: "X3", Domain: []int{1, 2, 3}},
	})
	if !res.Feasible {
		t.Fatalf("expected feasible, got reason: %s", res.Reason)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("expected no pruning, removed %v", res.Removed)
	}
	for name, d := range res.Domains {
		if !reflect.DeepEqual(d, []int{1, 2, 3}) {
			t.Fatalf("domain of %s = %v, want [1 2 3]", name, d)
		}
	}
}

// Régin's example-style chain: filtering must be idempotent and every kept
// edge must belong to some maximum matching (spot-checked by re-running
// propagation on the pruned domains).
func TestFilteringIsIdempotent(t *testing.T) {
	initial := []Variable{
		{Name: "X1", Domain: []int{1}},
		{Name: "X2", Domain: []int{2, 3}},
		{Name: "X3", Domain: []int{2, 3}},
		{Name: "X4", Domain: []int{1, 2, 3, 4}},
		{Name: "X5", Domain: []int{1, 2, 3, 4, 5}},
	}
	first := Propagate(initial)
	if !first.Feasible {
		t.Fatalf("expected feasible, got reason: %s", first.Reason)
	}
	assertValidMatching(t, first, 5)

	pruned := make([]Variable, 0, len(initial))
	for _, v := range initial {
		pruned = append(pruned, Variable{Name: v.Name, Domain: first.Domains[v.Name]})
	}
	second := Propagate(pruned)
	if !second.Feasible {
		t.Fatalf("second pass unexpectedly infeasible: %s", second.Reason)
	}
	if len(second.Removed) != 0 {
		t.Fatalf("second pass removed %v, want fixpoint", second.Removed)
	}
	if !reflect.DeepEqual(first.Domains, second.Domains) {
		t.Fatalf("domains changed between passes: %v vs %v", first.Domains, second.Domains)
	}
}

// A larger sanity check: a permutation-shaped problem stays feasible and
// the returned matching is a genuine system of distinct representatives.
func TestMatchingIsSystemOfDistinctRepresentatives(t *testing.T) {
	vars := []Variable{
		{Name: "X1", Domain: []int{2, 4}},
		{Name: "X2", Domain: []int{1, 2, 3}},
		{Name: "X3", Domain: []int{1, 3, 4}},
		{Name: "X4", Domain: []int{1, 2, 3, 4}},
	}
	res := Propagate(vars)
	if !res.Feasible {
		t.Fatalf("expected feasible, got reason: %s", res.Reason)
	}
	assertValidMatching(t, res, 4)
}

// assertValidMatching verifies the returned matching assigns each variable
// a distinct value drawn from its own (original) domain.
func assertValidMatching(t *testing.T, res *Result, nvars int) {
	t.Helper()
	if len(res.Matching) != nvars {
		t.Fatalf("matching covers %d variables, want %d", len(res.Matching), nvars)
	}
	seen := make(map[int]string)
	for _, e := range res.Matching {
		domain, ok := res.Domains[e.Variable]
		if !ok {
			t.Fatalf("matched unknown variable %q", e.Variable)
		}
		if i := sort.SearchInts(domain, e.Value); i >= len(domain) || domain[i] != e.Value {
			t.Fatalf("matched value %d not in pruned domain of %s", e.Value, e.Variable)
		}
		if prev, dup := seen[e.Value]; dup {
			t.Fatalf("value %d matched to both %s and %s", e.Value, prev, e.Variable)
		}
		seen[e.Value] = e.Variable
	}
}
