package scc

import (
	"reflect"
	"testing"
)

func compsByID(m *Maintainer) map[string][]string {
	out := make(map[string][]string)
	for _, c := range m.Components() {
		out[c.ID] = c.Nodes
	}
	return out
}

// Acceptance boundary:
//  1. triangle A->B->C->A merges into one component;
//  2. independent D plus edge C->D joins as its own component;
//  3. deleting B->C splits the triangle into {A},{B},{C} exactly,
//     leaving D untouched.
func TestAcceptanceTriangleSplit(t *testing.T) {
	m := New()
	m.AddEdge("A", "B")
	m.AddEdge("B", "C")
	ch := m.AddEdge("C", "A") // closes the triangle

	if len(ch.Merges) != 1 {
		t.Fatalf("expected 1 merge, got %+v", ch.Merges)
	}
	mg := ch.Merges[0]
	if mg.To.ID != "A" || !reflect.DeepEqual(mg.To.Nodes, []string{"A", "B", "C"}) {
		t.Fatalf("merged component = %+v, want ID A with nodes [A B C]", mg.To)
	}
	if !reflect.DeepEqual(mg.From, []string{"A", "B", "C"}) {
		t.Fatalf("merge.From = %v, want [A B C]", mg.From)
	}
	if len(ch.Splits) != 0 {
		t.Fatalf("unexpected splits: %+v", ch.Splits)
	}

	m.AddVertex("D")
	ch = m.AddEdge("C", "D") // one-way edge: no cycle, no merge
	if !ch.Empty() {
		t.Fatalf("C->D should not change SCCs, got %+v", ch)
	}
	if got := compsByID(m); !reflect.DeepEqual(got, map[string][]string{
		"A": {"A", "B", "C"},
		"D": {"D"},
	}) {
		t.Fatalf("components = %v", got)
	}

	ch = m.RemoveEdge("B", "C") // breaks the cycle
	if len(ch.Merges) != 0 {
		t.Fatalf("unexpected merges: %+v", ch.Merges)
	}
	if len(ch.Splits) != 1 {
		t.Fatalf("expected 1 split, got %+v", ch.Splits)
	}
	sp := ch.Splits[0]
	if sp.From != "A" {
		t.Fatalf("split.From = %q, want A", sp.From)
	}
	wantTo := []Component{
		{ID: "A", Nodes: []string{"A"}},
		{ID: "B", Nodes: []string{"B"}},
		{ID: "C", Nodes: []string{"C"}},
	}
	if !reflect.DeepEqual(sp.To, wantTo) {
		t.Fatalf("split.To = %+v, want %+v", sp.To, wantTo)
	}

	// D's component is unaffected.
	dc, ok := m.ComponentOf("D")
	if !ok || dc.ID != "D" || !reflect.DeepEqual(dc.Nodes, []string{"D"}) {
		t.Fatalf("D component = %+v (ok=%v)", dc, ok)
	}
	if got := compsByID(m); !reflect.DeepEqual(got, map[string][]string{
		"A": {"A"}, "B": {"B"}, "C": {"C"}, "D": {"D"},
	}) {
		t.Fatalf("final components = %v", got)
	}
}

func TestNoMergeWithoutCycle(t *testing.T) {
	m := New()
	if ch := m.AddEdge("A", "B"); !ch.Empty() {
		t.Fatalf("one-way edge must not merge, got %+v", ch)
	}
	if got := compsByID(m); !reflect.DeepEqual(got, map[string][]string{"A": {"A"}, "B": {"B"}}) {
		t.Fatalf("components = %v", got)
	}
	// Closing the cycle merges into the min-ID component.
	ch := m.AddEdge("B", "A")
	if len(ch.Merges) != 1 || ch.Merges[0].To.ID != "A" {
		t.Fatalf("merge = %+v", ch.Merges)
	}
}

func TestMergeAcrossMultipleComponents(t *testing.T) {
	m := New()
	// Two cycles {A,B} and {C,D} plus a bridge B->C.
	m.AddEdge("A", "B")
	m.AddEdge("B", "A")
	m.AddEdge("C", "D")
	m.AddEdge("D", "C")
	m.AddEdge("B", "C")
	if got := compsByID(m); !reflect.DeepEqual(got, map[string][]string{
		"A": {"A", "B"}, "C": {"C", "D"},
	}) {
		t.Fatalf("components = %v", got)
	}
	// D->A closes a big cycle through all four nodes.
	ch := m.AddEdge("D", "A")
	if len(ch.Merges) != 1 {
		t.Fatalf("expected 1 merge, got %+v", ch.Merges)
	}
	mg := ch.Merges[0]
	if mg.To.ID != "A" || !reflect.DeepEqual(mg.To.Nodes, []string{"A", "B", "C", "D"}) {
		t.Fatalf("merged = %+v", mg.To)
	}
	if !reflect.DeepEqual(mg.From, []string{"A", "C"}) {
		t.Fatalf("merge.From = %v, want [A C]", mg.From)
	}
}

func TestRemoveEdgeKeepsComponentWhenStillStronglyConnected(t *testing.T) {
	m := New()
	// Triangle plus a redundant parallel path edge A->C.
	m.AddEdge("A", "B")
	m.AddEdge("B", "C")
	m.AddEdge("C", "A")
	m.AddEdge("A", "C")
	ch := m.RemoveEdge("A", "C")
	if !ch.Empty() {
		t.Fatalf("removing redundant edge must not split, got %+v", ch)
	}
	c, _ := m.ComponentOf("B")
	if c.ID != "A" || !reflect.DeepEqual(c.Nodes, []string{"A", "B", "C"}) {
		t.Fatalf("component = %+v", c)
	}
}

func TestRemoveVertexSplitsComponent(t *testing.T) {
	m := New()
	// A<->B and B<->C: one component {A,B,C} held together by B.
	m.AddEdge("A", "B")
	m.AddEdge("B", "A")
	m.AddEdge("B", "C")
	m.AddEdge("C", "B")
	ch := m.RemoveVertex("B")
	if len(ch.Splits) != 1 || ch.Splits[0].From != "A" {
		t.Fatalf("split = %+v", ch.Splits)
	}
	want := []Component{
		{ID: "A", Nodes: []string{"A"}},
		{ID: "C", Nodes: []string{"C"}},
	}
	if !reflect.DeepEqual(ch.Splits[0].To, want) {
		t.Fatalf("split.To = %+v, want %+v", ch.Splits[0].To, want)
	}
	if m.HasVertex("B") || m.HasEdge("A", "B") {
		t.Fatal("B and its edges should be gone")
	}
}

func TestIdempotentAndMissingOps(t *testing.T) {
	m := New()
	m.AddEdge("A", "B")
	if ch := m.AddEdge("A", "B"); !ch.Empty() {
		t.Fatalf("duplicate edge: %+v", ch)
	}
	if ch := m.RemoveEdge("A", "C"); !ch.Empty() {
		t.Fatalf("missing edge: %+v", ch)
	}
	if ch := m.RemoveVertex("Z"); !ch.Empty() {
		t.Fatalf("missing vertex: %+v", ch)
	}
	if ch := m.AddVertex("A"); !ch.Empty() {
		t.Fatalf("duplicate vertex: %+v", ch)
	}
}

// Cross-check the maintainer against a from-scratch Tarjan over random
// operation sequences.
func TestRandomAgainstFullRecompute(t *testing.T) {
	names := []string{"A", "B", "C", "D", "E", "F"}
	rng := uint64(42)
	next := func(n int) int {
		rng = rng*6364136223846793005 + 1442695040888963407
		return int(rng>>33) % n
	}
	m := New()
	for step := 0; step < 2000; step++ {
		u := names[next(len(names))]
		v := names[next(len(names))]
		switch next(4) {
		case 0:
			m.AddVertex(u)
		case 1:
			m.RemoveVertex(u)
		case 2:
			if u != v {
				m.AddEdge(u, v)
			}
		case 3:
			m.RemoveEdge(u, v)
		}
		assertConsistent(t, m, step)
	}
}

func assertConsistent(t *testing.T, m *Maintainer, step int) {
	t.Helper()
	// Recompute SCCs from scratch with Tarjan over the raw adjacency.
	want := map[string]string{} // node -> comp id (min member)
	index := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	counter := 0
	var visit func(v string)
	visit = func(v string) {
		index[v], low[v] = counter, counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for w := range m.adj[v] {
			if _, seen := index[w]; !seen {
				visit(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] == index[v] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			min := scc[0]
			for _, n := range scc {
				if n < min {
					min = n
				}
			}
			for _, n := range scc {
				want[n] = min
			}
		}
	}
	for v := range m.adj {
		if _, seen := index[v]; !seen {
			visit(v)
		}
	}
	for node, cid := range want {
		if m.comp[node] != cid {
			t.Fatalf("step %d: node %s in comp %q, want %q", step, node, m.comp[node], cid)
		}
	}
	if len(want) != len(m.comp) {
		t.Fatalf("step %d: tracked %d nodes, want %d", step, len(m.comp), len(want))
	}
}
