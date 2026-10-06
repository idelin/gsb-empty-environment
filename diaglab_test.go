package diaglab

import (
	"strings"
	"testing"
)

func demoGraph() *Graph {
	g := NewGraph()
	g.AddEdge("A", "B")
	g.AddEdge("B", "C")
	g.AddEdge("C", "A")
	g.AddEdge("C", "D")
	g.AddEdge("D", "E")
	g.AddEdge("F", "E")
	g.AddEdge("G", "H")
	g.AddEdge("H", "G")
	return g
}

func TestSCCCompression(t *testing.T) {
	g := demoGraph()
	var cyc []string
	for _, comp := range g.SCCs() {
		if len(comp) == 3 {
			cyc = comp
		}
	}
	if strings.Join(cyc, ",") != "A,B,C" {
		t.Fatalf("cycle A,B,C not compressed into one SCC: %v", cyc)
	}
}

func TestMinRootCover(t *testing.T) {
	res, err := MinRootCover(CoverRequest{
		G:          demoGraph(),
		Observed:   []string{"D", "E", "H"},
		Candidates: []string{"A", "B", "F", "G"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.MinSize != 2 {
		t.Fatalf("MinSize = %d, want 2", res.MinSize)
	}
	if got := strings.Join(res.Roots, ","); got != "A,G" {
		t.Fatalf("Roots = %v, want [A G]", res.Roots)
	}
	if len(res.Uncovered) != 0 {
		t.Fatalf("uncovered: %v", res.Uncovered)
	}
	if !res.VerifiedOptimal {
		t.Fatal("brute-force enumeration disagrees with branch and bound")
	}
	if !res.CycleCheckPassed {
		t.Fatal("an optimal node-level solution splits a cycle into multiple roots")
	}
	// Evidence paths must be real paths in the graph.
	g := demoGraph()
	for _, ev := range res.Evidence {
		for _, c := range ev.Covers {
			if len(c.Path) == 0 || c.Path[0] != ev.Root || c.Path[len(c.Path)-1] != c.Observed {
				t.Fatalf("bad witness path %v for %s -> %s", c.Path, ev.Root, c.Observed)
			}
			if _, ok := g.Path(ev.Root, c.Observed); !ok {
				t.Fatalf("no real path %s -> %s", ev.Root, c.Observed)
			}
		}
	}
}

func TestCoverUnreacheableObserved(t *testing.T) {
	g := NewGraph()
	g.AddEdge("X", "Y")
	g.AddNode("Z")
	res, err := MinRootCover(CoverRequest{G: g, Observed: []string{"Z"}, Candidates: []string{"X"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.MinSize != -1 || len(res.Uncovered) != 1 {
		t.Fatalf("want infeasible with uncovered Z, got %+v", res)
	}
}

func TestTooManyCandidates(t *testing.T) {
	g := NewGraph()
	var cands []string
	for i := 0; i < MaxCandidates+1; i++ {
		n := strings.Repeat("n", 1) + string(rune('a'+i))
		g.AddNode(n)
		cands = append(cands, n)
	}
	if _, err := MinRootCover(CoverRequest{G: g, Observed: cands[:1], Candidates: cands}); err == nil {
		t.Fatal("expected candidate-limit error")
	}
}

func demoWorlds() ([]World, []Test) {
	worlds := []World{
		{Name: "w0", RootSet: []string{"A"}},
		{Name: "w1", RootSet: []string{"B"}},
		{Name: "w2", RootSet: []string{"G"}},
		{Name: "w3", RootSet: []string{"A", "G"}},
		{Name: "w4", RootSet: []string{"B", "G"}},
		{Name: "w5", RootSet: []string{"F"}},
	}
	inSet := func(w int, root string) bool {
		for _, r := range worlds[w].RootSet {
			if r == root {
				return true
			}
		}
		return false
	}
	probe := func(root string) func(int) string {
		return func(w int) string {
			if inSet(w, root) {
				return root + "+"
			}
			return root + "-"
		}
	}
	tests := []Test{
		{Name: "probeF", Cost: 1, Outcome: probe("F")},
		{Name: "probeA", Cost: 2, Outcome: probe("A")},
		{Name: "probeB", Cost: 2, Outcome: probe("B")},
		{Name: "probeG", Cost: 3, Outcome: probe("G")},
		{Name: "fullTrace", Cost: 9, Outcome: func(w int) string {
			return "trace=" + strings.Join(worlds[w].RootSet, "+")
		}},
	}
	return worlds, tests
}

func TestOptimalPolicyAndReplay(t *testing.T) {
	worlds, tests := demoWorlds()
	p, err := OptimalPolicy(worlds, tests)
	if err != nil {
		t.Fatal(err)
	}
	if p.WorstCost != 7 {
		t.Fatalf("WorstCost = %d, want 7", p.WorstCost)
	}
	recs, err := Replay(p, worlds, tests)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if !r.OK {
			t.Fatalf("world %s: identified %v, want %v", r.World, r.Identified, r.Expected)
		}
	}
	if got := WorstReplayCost(recs); got != p.WorstCost {
		t.Fatalf("replay worst %d != DP worst %d", got, p.WorstCost)
	}
}

func TestPolicyPrefersCheapAdaptiveOverExpensivePerfect(t *testing.T) {
	worlds, tests := demoWorlds()
	p, err := OptimalPolicy(worlds, tests)
	if err != nil {
		t.Fatal(err)
	}
	if p.Test == "fullTrace" {
		t.Fatal("clairvoyant one-shot test chosen despite higher worst cost")
	}
}

func TestIndistinguishableWorlds(t *testing.T) {
	worlds := []World{
		{Name: "w0", RootSet: []string{"A"}},
		{Name: "w1", RootSet: []string{"B"}},
	}
	tests := []Test{{Name: "useless", Cost: 1, Outcome: func(int) string { return "same" }}}
	if _, err := OptimalPolicy(worlds, tests); err == nil {
		t.Fatal("expected infeasible error")
	}
}

func TestSharedRootSetNeedsNoSeparation(t *testing.T) {
	worlds := []World{
		{Name: "w0", RootSet: []string{"A"}},
		{Name: "w1", RootSet: []string{"A"}},
		{Name: "w2", RootSet: []string{"B"}},
	}
	tests := []Test{{Name: "probe", Cost: 4, Outcome: func(w int) string {
		if w == 2 {
			return "no"
		}
		return "yes"
	}}}
	p, err := OptimalPolicy(worlds, tests)
	if err != nil {
		t.Fatal(err)
	}
	if p.WorstCost != 4 {
		t.Fatalf("WorstCost = %d, want 4", p.WorstCost)
	}
}

func TestWorldLimit(t *testing.T) {
	var worlds []World
	for i := 0; i < MaxWorlds+1; i++ {
		worlds = append(worlds, World{Name: string(rune('a' + i)), RootSet: []string{"A"}})
	}
	if _, err := OptimalPolicy(worlds, nil); err == nil {
		t.Fatal("expected world-limit error")
	}
}
