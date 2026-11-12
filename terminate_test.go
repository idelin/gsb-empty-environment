package netsec

import "testing"

// TestSelfLoopTermination: a tight self-loop over a write node must reach a
// fixpoint rather than diverge, and must distinguish an unrelated write (no
// gap) from a write of the checked field (gap).
func TestSelfLoopTermination(t *testing.T) {
	graph := ObjectGraph{Roots: map[string]string{"a": "B", "b": "B"}}

	build := func(writeField string) *Program {
		return &Program{
			Graph: graph,
			Nodes: []Node{
				{ID: "e", Kind: NodeEntry},
				{ID: "c", Kind: NodeCheck, Root: "a", Field: "len"},
				{ID: "w", Kind: NodeWrite, Root: "b", Field: writeField},
				{ID: "r", Kind: NodeRead, Root: "a", Field: "len"},
			},
			CFG: CFG{
				Entry: "e",
				Successors: map[string][]string{
					"e": {"c"},
					"c": {"w"},
					"w": {"w", "r"}, // self-loop plus exit
					"r": nil,
				},
			},
		}
	}

	if g := findGap(Analyze(build("tag")), "r", "B", "len"); g != nil {
		t.Fatalf("unrelated self-loop write must not invalidate: %+v", g)
	}
	g := findGap(Analyze(build("len")), "r", "B", "len")
	if g == nil || g.Kind != GapInvalidated || g.Invalidator != "w" {
		t.Fatalf("self-loop len write must invalidate, got %+v", g)
	}
}

// TestResolveCycleFinite: a cyclic object graph must resolve to a finite,
// deduplicated target set.
func TestResolveCycleFinite(t *testing.T) {
	graph := ObjectGraph{Roots: map[string]string{"head": "L0"}}
	graph = edge(graph, "L0", "next", "L1")
	graph = edge(graph, "L1", "next", "L0")

	got := graph.Resolve("head", "next.next.next.len")
	want := []Target{{Object: "L1", Field: "len"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("cyclic resolve = %+v, want %+v", got, want)
	}
}
