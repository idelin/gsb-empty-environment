package selector

import (
	"reflect"
	"testing"
)

func sym(ids ...string) []Symbol {
	var out []Symbol
	for _, id := range ids {
		kind := "func"
		if IsTest(Symbol{ID: id}) {
			kind = "test"
		}
		out = append(out, Symbol{ID: id, Kind: kind})
	}
	return out
}

func findSelection(rep *Report, test string) *Selection {
	for i := range rep.Selected {
		if rep.Selected[i].Test == test {
			return &rep.Selected[i]
		}
	}
	return nil
}

func hasChange(changes []Change, symbol string, reason ChangeReason) bool {
	for _, c := range changes {
		if c.Symbol == symbol && c.Reason == reason {
			return true
		}
	}
	return false
}

// Deleted symbol: pkg.Gone exists only in the old graph; its old caller
// pkg.Caller must be impacted and its test selected.
func TestDeletedSymbol(t *testing.T) {
	oldG := &Graph{
		Symbols: sym("pkg.Gone", "pkg.Caller", "pkg.TestCaller"),
		Edges: []Edge{
			{Caller: "pkg.Caller", Callee: "pkg.Gone"},
			{Caller: "pkg.TestCaller", Callee: "pkg.Caller"},
		},
	}
	newG := &Graph{
		Symbols: sym("pkg.Caller", "pkg.TestCaller"),
		Edges: []Edge{
			{Caller: "pkg.TestCaller", Callee: "pkg.Caller"},
		},
	}

	changes := ChangedSymbols(oldG, newG, nil)
	if !hasChange(changes, "pkg.Gone", ReasonDeleted) {
		t.Fatalf("expected pkg.Gone deleted change, got %+v", changes)
	}
	// pkg.Caller is also changed (its call to pkg.Gone was removed), so the
	// shortest impact path starts from pkg.Caller, not pkg.Gone.
	if !hasChange(changes, "pkg.Caller", ReasonEdgesChanged) {
		t.Fatalf("expected pkg.Caller edges-changed, got %+v", changes)
	}
	rep := Select(oldG, newG, changes)
	sel := findSelection(rep, "pkg.TestCaller")
	if sel == nil {
		t.Fatalf("expected pkg.TestCaller selected, got %+v", rep.Selected)
	}
	want := []string{"pkg.Caller", "pkg.TestCaller"}
	if !reflect.DeepEqual(sel.Path, want) {
		t.Fatalf("path = %v, want %v", sel.Path, want)
	}
	if sel.Distance != 1 {
		t.Fatalf("distance = %d, want 1", sel.Distance)
	}
}

// Recursive calls: A -> B -> C -> A cycle must terminate and give the
// shortest path back through the cycle.
func TestRecursiveCalls(t *testing.T) {
	g := &Graph{
		Symbols: sym("pkg.A", "pkg.B", "pkg.C", "pkg.TestA"),
		Edges: []Edge{
			{Caller: "pkg.A", Callee: "pkg.B"},
			{Caller: "pkg.B", Callee: "pkg.C"},
			{Caller: "pkg.C", Callee: "pkg.A"}, // recursion cycle
			{Caller: "pkg.TestA", Callee: "pkg.A"},
		},
	}
	changes := ChangedSymbols(g, g, []string{"pkg.B"})
	rep := Select(g, g, changes)
	sel := findSelection(rep, "pkg.TestA")
	if sel == nil {
		t.Fatalf("expected pkg.TestA selected")
	}
	// shortest: B -> A -> TestA (not B -> C -> A -> TestA)
	want := []string{"pkg.B", "pkg.A", "pkg.TestA"}
	if !reflect.DeepEqual(sel.Path, want) {
		t.Fatalf("path = %v, want %v", sel.Path, want)
	}
	if sel.Distance != 2 {
		t.Fatalf("distance = %d, want 2", sel.Distance)
	}
}

// Edge change old -> new: pkg.A's callee switches from pkg.Old to pkg.New.
// pkg.A must be flagged edges-changed and its test selected.
func TestEdgeChange(t *testing.T) {
	oldG := &Graph{
		Symbols: sym("pkg.A", "pkg.Old", "pkg.TestA"),
		Edges: []Edge{
			{Caller: "pkg.A", Callee: "pkg.Old"},
			{Caller: "pkg.TestA", Callee: "pkg.A"},
		},
	}
	newG := &Graph{
		Symbols: sym("pkg.A", "pkg.New", "pkg.TestA"),
		Edges: []Edge{
			{Caller: "pkg.A", Callee: "pkg.New"},
			{Caller: "pkg.TestA", Callee: "pkg.A"},
		},
	}
	changes := ChangedSymbols(oldG, newG, nil)
	if !hasChange(changes, "pkg.A", ReasonEdgesChanged) {
		t.Fatalf("expected pkg.A edges-changed, got %+v", changes)
	}
	if !hasChange(changes, "pkg.Old", ReasonDeleted) || !hasChange(changes, "pkg.New", ReasonAdded) {
		t.Fatalf("expected pkg.Old deleted and pkg.New added, got %+v", changes)
	}
	rep := Select(oldG, newG, changes)
	if findSelection(rep, "pkg.TestA") == nil {
		t.Fatalf("expected pkg.TestA selected, got %+v", rep.Selected)
	}
}

// Dynamic call: the caller dispatches to a candidate set; a change in any
// candidate must select the caller's test.
func TestDynamicCandidates(t *testing.T) {
	g := &Graph{
		Symbols: sym("pkg.Handler", "pkg.ImplA", "pkg.ImplB", "pkg.TestHandler"),
		Edges: []Edge{
			{Caller: "pkg.Handler", Dynamic: true, Candidates: []string{"pkg.ImplA", "pkg.ImplB"}},
			{Caller: "pkg.TestHandler", Callee: "pkg.Handler"},
		},
	}
	changes := ChangedSymbols(g, g, []string{"pkg.ImplB"})
	rep := Select(g, g, changes)
	sel := findSelection(rep, "pkg.TestHandler")
	if sel == nil {
		t.Fatalf("expected pkg.TestHandler selected via dynamic candidate")
	}
	want := []string{"pkg.ImplB", "pkg.Handler", "pkg.TestHandler"}
	if !reflect.DeepEqual(sel.Path, want) {
		t.Fatalf("path = %v, want %v", sel.Path, want)
	}
}

// Unresolved call: conservative rule 1 expands to every symbol with the
// matching short name, across packages.
func TestUnresolvedConservativeExpansion(t *testing.T) {
	g := &Graph{
		Symbols: sym("a.Run", "b.Run", "pkg.Caller", "pkg.TestCaller"),
		Edges: []Edge{
			{Caller: "pkg.Caller", Unresolved: true, Name: "Run"},
			{Caller: "pkg.TestCaller", Callee: "pkg.Caller"},
		},
	}
	u := g.Universe()
	got := ExpandEdge(g.Edges[0], u)
	want := []string{"a.Run", "b.Run"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expansion = %v, want %v", got, want)
	}
	changes := ChangedSymbols(g, g, []string{"b.Run"})
	rep := Select(g, g, changes)
	if findSelection(rep, "pkg.TestCaller") == nil {
		t.Fatalf("expected pkg.TestCaller selected via unresolved expansion")
	}
}

// Unresolved call with no name match falls back to the caller's package
// (rule 2), then to the whole universe (rule 3).
func TestUnresolvedFallbackRules(t *testing.T) {
	g := &Graph{
		Symbols: sym("pkg.Caller", "pkg.Helper", "other.Thing"),
		Edges:   []Edge{{Caller: "pkg.Caller", Unresolved: true, Name: "Missing"}},
	}
	u := g.Universe()
	got := ExpandEdge(g.Edges[0], u)
	want := []string{"pkg.Helper"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rule 2 expansion = %v, want %v", got, want)
	}

	g2 := &Graph{
		Symbols: sym("pkg.Caller", "other.Thing"),
		Edges:   []Edge{{Caller: "pkg.Caller", Unresolved: true, Name: "Missing"}},
	}
	got2 := ExpandEdge(g2.Edges[0], g2.Universe())
	want2 := []string{"other.Thing"}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("rule 3 expansion = %v, want %v", got2, want2)
	}
}

// Shortest path: with two routes from the changed symbol to a test, the
// shorter one must be reported.
func TestShortestPathChosen(t *testing.T) {
	g := &Graph{
		Symbols: sym("pkg.X", "pkg.Mid1", "pkg.Mid2", "pkg.Leaf", "pkg.TestX"),
		Edges: []Edge{
			{Caller: "pkg.Leaf", Callee: "pkg.X"}, // long: X -> Leaf -> ...
			{Caller: "pkg.Mid1", Callee: "pkg.X"}, // short: X -> Mid1 -> TestX
			{Caller: "pkg.Mid2", Callee: "pkg.Leaf"},
			{Caller: "pkg.TestX", Callee: "pkg.Mid1"},
			{Caller: "pkg.TestX", Callee: "pkg.Mid2"},
		},
	}
	changes := ChangedSymbols(g, g, []string{"pkg.X"})
	rep := Select(g, g, changes)
	sel := findSelection(rep, "pkg.TestX")
	if sel == nil {
		t.Fatalf("expected pkg.TestX selected")
	}
	want := []string{"pkg.X", "pkg.Mid1", "pkg.TestX"}
	if !reflect.DeepEqual(sel.Path, want) {
		t.Fatalf("path = %v, want %v", sel.Path, want)
	}
	if sel.Distance != 2 {
		t.Fatalf("distance = %d, want 2", sel.Distance)
	}
}

// A changed symbol that is itself a test is selected with distance 0.
func TestChangedTestItself(t *testing.T) {
	g := &Graph{Symbols: sym("pkg.TestFoo", "pkg.Helper")}
	changes := ChangedSymbols(g, g, []string{"pkg.TestFoo"})
	rep := Select(g, g, changes)
	sel := findSelection(rep, "pkg.TestFoo")
	if sel == nil {
		t.Fatalf("expected pkg.TestFoo selected")
	}
	if sel.Distance != 0 || !reflect.DeepEqual(sel.Path, []string{"pkg.TestFoo"}) {
		t.Fatalf("got %+v, want distance 0 self path", sel)
	}
}

// Changed symbols with no path to any test are reported as uncovered.
func TestUncovered(t *testing.T) {
	g := &Graph{
		Symbols: sym("pkg.Lonely", "pkg.TestOther"),
		Edges:   []Edge{{Caller: "pkg.TestOther", Callee: "pkg.Helper"}},
	}
	changes := ChangedSymbols(g, g, []string{"pkg.Lonely"})
	rep := Select(g, g, changes)
	if len(rep.Selected) != 0 {
		t.Fatalf("expected no selections, got %+v", rep.Selected)
	}
	if !reflect.DeepEqual(rep.Uncovered, []string{"pkg.Lonely"}) {
		t.Fatalf("uncovered = %v, want [pkg.Lonely]", rep.Uncovered)
	}
}
