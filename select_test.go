package main

import (
	"reflect"
	"testing"
)

func graphWith(symbols []Symbol, edges []Edge) *Graph {
	g := NewGraph()
	for _, s := range symbols {
		g.AddSymbol(s)
	}
	for _, e := range edges {
		g.AddEdge(e)
	}
	return g
}

func selectTests(oldG, newG *Graph, modified ...string) []Selection {
	return Select(oldG, newG, Diff(oldG, newG, modified))
}

func findSelection(sels []Selection, test string) *Selection {
	for i := range sels {
		if sels[i].Test == test {
			return &sels[i]
		}
	}
	return nil
}

// Static call chain: test -> a -> b; changing b selects the test with the
// full reverse path.
func TestStaticChain(t *testing.T) {
	syms := []Symbol{
		{Name: "pkg.TestA", Test: true},
		{Name: "pkg.a"}, {Name: "pkg.b"},
	}
	edges := []Edge{
		{From: "pkg.TestA", To: "pkg.a"},
		{From: "pkg.a", To: "pkg.b"},
	}
	g := graphWith(syms, edges)
	sels := selectTests(g, g, "pkg.b")
	if len(sels) != 1 {
		t.Fatalf("want 1 selection, got %v", sels)
	}
	want := []string{"pkg.TestA", "pkg.a", "pkg.b"}
	if !reflect.DeepEqual(sels[0].Path, want) {
		t.Fatalf("path = %v, want %v", sels[0].Path, want)
	}
}

// Unaffected tests are not selected.
func TestUnaffectedTestNotSelected(t *testing.T) {
	syms := []Symbol{
		{Name: "pkg.TestA", Test: true},
		{Name: "pkg.TestB", Test: true},
		{Name: "pkg.a"}, {Name: "pkg.b"},
	}
	edges := []Edge{
		{From: "pkg.TestA", To: "pkg.a"},
		{From: "pkg.TestB", To: "pkg.b"},
	}
	g := graphWith(syms, edges)
	sels := selectTests(g, g, "pkg.a")
	if len(sels) != 1 || sels[0].Test != "pkg.TestA" {
		t.Fatalf("want only pkg.TestA, got %v", sels)
	}
}

// Dynamic dispatch: the candidate set acts as parallel edges; changing any
// candidate impacts the dispatcher's tests.
func TestDynamicCandidateSet(t *testing.T) {
	syms := []Symbol{
		{Name: "app.TestHandler", Test: true},
		{Name: "app.Handler"},
		{Name: "app.Render"}, {Name: "plugin.Render"},
	}
	edges := []Edge{
		{From: "app.TestHandler", To: "app.Handler"},
		{From: "app.Handler", Dynamic: true, Candidates: []string{"app.Render", "plugin.Render"}},
	}
	g := graphWith(syms, edges)
	sels := selectTests(g, g, "plugin.Render")
	if len(sels) != 1 {
		t.Fatalf("want 1 selection, got %v", sels)
	}
	want := []string{"app.TestHandler", "app.Handler", "plugin.Render"}
	if !reflect.DeepEqual(sels[0].Path, want) {
		t.Fatalf("path = %v, want %v", sels[0].Path, want)
	}
}

// Unresolved call with a name hint (rule R1): expands to every symbol
// with that short name, across packages.
func TestUnresolvedNameHint(t *testing.T) {
	syms := []Symbol{
		{Name: "app.TestJob", Test: true},
		{Name: "app.job"},
		{Name: "app.Run"}, {Name: "worker.Run"}, {Name: "app.other"},
	}
	edges := []Edge{
		{From: "app.TestJob", To: "app.job"},
		{From: "app.job", Unresolved: true, NameHint: "Run"},
	}
	g := graphWith(syms, edges)

	// A change in worker.Run must reach app.job through the hint expansion.
	sels := selectTests(g, g, "worker.Run")
	if s := findSelection(sels, "app.TestJob"); s == nil {
		t.Fatalf("want app.TestJob selected, got %v", sels)
	} else if want := []string{"app.TestJob", "app.job", "worker.Run"}; !reflect.DeepEqual(s.Path, want) {
		t.Fatalf("path = %v, want %v", s.Path, want)
	}

	// A change in an unrelated symbol must not be selected.
	sels = selectTests(g, g, "app.other")
	if len(sels) != 0 {
		t.Fatalf("want no selection, got %v", sels)
	}
}

// Unresolved call without a hint (rule R2): expands to the caller's
// package only, not to other packages.
func TestUnresolvedNoHintPackageScope(t *testing.T) {
	syms := []Symbol{
		{Name: "app.TestJob", Test: true},
		{Name: "app.job"}, {Name: "app.helper"},
		{Name: "lib.helper"},
	}
	edges := []Edge{
		{From: "app.TestJob", To: "app.job"},
		{From: "app.job", Unresolved: true},
	}
	g := graphWith(syms, edges)

	sels := selectTests(g, g, "app.helper")
	if s := findSelection(sels, "app.TestJob"); s == nil {
		t.Fatalf("same-package change must select the test, got %v", sels)
	}

	sels = selectTests(g, g, "lib.helper")
	if len(sels) != 0 {
		t.Fatalf("cross-package change must not select, got %v", sels)
	}
}

// Deleted symbol: the removed node is a change root, and its callers from
// the OLD graph are still impacted via the union of edges.
func TestDeletedSymbol(t *testing.T) {
	syms := []Symbol{
		{Name: "pkg.TestCaller", Test: true},
		{Name: "pkg.caller"}, {Name: "pkg.victim"},
	}
	oldG := graphWith(syms, []Edge{
		{From: "pkg.TestCaller", To: "pkg.caller"},
		{From: "pkg.caller", To: "pkg.victim"},
	})
	newG := graphWith(syms[:2], []Edge{
		{From: "pkg.TestCaller", To: "pkg.caller"},
	})

	changes := Diff(oldG, newG, nil)
	if !reflect.DeepEqual(changes.Removed, []string{"pkg.victim"}) {
		t.Fatalf("removed = %v, want [pkg.victim]", changes.Removed)
	}
	// pkg.caller lost an edge, so it is modified too.
	if !reflect.DeepEqual(changes.Modified, []string{"pkg.caller"}) {
		t.Fatalf("modified = %v, want [pkg.caller]", changes.Modified)
	}

	sels := Select(oldG, newG, changes)
	s := findSelection(sels, "pkg.TestCaller")
	if s == nil {
		t.Fatalf("caller of deleted symbol must be selected, got %v", sels)
	}
	// Shortest path goes through the modified caller, not the deleted leaf.
	want := []string{"pkg.TestCaller", "pkg.caller"}
	if !reflect.DeepEqual(s.Path, want) {
		t.Fatalf("path = %v, want %v", s.Path, want)
	}
}

// Recursion: cyclic graphs (including self-recursion) terminate and still
// propagate impact to all callers.
func TestRecursion(t *testing.T) {
	syms := []Symbol{
		{Name: "pkg.TestRec", Test: true},
		{Name: "pkg.fib"}, {Name: "pkg.wrapper"},
	}
	edges := []Edge{
		{From: "pkg.TestRec", To: "pkg.wrapper"},
		{From: "pkg.wrapper", To: "pkg.fib"},
		{From: "pkg.fib", To: "pkg.fib"}, // direct self-recursion
	}
	g := graphWith(syms, edges)
	sels := selectTests(g, g, "pkg.fib")
	if len(sels) != 1 {
		t.Fatalf("want 1 selection, got %v", sels)
	}
	want := []string{"pkg.TestRec", "pkg.wrapper", "pkg.fib"}
	if !reflect.DeepEqual(sels[0].Path, want) {
		t.Fatalf("path = %v, want %v", sels[0].Path, want)
	}

	// Mutual recursion: a <-> b, changing either reaches both.
	syms2 := []Symbol{
		{Name: "pkg.TestMutual", Test: true},
		{Name: "pkg.a"}, {Name: "pkg.b"},
	}
	edges2 := []Edge{
		{From: "pkg.TestMutual", To: "pkg.a"},
		{From: "pkg.a", To: "pkg.b"},
		{From: "pkg.b", To: "pkg.a"},
	}
	g2 := graphWith(syms2, edges2)
	sels = selectTests(g2, g2, "pkg.b")
	if s := findSelection(sels, "pkg.TestMutual"); s == nil {
		t.Fatalf("mutual recursion must select the test, got %v", sels)
	}
}

// Edge change old -> new: h switched its callee from x to y. h is
// modified, y is added, and tests reaching h are selected.
func TestEdgeChangeOldToNew(t *testing.T) {
	syms := []Symbol{
		{Name: "pkg.TestH", Test: true},
		{Name: "pkg.h"}, {Name: "pkg.x"},
	}
	oldG := graphWith(syms, []Edge{
		{From: "pkg.TestH", To: "pkg.h"},
		{From: "pkg.h", To: "pkg.x"},
	})
	newSyms := append(append([]Symbol{}, syms...), Symbol{Name: "pkg.y"})
	newG := graphWith(newSyms, []Edge{
		{From: "pkg.TestH", To: "pkg.h"},
		{From: "pkg.h", To: "pkg.y"},
	})

	changes := Diff(oldG, newG, nil)
	if !reflect.DeepEqual(changes.Added, []string{"pkg.y"}) {
		t.Fatalf("added = %v, want [pkg.y]", changes.Added)
	}
	if !reflect.DeepEqual(changes.Modified, []string{"pkg.h"}) {
		t.Fatalf("modified = %v, want [pkg.h]", changes.Modified)
	}

	sels := Select(oldG, newG, changes)
	s := findSelection(sels, "pkg.TestH")
	if s == nil {
		t.Fatalf("edge change must select the test, got %v", sels)
	}
	want := []string{"pkg.TestH", "pkg.h"}
	if !reflect.DeepEqual(s.Path, want) {
		t.Fatalf("path = %v, want %v", s.Path, want)
	}
}

// Shortest path: with both a direct and an indirect route to the changed
// symbol, the direct one wins.
func TestShortestPathPreferred(t *testing.T) {
	syms := []Symbol{
		{Name: "pkg.TestS", Test: true},
		{Name: "pkg.a"}, {Name: "pkg.b"}, {Name: "pkg.c"},
	}
	edges := []Edge{
		{From: "pkg.TestS", To: "pkg.c"}, // direct: length 1
		{From: "pkg.TestS", To: "pkg.a"},
		{From: "pkg.a", To: "pkg.b"},
		{From: "pkg.b", To: "pkg.c"}, // indirect: length 3
	}
	g := graphWith(syms, edges)
	sels := selectTests(g, g, "pkg.c")
	if len(sels) != 1 {
		t.Fatalf("want 1 selection, got %v", sels)
	}
	want := []string{"pkg.TestS", "pkg.c"}
	if !reflect.DeepEqual(sels[0].Path, want) {
		t.Fatalf("path = %v, want shortest %v", sels[0].Path, want)
	}
}

// A changed test selects itself with a single-node path.
func TestChangedTestSelectsItself(t *testing.T) {
	syms := []Symbol{{Name: "pkg.TestX", Test: true}, {Name: "pkg.x"}}
	edges := []Edge{{From: "pkg.TestX", To: "pkg.x"}}
	g := graphWith(syms, edges)
	sels := selectTests(g, g, "pkg.TestX")
	if len(sels) != 1 || !reflect.DeepEqual(sels[0].Path, []string{"pkg.TestX"}) {
		t.Fatalf("got %v", sels)
	}
}

// No changes at all selects nothing.
func TestNoChanges(t *testing.T) {
	syms := []Symbol{{Name: "pkg.TestX", Test: true}, {Name: "pkg.x"}}
	edges := []Edge{{From: "pkg.TestX", To: "pkg.x"}}
	g := graphWith(syms, edges)
	if sels := selectTests(g, g); len(sels) != 0 {
		t.Fatalf("want no selection, got %v", sels)
	}
}
