package graphcontract

import (
	"reflect"
	"sort"
	"testing"
)

// sampleGraph builds a directed multigraph on nodes 0..5 with parallel
// edges, a self-loop, and two attribute keys (some edges lack "cost").
//
//	e0: 0->1  e1: 0->1 (parallel)  e2: 1->2  e3: 2->0  e9: 2->2 (loop)
//	e4: 3->4  e5: 4->5  e6: 5->3
//	e7: 0->3  e8: 1->4 (cross-group, parallel after contraction)
func sampleGraph() *Graph {
	g := New(true)
	g.AddEdge(0, 1, Attributes{"w": 1, "cost": 10}) // e0
	g.AddEdge(0, 1, Attributes{"w": 2})             // e1
	g.AddEdge(1, 2, Attributes{"w": 3, "cost": 30}) // e2
	g.AddEdge(2, 0, Attributes{"w": 4})             // e3
	g.AddEdge(3, 4, Attributes{"w": 5, "cost": 50}) // e4
	g.AddEdge(4, 5, Attributes{"w": 6})             // e5
	g.AddEdge(5, 3, Attributes{"w": 7, "cost": 70}) // e6
	g.AddEdge(0, 3, Attributes{"w": 8})             // e7
	g.AddEdge(1, 4, Attributes{"w": 9, "cost": 90}) // e8
	g.AddEdge(2, 2, Attributes{"w": 10})            // e9
	return g
}

// samplePartition groups {0,1,2} -> 0 and {3,4,5} -> 1.
func samplePartition(t *testing.T) *Partition {
	t.Helper()
	p, err := NewPartition([][]NodeID{{0, 1, 2}, {3, 4, 5}})
	if err != nil {
		t.Fatalf("NewPartition: %v", err)
	}
	return p
}

func sortedIDs(ids []EdgeID) []EdgeID {
	out := make([]EdgeID, len(ids))
	copy(out, ids)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func TestContractPreservesParallelEdgesAndProvenance(t *testing.T) {
	g := sampleGraph()
	p := samplePartition(t)

	cg, m, err := Contract(g, p, ContractOptions{})
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if cg.NumNodes() != 2 {
		t.Fatalf("contracted nodes = %d, want 2", cg.NumNodes())
	}
	// Without KeepSelfLoops only the two cross-group edges remain, and they
	// stay parallel (not merged).
	if cg.NumEdges() != 2 {
		t.Fatalf("contracted edges = %d, want 2", cg.NumEdges())
	}
	for _, e := range cg.Edges() {
		if e.From != 0 || e.To != 1 {
			t.Fatalf("edge %d endpoints = %d->%d, want 0->1", e.ID, e.From, e.To)
		}
	}
	if got := m.Sources(0); !reflect.DeepEqual(got, []EdgeID{7}) {
		t.Fatalf("Sources(0) = %v, want [7]", got)
	}
	if got := m.Sources(1); !reflect.DeepEqual(got, []EdgeID{8}) {
		t.Fatalf("Sources(1) = %v, want [8]", got)
	}
	// Every in-group edge, including the original self-loop, is dropped.
	wantDropped := []EdgeID{0, 1, 2, 3, 4, 5, 6, 9}
	if got := m.DroppedEdges(); !reflect.DeepEqual(got, wantDropped) {
		t.Fatalf("DroppedEdges = %v, want %v", got, wantDropped)
	}
}

func TestContractSelfLoopsKeptOrDropped(t *testing.T) {
	g := sampleGraph()
	p := samplePartition(t)

	kept, m, err := Contract(g, p, ContractOptions{KeepSelfLoops: true})
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if kept.NumEdges() != g.NumEdges() {
		t.Fatalf("kept edges = %d, want %d (one per original edge)", kept.NumEdges(), g.NumEdges())
	}
	if len(m.DroppedEdges()) != 0 {
		t.Fatalf("DroppedEdges = %v, want none", m.DroppedEdges())
	}
	loops := 0
	for _, e := range kept.Edges() {
		if e.From == e.To {
			loops++
		}
	}
	// In-group edges become self-loops of their contracted node.
	if loops != 8 {
		t.Fatalf("self-loops = %d, want 8", loops)
	}

	dropped, m2, err := Contract(g, p, ContractOptions{KeepSelfLoops: false})
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	for _, e := range dropped.Edges() {
		if e.From == e.To {
			t.Fatalf("edge %d is a self-loop but KeepSelfLoops=false", e.ID)
		}
	}
	if len(m2.DroppedEdges()) != 8 {
		t.Fatalf("dropped = %d, want 8", len(m2.DroppedEdges()))
	}
}

func TestContractCompositionEqualsComposedPartition(t *testing.T) {
	for _, merge := range []bool{false, true} {
		for _, keepLoops := range []bool{false, true} {
			opts := ContractOptions{KeepSelfLoops: keepLoops, MergeParallel: merge}
			g := sampleGraph()
			p1 := samplePartition(t)
			p2, err := NewPartition([][]NodeID{{0, 1}}) // merge both groups into one
			if err != nil {
				t.Fatalf("NewPartition p2: %v", err)
			}

			// Two successive contractions.
			g1, m1, err := Contract(g, p1, opts)
			if err != nil {
				t.Fatalf("first Contract: %v", err)
			}
			g2, m2, err := Contract(g1, p2, opts)
			if err != nil {
				t.Fatalf("second Contract: %v", err)
			}
			composed, err := m1.Then(m2)
			if err != nil {
				t.Fatalf("Then: %v", err)
			}

			// One contraction with the composed partition.
			p12, err := ComposePartitions(p1, p2)
			if err != nil {
				t.Fatalf("ComposePartitions: %v", err)
			}
			g2want, want, err := Contract(g, p12, opts)
			if err != nil {
				t.Fatalf("composed Contract: %v", err)
			}

			if g2.NumNodes() != g2want.NumNodes() || g2.NumEdges() != g2want.NumEdges() {
				t.Fatalf("merge=%v keepLoops=%v: shape (%d nodes, %d edges), want (%d, %d)",
					merge, keepLoops, g2.NumNodes(), g2.NumEdges(), g2want.NumNodes(), g2want.NumEdges())
			}
			// Node provenance must agree.
			for _, n := range g.Nodes() {
				gotGroup, _ := composed.GroupOf(n)
				wantGroup, _ := want.GroupOf(n)
				if gotGroup != wantGroup {
					t.Fatalf("merge=%v keepLoops=%v: node %d group = %d, want %d",
						merge, keepLoops, n, gotGroup, wantGroup)
				}
			}
			// Edge provenance must agree edge by edge (as sets: merge order
			// can differ between one-step and two-step contraction).
			for _, e := range g2want.Edges() {
				if !reflect.DeepEqual(sortedIDs(composed.Sources(e.ID)), sortedIDs(want.Sources(e.ID))) {
					t.Fatalf("merge=%v keepLoops=%v: sources of edge %d = %v, want %v",
						merge, keepLoops, e.ID, composed.Sources(e.ID), want.Sources(e.ID))
				}
			}
			if !reflect.DeepEqual(sortedIDs(composed.DroppedEdges()), sortedIDs(want.DroppedEdges())) {
				t.Fatalf("merge=%v keepLoops=%v: dropped = %v, want %v",
					merge, keepLoops, composed.DroppedEdges(), want.DroppedEdges())
			}
		}
	}
}

func TestPartitionRejectsInvalidInput(t *testing.T) {
	if _, err := NewPartition([][]NodeID{{0, 1}, {}}); err == nil {
		t.Fatal("empty group not rejected")
	}
	if _, err := NewPartition([][]NodeID{}); err == nil {
		t.Fatal("empty partition not rejected")
	}
	if _, err := NewPartition([][]NodeID{{0, 1}, {1, 2}}); err == nil {
		t.Fatal("duplicate node not rejected")
	}

	g := sampleGraph()
	// Missing node 5.
	missing, err := NewPartition([][]NodeID{{0, 1, 2}, {3, 4}})
	if err != nil {
		t.Fatalf("NewPartition: %v", err)
	}
	if _, _, err := Contract(g, missing, ContractOptions{}); err == nil {
		t.Fatal("partition not covering all nodes not rejected")
	}
	// Unknown node 9.
	extra, err := NewPartition([][]NodeID{{0, 1, 2}, {3, 4, 5, 9}})
	if err != nil {
		t.Fatalf("NewPartition: %v", err)
	}
	if _, _, err := Contract(g, extra, ContractOptions{}); err == nil {
		t.Fatal("partition with unknown node not rejected")
	}
}

// TestContractEdgeByEdgeAgainstOriginal verifies the mapping by replaying
// every edge of the original graph: each edge must land in exactly one
// contracted edge (or the dropped set), and aggregated attributes must equal
// the per-key sums over the sources.
func TestContractEdgeByEdgeAgainstOriginal(t *testing.T) {
	g := sampleGraph()
	p := samplePartition(t)
	cg, m, err := Contract(g, p, ContractOptions{KeepSelfLoops: true, MergeParallel: true})
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}

	// Independently replay the original edges through the partition.
	type key struct{ from, to int }
	wantSources := make(map[key][]EdgeID)
	seen := make(map[EdgeID]bool)
	for _, e := range g.Edges() {
		from, _ := p.GroupOf(e.From)
		to, _ := p.GroupOf(e.To)
		wantSources[key{from, to}] = append(wantSources[key{from, to}], e.ID)
	}
	if cg.NumEdges() != len(wantSources) {
		t.Fatalf("contracted edges = %d, want %d", cg.NumEdges(), len(wantSources))
	}
	for _, e := range cg.Edges() {
		src := m.Sources(e.ID)
		want := wantSources[key{int(e.From), int(e.To)}]
		if !reflect.DeepEqual(src, want) {
			t.Fatalf("edge %d (%d->%d) sources = %v, want %v", e.ID, e.From, e.To, src, want)
		}
		// Attribute aggregation equals per-key sums over the source edges.
		wantAttrs := SumAttributes(edgeAttrs(g, src))
		if !reflect.DeepEqual(e.Attrs, wantAttrs) {
			t.Fatalf("edge %d attrs = %v, want %v", e.ID, e.Attrs, wantAttrs)
		}
		for _, s := range src {
			if seen[s] {
				t.Fatalf("original edge %d appears in two contracted edges", s)
			}
			seen[s] = true
		}
	}
	if len(seen) != g.NumEdges() {
		t.Fatalf("mapped %d original edges, want %d", len(seen), g.NumEdges())
	}

	// Spot-check concrete sums, including a key missing on some sources.
	for _, e := range cg.Edges() {
		if e.From == 0 && e.To == 1 {
			if e.Attrs["w"] != 17 || e.Attrs["cost"] != 90 {
				t.Fatalf("cross edge attrs = %v, want w=17 cost=90", e.Attrs)
			}
		}
	}
}

func edgeAttrs(g *Graph, ids []EdgeID) []Attributes {
	out := make([]Attributes, len(ids))
	for i, id := range ids {
		out[i] = g.Edge(id).Attrs
	}
	return out
}

func TestMappingExpansion(t *testing.T) {
	g := sampleGraph()
	p := samplePartition(t)
	cg, m, err := Contract(g, p, ContractOptions{KeepSelfLoops: true, MergeParallel: true})
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if got := m.Members(0); !reflect.DeepEqual(got, []NodeID{0, 1, 2}) {
		t.Fatalf("Members(0) = %v, want [0 1 2]", got)
	}
	if got := m.Members(1); !reflect.DeepEqual(got, []NodeID{3, 4, 5}) {
		t.Fatalf("Members(1) = %v, want [3 4 5]", got)
	}
	// Every contracted node maps back to a non-empty member set covering all
	// original nodes exactly once.
	covered := make(map[NodeID]int)
	for _, cn := range cg.Nodes() {
		for _, n := range m.Members(int(cn)) {
			covered[n]++
		}
	}
	if len(covered) != g.NumNodes() {
		t.Fatalf("expansion covers %d nodes, want %d", len(covered), g.NumNodes())
	}
	for n, count := range covered {
		if count != 1 {
			t.Fatalf("node %d covered %d times", n, count)
		}
	}
}

func TestContractUndirectedMergesBothDirections(t *testing.T) {
	g := New(false)
	g.AddEdge(0, 1, Attributes{"w": 1})
	g.AddEdge(1, 0, Attributes{"w": 2})
	p, err := NewPartition([][]NodeID{{0}, {1}})
	if err != nil {
		t.Fatalf("NewPartition: %v", err)
	}
	cg, m, err := Contract(g, p, ContractOptions{MergeParallel: true})
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if cg.NumEdges() != 1 {
		t.Fatalf("edges = %d, want 1", cg.NumEdges())
	}
	e := cg.Edge(0)
	if e.Attrs["w"] != 3 {
		t.Fatalf("w = %v, want 3", e.Attrs["w"])
	}
	if got := sortedIDs(m.Sources(0)); !reflect.DeepEqual(got, []EdgeID{0, 1}) {
		t.Fatalf("sources = %v, want [0 1]", got)
	}
}
