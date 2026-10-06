package graphx

import (
	"math/rand"
	"reflect"
	"testing"
)

func mustContract(t *testing.T, g *Graph, p Partition, opts Options) *Contraction {
	t.Helper()
	c, err := Contract(g, p, opts)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	return c
}

func partitionReason(t *testing.T, err error) error {
	t.Helper()
	pe, ok := err.(*PartitionError)
	if !ok {
		t.Fatalf("got %T (%v), want *PartitionError", err, err)
	}
	return pe.Err
}

func TestContractRejectsEmptyGroup(t *testing.T) {
	g := New(true)
	g.AddNode(1)
	g.AddNode(2)
	_, err := Contract(g, Partition{{1, 2}, {}}, Options{})
	if err == nil {
		t.Fatal("expected error for empty group")
	}
	if got := partitionReason(t, err); got != ErrEmptyGroup {
		t.Fatalf("reason = %v, want %v", got, ErrEmptyGroup)
	}
}

func TestContractRejectsInvalidPartitions(t *testing.T) {
	g := New(true)
	for _, n := range []int{1, 2, 3} {
		g.AddNode(n)
	}
	cases := []struct {
		name string
		p    Partition
		want error
	}{
		{"unknown node", Partition{{1, 2}, {3, 99}}, ErrUnknownNode},
		{"duplicate node", Partition{{1, 2}, {2, 3}}, ErrDuplicateNode},
		{"unassigned node", Partition{{1, 2}}, ErrUnassignedNode},
	}
	for _, tc := range cases {
		_, err := Contract(g, tc.p, Options{})
		if err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
		if got := partitionReason(t, err); got != tc.want {
			t.Fatalf("%s: reason = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Two parallel edges crossing the same group boundary must survive as two
// contracted edges, each pointing back at its own source edge.
func TestContractPreservesParallelEdgeSources(t *testing.T) {
	g := New(true)
	for _, n := range []int{1, 2, 3, 4} {
		g.AddNode(n)
	}
	e0, _ := g.AddEdge(1, 3, Attrs{"w": 1.0})
	e1, _ := g.AddEdge(2, 3, Attrs{"w": 2.0}) // parallel to e0 after contraction
	e2, _ := g.AddEdge(1, 2, Attrs{"w": 5.0}) // intra-group, dropped
	e3, _ := g.AddEdge(4, 3, Attrs{"w": 3.0})

	c := mustContract(t, g, Partition{{1, 2}, {3}, {4}}, Options{})
	if c.Graph.NumNodes() != 3 {
		t.Fatalf("contracted nodes = %d, want 3", c.Graph.NumNodes())
	}
	if c.Graph.NumEdges() != 3 {
		t.Fatalf("contracted edges = %d, want 3", c.Graph.NumEdges())
	}
	if c.EdgeMap[e2] != -1 {
		t.Fatalf("intra-group edge %d mapped to %d, want -1", e2, c.EdgeMap[e2])
	}
	ce0, ce1 := c.EdgeMap[e0], c.EdgeMap[e1]
	if ce0 == ce1 {
		t.Fatalf("parallel source edges merged into edge %d", ce0)
	}
	for _, ce := range []int{ce0, ce1} {
		if e := c.Graph.Edge(ce); e.From != 0 || e.To != 1 {
			t.Fatalf("contracted edge %d = %d->%d, want 0->1", ce, e.From, e.To)
		}
	}
	if got := c.EdgeSources[ce0]; !reflect.DeepEqual(got, []int{e0}) {
		t.Fatalf("sources of %d = %v, want [%d]", ce0, got, e0)
	}
	if got := c.EdgeSources[ce1]; !reflect.DeepEqual(got, []int{e1}) {
		t.Fatalf("sources of %d = %v, want [%d]", ce1, got, e1)
	}
	if got := c.EdgeSources[c.EdgeMap[e3]]; !reflect.DeepEqual(got, []int{e3}) {
		t.Fatalf("sources of e3 mapping = %v, want [%d]", got, e3)
	}
	if err := c.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestContractSelfLoopsOption(t *testing.T) {
	build := func() *Graph {
		g := New(true)
		for _, n := range []int{1, 2, 3} {
			g.AddNode(n)
		}
		g.AddEdge(1, 2, nil) // intra-group
		g.AddEdge(2, 1, nil) // intra-group
		g.AddEdge(1, 3, nil) // cross-group
		return g
	}
	p := Partition{{1, 2}, {3}}

	kept := mustContract(t, build(), p, Options{KeepSelfLoops: true})
	if kept.Graph.NumEdges() != 3 {
		t.Fatalf("kept edges = %d, want 3", kept.Graph.NumEdges())
	}
	loops := 0
	for _, e := range kept.Graph.Edges() {
		if e.From == e.To {
			loops++
		}
	}
	if loops != 2 {
		t.Fatalf("self-loops = %d, want 2", loops)
	}
	if err := kept.Verify(); err != nil {
		t.Fatalf("Verify kept: %v", err)
	}

	dropped := mustContract(t, build(), p, Options{})
	if dropped.Graph.NumEdges() != 1 {
		t.Fatalf("remaining edges = %d, want 1", dropped.Graph.NumEdges())
	}
	for _, e := range build().Edges() {
		_ = e
	}
	if dropped.EdgeMap[0] != -1 || dropped.EdgeMap[1] != -1 {
		t.Fatalf("intra-group edges not dropped: %v", dropped.EdgeMap)
	}
	if dropped.EdgeMap[2] != 0 {
		t.Fatalf("cross-group edge mapped to %d, want 0", dropped.EdgeMap[2])
	}
	if err := dropped.Verify(); err != nil {
		t.Fatalf("Verify dropped: %v", err)
	}
}

func sameProvenance(a, b *Contraction) bool {
	return reflect.DeepEqual(a.NodeMap, b.NodeMap) &&
		reflect.DeepEqual(a.Groups, b.Groups) &&
		reflect.DeepEqual(a.EdgeMap, b.EdgeMap) &&
		reflect.DeepEqual(a.EdgeSources, b.EdgeSources)
}

// Contracting twice and expanding must equal contracting once by the
// composed partition.
func checkComposition(t *testing.T, g *Graph, inner, outer Partition, keep bool) {
	t.Helper()
	opts := Options{KeepSelfLoops: keep}

	c1 := mustContract(t, g, inner, opts)
	c2 := mustContract(t, c1.Graph, outer, opts)
	expanded, err := c2.Expand(c1)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if err := expanded.Verify(); err != nil {
		t.Fatalf("Verify expanded: %v", err)
	}

	composed, err := Compose(inner, outer)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	direct := mustContract(t, g, composed, opts)
	if err := direct.Verify(); err != nil {
		t.Fatalf("Verify direct: %v", err)
	}

	if !sameProvenance(expanded, direct) {
		t.Fatalf("keep=%v: two-step expansion differs from direct contraction\ntwo-step: %+v\ndirect:   %+v",
			keep, expanded, direct)
	}
	if expanded.Graph.NumEdges() != direct.Graph.NumEdges() {
		t.Fatalf("keep=%v: edge counts differ: %d vs %d",
			keep, expanded.Graph.NumEdges(), direct.Graph.NumEdges())
	}
}

func TestContractComposition(t *testing.T) {
	g := New(true)
	for n := 1; n <= 6; n++ {
		g.AddNode(n)
	}
	edges := [][2]int{{1, 2}, {1, 3}, {3, 4}, {4, 3}, {2, 5}, {5, 6}, {6, 1}, {4, 5}, {2, 2}}
	for _, e := range edges {
		g.AddEdge(e[0], e[1], nil)
	}
	inner := Partition{{1, 2}, {3, 4}, {5, 6}}
	outer := Partition{{0, 2}, {1}}
	for _, keep := range []bool{false, true} {
		checkComposition(t, g, inner, outer, keep)
	}
}

// Property check on seeded pseudo-random graphs and partitions, directed
// and undirected.
func TestContractCompositionRandom(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		directed := seed%2 == 0
		g := New(directed)
		n := 2 + rng.Intn(9)
		for i := 0; i < n; i++ {
			g.AddNode(100 + i)
		}
		m := rng.Intn(3 * n)
		for i := 0; i < m; i++ {
			g.AddEdge(100+rng.Intn(n), 100+rng.Intn(n), Attrs{"w": rng.Float64()})
		}
		// Random inner partition: shuffle nodes, cut into 1..n groups.
		perm := rng.Perm(n)
		inner := Partition{}
		i := 0
		for i < n {
			size := 1 + rng.Intn(n-i)
			group := make([]int, size)
			for j := range group {
				group[j] = 100 + perm[i+j]
			}
			inner = append(inner, group)
			i += size
		}
		// Random outer partition over inner group indices.
		gperm := rng.Perm(len(inner))
		outer := Partition{}
		i = 0
		for i < len(inner) {
			size := 1 + rng.Intn(len(inner)-i)
			group := make([]int, size)
			for j := range group {
				group[j] = gperm[i+j]
			}
			outer = append(outer, group)
			i += size
		}
		for _, keep := range []bool{false, true} {
			checkComposition(t, g, inner, outer, keep)
		}
	}
}

func TestComposeRejectsInvalidOuter(t *testing.T) {
	inner := Partition{{1}, {2}}
	if _, err := Compose(inner, Partition{{0}, {}}); partitionReason(t, err) != ErrEmptyGroup {
		t.Fatalf("empty outer group: %v", err)
	}
	if _, err := Compose(inner, Partition{{0, 5}, {1}}); partitionReason(t, err) != ErrGroupOutOfRange {
		t.Fatalf("out of range: %v", err)
	}
	if _, err := Compose(inner, Partition{{0, 0}, {1}}); partitionReason(t, err) != ErrDuplicateNode {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := Compose(inner, Partition{{0}}); partitionReason(t, err) != ErrUnassignedNode {
		t.Fatalf("unassigned: %v", err)
	}
}

func TestExpandMismatchedChain(t *testing.T) {
	g := New(true)
	g.AddNode(1)
	g.AddNode(2)
	g.AddEdge(1, 2, nil)
	p := Partition{{1}, {2}}
	c1 := mustContract(t, g, p, Options{})
	c2 := mustContract(t, g, p, Options{})
	if _, err := c2.Expand(c1); err != ErrMismatchedChain {
		t.Fatalf("err = %v, want %v", err, ErrMismatchedChain)
	}
	if _, err := c1.Expand(nil); err != ErrMismatchedChain {
		t.Fatalf("err = %v, want %v", err, ErrMismatchedChain)
	}
}

// Aggregated weights of merged parallel edges must equal the sum computed
// directly over the original edges, and provenance must list every
// contributing source edge.
func TestAggregateSumsParallelEdges(t *testing.T) {
	g := New(true)
	for n := 1; n <= 4; n++ {
		g.AddNode(n)
	}
	g.AddEdge(1, 3, Attrs{"w": 1.5})
	g.AddEdge(2, 3, Attrs{"w": 2.5})
	g.AddEdge(1, 4, Attrs{"w": 4.0})
	g.AddEdge(3, 4, Attrs{"w": 1.0})

	c := mustContract(t, g, Partition{{1, 2}, {3}, {4}}, Options{})
	agg := c.Aggregate(SumFloat("w"))
	if err := agg.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if agg.Graph.NumEdges() != 3 {
		t.Fatalf("aggregated edges = %d, want 3", agg.Graph.NumEdges())
	}

	// Recompute expectations edge by edge from the original graph.
	sums := map[[2]int]float64{}
	srcs := map[[2]int][]int{}
	for _, e := range g.Edges() {
		key := [2]int{c.NodeMap[e.From], c.NodeMap[e.To]}
		sums[key] += e.Attrs["w"].(float64)
		srcs[key] = append(srcs[key], e.ID)
	}
	for _, e := range agg.Graph.Edges() {
		key := [2]int{e.From, e.To}
		if got := e.Attrs["w"].(float64); got != sums[key] {
			t.Fatalf("edge %v: w = %v, want %v", key, got, sums[key])
		}
		if !reflect.DeepEqual(agg.EdgeSources[e.ID], srcs[key]) {
			t.Fatalf("edge %v: sources = %v, want %v", key, agg.EdgeSources[e.ID], srcs[key])
		}
	}
}

// Undirected aggregation merges both orientations of a pair.
func TestAggregateUndirected(t *testing.T) {
	g := New(false)
	for n := 1; n <= 4; n++ {
		g.AddNode(n)
	}
	g.AddEdge(1, 3, Attrs{"w": 1.0})
	g.AddEdge(3, 2, Attrs{"w": 2.0}) // same unordered pair after contraction
	c := mustContract(t, g, Partition{{1, 2}, {3}, {4}}, Options{})
	agg := c.Aggregate(SumFloat("w"))
	if agg.Graph.NumEdges() != 1 {
		t.Fatalf("aggregated edges = %d, want 1", agg.Graph.NumEdges())
	}
	e := agg.Graph.Edge(0)
	if got := e.Attrs["w"].(float64); got != 3.0 {
		t.Fatalf("w = %v, want 3.0", got)
	}
	if !reflect.DeepEqual(agg.EdgeSources[0], []int{0, 1}) {
		t.Fatalf("sources = %v, want [0 1]", agg.EdgeSources[0])
	}
	if err := agg.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Edge-by-edge spot check of Verify against a tampered contraction.
func TestVerifyDetectsTampering(t *testing.T) {
	g := New(true)
	g.AddNode(1)
	g.AddNode(2)
	g.AddEdge(1, 2, nil)
	c := mustContract(t, g, Partition{{1}, {2}}, Options{})
	c.EdgeSources[0] = []int{7}
	if err := c.Verify(); err == nil {
		t.Fatal("Verify accepted tampered EdgeSources")
	}
}
