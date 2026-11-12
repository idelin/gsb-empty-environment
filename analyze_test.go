package netsec

import (
	"reflect"
	"testing"
)

// findGap returns the gap for the given read/target, or nil.
func findGap(res *Result, readID, object, field string) *Gap {
	for i := range res.Gaps {
		g := &res.Gaps[i]
		if g.Read == readID && g.Target.Object == object && g.Target.Field == field {
			return g
		}
	}
	return nil
}

func edge(graph ObjectGraph, obj, field, dst string) ObjectGraph {
	if graph.Edges == nil {
		graph.Edges = map[string]map[string]string{}
	}
	if graph.Edges[obj] == nil {
		graph.Edges[obj] = map[string]string{}
	}
	graph.Edges[obj][field] = dst
	return graph
}

// TestDifferentFieldPrecision: writing a different field of the same object
// must not invalidate a length fact; writing the length field (through another
// alias) must.
func TestDifferentFieldPrecision(t *testing.T) {
	// shared object B with scalar fields "len" and "cap".
	graph := ObjectGraph{Roots: map[string]string{"a": "B", "other": "B"}}

	prog := &Program{
		Graph: graph,
		Nodes: []Node{
			{ID: "e", Kind: NodeEntry},
			{ID: "c", Kind: NodeCheck, Root: "a", Field: "len"},
			// unrelated field: must keep the fact valid.
			{ID: "wcap", Kind: NodeWrite, Root: "other", Field: "cap"},
			{ID: "r1", Kind: NodeRead, Root: "a", Field: "len"},
			// length write through the alias: must invalidate.
			{ID: "wlen", Kind: NodeWrite, Root: "other", Field: "len"},
			{ID: "r2", Kind: NodeRead, Root: "a", Field: "len"},
		},
		CFG: CFG{
			Entry: "e",
			Successors: map[string][]string{
				"e":    {"c"},
				"c":    {"wcap"},
				"wcap": {"r1"},
				"r1":   {"wlen"},
				"wlen": {"r2"},
			},
		},
	}

	res := Analyze(prog)
	if g := findGap(res, "r1", "B", "len"); g != nil {
		t.Fatalf("different-field write should not invalidate: got gap %+v", g)
	}
	g := findGap(res, "r2", "B", "len")
	if g == nil {
		t.Fatal("alias length write must invalidate the length fact")
	}
	if g.Kind != GapInvalidated || g.Invalidator != "wlen" {
		t.Fatalf("expected invalidated gap by wlen, got %+v", g)
	}
	wantPath := []string{"e", "c", "wcap", "r1", "wlen", "r2"}
	if !reflect.DeepEqual(g.Path, wantPath) {
		t.Fatalf("gap path = %v, want %v", g.Path, wantPath)
	}
}

// TestRecursiveAlias: a linked-list-like structure where node.next is a
// reference. Writes reached only through the recursive alias must invalidate
// the exact shared field, while a distinct node's field stays valid.
func TestRecursiveAlias(t *testing.T) {
	// L0.next -> L1 ; L1.next -> L1 (recursive/self alias).
	graph := ObjectGraph{Roots: map[string]string{"head": "L0", "alias": "L1"}}
	graph = edge(graph, "L0", "next", "L1")
	graph = edge(graph, "L1", "next", "L1")

	prog := &Program{
		Graph: graph,
		Nodes: []Node{
			{ID: "e", Kind: NodeEntry},
			// check head.next.len -> L1.len
			{ID: "c", Kind: NodeCheck, Root: "head", Field: "next.len"},
			// write alias.len -> L1.len via the recursive alias.
			{ID: "w", Kind: NodeWrite, Root: "alias", Field: "len"},
			{ID: "r", Kind: NodeRead, Root: "head", Field: "next.len"},
		},
		CFG: CFG{
			Entry: "e",
			Successors: map[string][]string{
				"e": {"c"}, "c": {"w"}, "w": {"r"},
			},
		},
	}

	res := Analyze(prog)
	g := findGap(res, "r", "L1", "len")
	if g == nil {
		t.Fatal("recursive alias write must invalidate the shared field")
	}
	if g.Invalidator != "w" {
		t.Fatalf("expected invalidator w, got %+v", g)
	}

	// Precision: writing L1.tag must not invalidate L1.len.
	prog2 := *prog
	prog2.Nodes = []Node{
		{ID: "e", Kind: NodeEntry},
		{ID: "c", Kind: NodeCheck, Root: "head", Field: "next.len"},
		{ID: "w", Kind: NodeWrite, Root: "alias", Field: "tag"},
		{ID: "r", Kind: NodeRead, Root: "head", Field: "next.len"},
	}
	if g := findGap(Analyze(&prog2), "r", "L1", "len"); g != nil {
		t.Fatalf("distinct field on aliased object must not invalidate: %+v", g)
	}
}
