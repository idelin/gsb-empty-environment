package netsec

import "testing"

// TestLoopFixpoint: the check dominates a loop. On iterations where the body
// does not write the checked field, the fact survives; a write inside the body
// makes the post-loop read a gap with a path through the back edge.
func TestLoopFixpoint(t *testing.T) {
	graph := ObjectGraph{Roots: map[string]string{"a": "B", "b": "B"}}

	prog := &Program{
		Graph: graph,
		Nodes: []Node{
			{ID: "e", Kind: NodeEntry},
			{ID: "c", Kind: NodeCheck, Root: "a", Field: "len"},
			{ID: "j", Kind: NodeJoin},
			{ID: "wother", Kind: NodeWrite, Root: "b", Field: "tag"},
			{ID: "back", Kind: NodeJoin},
			{ID: "w", Kind: NodeWrite, Root: "b", Field: "len"},
			{ID: "r", Kind: NodeRead, Root: "a", Field: "len"},
			{ID: "x", Kind: NodeExit},
		},
		CFG: CFG{
			Entry: "e",
			Successors: map[string][]string{
				"e":      {"c"},
				"c":      {"j"},
				"j":      {"wother", "r"},
				"wother": {"back"},
				"back":   {"w"},
				"w":      {"j"}, // back edge
				"r":      {"x"},
			},
		},
	}

	res := Analyze(prog)

	// Fixpoint must be stable and finite: re-running yields identical gaps.
	again := Analyze(prog)
	if len(again.Gaps) != len(res.Gaps) {
		t.Fatalf("analysis not deterministic: %d vs %d gaps", len(again.Gaps), len(res.Gaps))
	}

	g := findGap(res, "r", "B", "len")
	if g == nil {
		t.Fatal("post-loop read after a possible in-loop write must be a gap")
	}
	if g.Kind != GapInvalidated || g.Invalidator != "w" {
		t.Fatalf("expected invalidation by w, got %+v", g)
	}
	// The witness path must traverse the back edge via the loop header.
	if !containsInOrder(g.Path, []string{"c", "j", "wother", "back", "w", "j", "r"}) {
		t.Fatalf("gap path does not traverse the loop: %v", g.Path)
	}

	// Precision: remove the len write; the fact survives every iteration.
	prog2 := *prog
	prog2.Nodes = append([]Node(nil), prog.Nodes...)
	for i := range prog2.Nodes {
		if prog2.Nodes[i].ID == "w" {
			// Same loop shape, but the body writes an unrelated field.
			prog2.Nodes[i] = Node{ID: "w", Kind: NodeWrite, Root: "b", Field: "tag"}
		}
	}
	if g := findGap(Analyze(&prog2), "r", "B", "len"); g != nil {
		t.Fatalf("unrelated in-loop write must not invalidate: %+v", g)
	}
}

// TestCallSummary: a callee summary's possible write set is matched to caller
// roots via argument bindings and invalidates through aliases.
func TestCallSummary(t *testing.T) {
	graph := ObjectGraph{Roots: map[string]string{"a": "B", "p": "B", "q": "C"}}

	prog := &Program{
		Graph: graph,
		Nodes: []Node{
			{ID: "e", Kind: NodeEntry},
			{ID: "c", Kind: NodeCheck, Root: "a", Field: "len"},
			// callee writes only p.tag; bound p->a shares B, but tag != len.
			{ID: "call1", Kind: NodeCall, Summary: "S1"},
			{ID: "r1", Kind: NodeRead, Root: "a", Field: "len"},
			// callee writes p.len through the alias.
			{ID: "call2", Kind: NodeCall, Summary: "S2"},
			{ID: "r2", Kind: NodeRead, Root: "a", Field: "len"},
		},
		CFG: CFG{
			Entry: "e",
			Successors: map[string][]string{
				"e":     {"c"},
				"c":     {"call1"},
				"call1": {"r1"},
				"r1":    {"call2"},
				"call2": {"r2"},
			},
		},
		Calls: []CallSite{
			{NodeID: "call1", Bindings: []ArgumentBinding{{Param: "p", Root: "p"}, {Param: "q", Root: "q"}}},
			{NodeID: "call2", Bindings: []ArgumentBinding{{Param: "p", Root: "p"}}},
		},
		Summaries: []CallSummary{
			{ID: "S1", MayWrites: []RootWrite{{Param: "p", Field: "tag"}, {Param: "q", Field: "len"}}},
			{ID: "S2", MayWrites: []RootWrite{{Param: "p", Field: "len"}}},
		},
	}

	res := Analyze(prog)
	if g := findGap(res, "r1", "B", "len"); g != nil {
		t.Fatalf("summary writing distinct field/object must not invalidate: %+v", g)
	}
	g := findGap(res, "r2", "B", "len")
	if g == nil || g.Kind != GapInvalidated || g.Invalidator != "call2" {
		t.Fatalf("expected call2 invalidation, got %+v", g)
	}
}

// TestUncheckedRead: a read with no dominating check at all is reported as an
// unchecked gap, not silently dropped.
func TestUncheckedRead(t *testing.T) {
	prog := &Program{
		Graph: ObjectGraph{Roots: map[string]string{"a": "B"}},
		Nodes: []Node{
			{ID: "e", Kind: NodeEntry},
			{ID: "r", Kind: NodeRead, Root: "a", Field: "len"},
		},
		CFG: CFG{Entry: "e", Successors: map[string][]string{"e": {"r"}}},
	}
	g := findGap(Analyze(prog), "r", "B", "len")
	if g == nil || g.Kind != GapUnchecked || g.Invalidator != "" {
		t.Fatalf("expected unchecked gap, got %+v", g)
	}
	if len(g.Path) != 2 || g.Path[0] != "e" || g.Path[1] != "r" {
		t.Fatalf("unchecked path = %v", g.Path)
	}
}

func containsInOrder(path, want []string) bool {
	i := 0
	for _, id := range path {
		if i < len(want) && id == want[i] {
			i++
		}
	}
	return i == len(want)
}

func cloneSuccessors(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in))
	for k, v := range in {
		cp := make([]string, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}
