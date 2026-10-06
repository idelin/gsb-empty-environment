package graphx

import (
	"errors"
	"fmt"
	"sort"
)

// Sentinel reasons carried by PartitionError.
var (
	ErrEmptyGroup      = errors.New("graphx: partition contains an empty group")
	ErrUnknownNode     = errors.New("graphx: partition references a node not in the graph")
	ErrDuplicateNode   = errors.New("graphx: node assigned to more than one group")
	ErrUnassignedNode  = errors.New("graphx: node not assigned to any group")
	ErrGroupOutOfRange = errors.New("graphx: group index out of range")
	// ErrMismatchedChain is returned by Expand when the parent contraction
	// did not produce the graph this contraction was built from.
	ErrMismatchedChain = errors.New("graphx: contraction chain mismatch")
)

// PartitionError describes why a partition is invalid. Err is one of the
// sentinel errors above; Group and Node locate the offending entry, and
// Prev gives the earlier group index for duplicates (-1 when unused).
type PartitionError struct {
	Err   error
	Group int
	Node  int
	Prev  int
}

func (e *PartitionError) Error() string {
	switch e.Err {
	case ErrEmptyGroup:
		return fmt.Sprintf("%v: group %d", e.Err, e.Group)
	case ErrUnknownNode, ErrUnassignedNode:
		return fmt.Sprintf("%v: node %d", e.Err, e.Node)
	case ErrDuplicateNode:
		return fmt.Sprintf("%v: node %d in groups %d and %d", e.Err, e.Node, e.Prev, e.Group)
	case ErrGroupOutOfRange:
		return fmt.Sprintf("%v: group %d references group index %d", e.Err, e.Group, e.Node)
	}
	return e.Err.Error()
}

// Partition assigns every node of a graph to exactly one group. Groups are
// lists of node IDs; group i becomes node i of the contracted graph.
type Partition [][]int

// Validate checks that p is a total, non-overlapping assignment of the
// nodes of g with no empty groups.
func (p Partition) Validate(g *Graph) error {
	seen := make(map[int]int, g.NumNodes())
	for gi, group := range p {
		if len(group) == 0 {
			return &PartitionError{Err: ErrEmptyGroup, Group: gi, Node: -1, Prev: -1}
		}
		for _, n := range group {
			if !g.HasNode(n) {
				return &PartitionError{Err: ErrUnknownNode, Group: gi, Node: n, Prev: -1}
			}
			if prev, ok := seen[n]; ok {
				return &PartitionError{Err: ErrDuplicateNode, Group: gi, Node: n, Prev: prev}
			}
			seen[n] = gi
		}
	}
	for _, n := range g.Nodes() {
		if _, ok := seen[n]; !ok {
			return &PartitionError{Err: ErrUnassignedNode, Group: -1, Node: n, Prev: -1}
		}
	}
	return nil
}

// Options controls contraction behavior.
type Options struct {
	// KeepSelfLoops keeps edges whose endpoints fall in the same group as
	// self-loops on the contracted node. When false they are dropped and
	// their EdgeMap entry is -1.
	KeepSelfLoops bool
}

// Contraction is the result of contracting a graph by a partition. It
// carries the contracted graph plus the full provenance mapping back to
// the source graph, and can be expanded through a parent contraction to
// re-express that provenance in terms of an earlier graph.
type Contraction struct {
	// Graph is the contracted graph. Its node IDs are the group indices
	// 0..len(Groups)-1 of the partition.
	Graph *Graph
	// NodeMap maps a source node ID to its contracted node ID.
	NodeMap map[int]int
	// Groups maps a contracted node ID to its source node IDs.
	Groups [][]int
	// EdgeMap maps a source edge ID to its contracted edge ID, or -1 if
	// the edge was dropped (an intra-group edge with KeepSelfLoops off).
	EdgeMap map[int]int
	// EdgeSources maps a contracted edge ID to the source edge IDs that
	// produced it, in ascending source-ID order.
	EdgeSources [][]int

	source *Graph
}

// Source returns the graph this contraction was built from.
func (c *Contraction) Source() *Graph { return c.source }

// Contract collapses each group of p into a single node. Every source edge
// whose endpoints land in different groups becomes one contracted edge, so
// cross-group parallel edges are preserved one-to-one along with their
// provenance. Intra-group edges become self-loops or are dropped according
// to opts.
func Contract(g *Graph, p Partition, opts Options) (*Contraction, error) {
	if err := p.Validate(g); err != nil {
		return nil, err
	}
	cg := New(g.Directed)
	for range p {
		cg.AddNode(cg.NumNodes())
	}
	nodeMap := make(map[int]int, g.NumNodes())
	groups := make([][]int, len(p))
	for gi, group := range p {
		groups[gi] = append([]int(nil), group...)
		for _, n := range group {
			nodeMap[n] = gi
		}
	}
	c := &Contraction{
		Graph:       cg,
		NodeMap:     nodeMap,
		Groups:      groups,
		EdgeMap:     make(map[int]int, g.NumEdges()),
		EdgeSources: make([][]int, 0, g.NumEdges()),
		source:      g,
	}
	for _, e := range g.edges {
		from, to := nodeMap[e.From], nodeMap[e.To]
		if from == to && !opts.KeepSelfLoops {
			c.EdgeMap[e.ID] = -1
			continue
		}
		attrs := make(Attrs, len(e.Attrs))
		for k, v := range e.Attrs {
			attrs[k] = v
		}
		id, err := cg.AddEdge(from, to, attrs)
		if err != nil {
			return nil, err
		}
		c.EdgeMap[e.ID] = id
		c.EdgeSources = append(c.EdgeSources, []int{e.ID})
	}
	return c, nil
}

// Compose returns the single partition of the original node set equivalent to
// contracting by inner first and then by outer, where outer groups the
// group indices produced by inner. outer must reference every inner group
// exactly once and contain no empty groups.
func Compose(inner, outer Partition) (Partition, error) {
	seen := make([]bool, len(inner))
	composed := make(Partition, len(outer))
	for gi, group := range outer {
		if len(group) == 0 {
			return nil, &PartitionError{Err: ErrEmptyGroup, Group: gi, Node: -1, Prev: -1}
		}
		for _, idx := range group {
			if idx < 0 || idx >= len(inner) {
				return nil, &PartitionError{Err: ErrGroupOutOfRange, Group: gi, Node: idx, Prev: -1}
			}
			if seen[idx] {
				return nil, &PartitionError{Err: ErrDuplicateNode, Group: gi, Node: idx, Prev: -1}
			}
			seen[idx] = true
			composed[gi] = append(composed[gi], inner[idx]...)
		}
	}
	for idx, ok := range seen {
		if !ok {
			return nil, &PartitionError{Err: ErrUnassignedNode, Group: -1, Node: idx, Prev: -1}
		}
	}
	return composed, nil
}

// Expand flattens c's provenance through the parent contraction that
// produced c's source graph, yielding an equivalent contraction expressed
// directly in terms of the original graph. Expanding Contract(Contract(g,
// inner), outer) through the inner contraction gives the same provenance
// as Contract(g, Compose(inner, outer)).
func (c *Contraction) Expand(parent *Contraction) (*Contraction, error) {
	if parent == nil || parent.Graph != c.source {
		return nil, ErrMismatchedChain
	}
	nodeMap := make(map[int]int, len(parent.NodeMap))
	for n, g1 := range parent.NodeMap {
		nodeMap[n] = c.NodeMap[g1]
	}
	groups := make([][]int, len(c.Groups))
	for i, group := range c.Groups {
		for _, g1 := range group {
			groups[i] = append(groups[i], parent.Groups[g1]...)
		}
	}
	edgeMap := make(map[int]int, len(parent.EdgeMap))
	for e, ce1 := range parent.EdgeMap {
		if ce1 < 0 {
			edgeMap[e] = -1
			continue
		}
		edgeMap[e] = c.EdgeMap[ce1]
	}
	sources := make([][]int, len(c.EdgeSources))
	for ce, srcs := range c.EdgeSources {
		for _, s := range srcs {
			sources[ce] = append(sources[ce], parent.EdgeSources[s]...)
		}
	}
	return &Contraction{
		Graph:       c.Graph,
		NodeMap:     nodeMap,
		Groups:      groups,
		EdgeMap:     edgeMap,
		EdgeSources: sources,
		source:      parent.source,
	}, nil
}

// Aggregator combines the attributes of a set of source edges into the
// attributes of the single edge that represents them.
type Aggregator func(edges []Edge) Attrs

// Aggregate merges each set of parallel contracted edges (same ordered
// endpoints; same unordered pair for undirected graphs) into one edge whose
// attributes are produced by agg over the underlying source edges. Node
// provenance is unchanged; edge provenance accumulates, so each merged
// edge records all source edges it summarizes.
func (c *Contraction) Aggregate(agg Aggregator) *Contraction {
	type pair struct{ from, to int }
	var order []pair
	buckets := make(map[pair][]int)
	for _, e := range c.Graph.edges {
		key := pair{e.From, e.To}
		if !c.Graph.Directed && key.to < key.from {
			key.from, key.to = key.to, key.from
		}
		if _, ok := buckets[key]; !ok {
			order = append(order, key)
		}
		buckets[key] = append(buckets[key], e.ID)
	}
	ag := New(c.Graph.Directed)
	for _, n := range c.Graph.Nodes() {
		ag.AddNode(n)
	}
	edgeMap := make(map[int]int, len(c.EdgeMap))
	for e, ce := range c.EdgeMap {
		if ce < 0 {
			edgeMap[e] = -1
		}
	}
	sources := make([][]int, 0, len(order))
	for _, key := range order {
		var srcIDs []int
		for _, ce := range buckets[key] {
			srcIDs = append(srcIDs, c.EdgeSources[ce]...)
		}
		sort.Ints(srcIDs)
		srcEdges := make([]Edge, len(srcIDs))
		for i, s := range srcIDs {
			srcEdges[i] = c.source.Edge(s)
		}
		id, err := ag.AddEdge(key.from, key.to, agg(srcEdges))
		if err != nil {
			panic(err) // nodes were just added; cannot fail
		}
		for _, s := range srcIDs {
			edgeMap[s] = id
		}
		sources = append(sources, srcIDs)
	}
	return &Contraction{
		Graph:       ag,
		NodeMap:     c.NodeMap,
		Groups:      c.Groups,
		EdgeMap:     edgeMap,
		EdgeSources: sources,
		source:      c.source,
	}
}

// SumFloat returns an Aggregator that sums the numeric values of key over
// the source edges. Missing or non-numeric values count as zero.
func SumFloat(key string) Aggregator {
	return func(edges []Edge) Attrs {
		total := 0.0
		for _, e := range edges {
			switch v := e.Attrs[key].(type) {
			case float64:
				total += v
			case int:
				total += float64(v)
			case int64:
				total += float64(v)
			}
		}
		return Attrs{key: total}
	}
}

// Verify checks the contraction edge by edge against its source graph:
// every source edge is accounted for in EdgeMap, every mapped edge has the
// contracted endpoints implied by NodeMap, EdgeSources is exactly the
// inverse of EdgeMap, and Groups is the inverse of NodeMap.
func (c *Contraction) Verify() error {
	for gi, group := range c.Groups {
		for _, n := range group {
			mapped, ok := c.NodeMap[n]
			if !ok || mapped != gi {
				return fmt.Errorf("graphx: verify: node %d listed in group %d but NodeMap says %d", n, gi, mapped)
			}
		}
	}
	if len(c.EdgeSources) != c.Graph.NumEdges() {
		return fmt.Errorf("graphx: verify: %d EdgeSources entries for %d contracted edges",
			len(c.EdgeSources), c.Graph.NumEdges())
	}
	inverse := make(map[int][]int)
	for _, e := range c.source.edges {
		ce, ok := c.EdgeMap[e.ID]
		if !ok {
			return fmt.Errorf("graphx: verify: source edge %d missing from EdgeMap", e.ID)
		}
		if ce < 0 {
			continue
		}
		if ce >= c.Graph.NumEdges() {
			return fmt.Errorf("graphx: verify: source edge %d maps to nonexistent edge %d", e.ID, ce)
		}
		mapped := c.Graph.Edge(ce)
		wantFrom, wantTo := c.NodeMap[e.From], c.NodeMap[e.To]
		if !endpointsMatch(c.Graph.Directed, mapped, wantFrom, wantTo) {
			return fmt.Errorf("graphx: verify: edge %d (%d->%d) maps to edge %d (%d->%d), want (%d->%d)",
				e.ID, e.From, e.To, ce, mapped.From, mapped.To, wantFrom, wantTo)
		}
		inverse[ce] = append(inverse[ce], e.ID)
	}
	for ce := 0; ce < c.Graph.NumEdges(); ce++ {
		got := append([]int(nil), inverse[ce]...)
		want := append([]int(nil), c.EdgeSources[ce]...)
		sort.Ints(got)
		sort.Ints(want)
		if len(got) != len(want) {
			return fmt.Errorf("graphx: verify: edge %d sources %v, EdgeMap inverse gives %v", ce, want, got)
		}
		for i := range got {
			if got[i] != want[i] {
				return fmt.Errorf("graphx: verify: edge %d sources %v, EdgeMap inverse gives %v", ce, want, got)
			}
		}
	}
	return nil
}

func endpointsMatch(directed bool, e Edge, from, to int) bool {
	if directed {
		return e.From == from && e.To == to
	}
	return (e.From == from && e.To == to) || (e.From == to && e.To == from)
}
