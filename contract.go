package graphcontract

import "fmt"

// Aggregator combines the attributes of several source edges into the
// attributes of one contracted edge. The input slice preserves edge
// insertion order.
type Aggregator func(attrs []Attributes) Attributes

// SumAttributes sums attribute values per key; missing keys count as zero.
// It is the default Aggregator.
func SumAttributes(attrs []Attributes) Attributes {
	out := make(Attributes)
	for _, a := range attrs {
		for k, v := range a {
			out[k] += v
		}
	}
	return out
}

// ContractOptions controls how Contract behaves.
type ContractOptions struct {
	// KeepSelfLoops keeps edges whose endpoints fall into the same group as
	// self-loops of the contracted node. Otherwise they are dropped and
	// recorded in Mapping.DroppedEdges.
	KeepSelfLoops bool
	// MergeParallel merges all edges between the same ordered pair of
	// contracted nodes into one edge whose attributes are aggregated and
	// whose provenance lists every source edge. If false, every source edge
	// yields its own contracted edge, preserving parallelism.
	MergeParallel bool
	// Aggregate combines attributes of merged edges. Nil means SumAttributes.
	Aggregate Aggregator
}

// Mapping records how a contracted graph relates to its input graph. It is
// expandable: every contracted node and edge can be expanded back to the
// nodes and edges of the input graph, and mappings compose with Then so a
// chain of contractions can be expanded all the way to the original graph.
type Mapping struct {
	nodeToGroup map[NodeID]int
	members     [][]NodeID
	edgeSources map[EdgeID][]EdgeID
	dropped     []EdgeID
}

// GroupOf returns the contracted node that input node n maps to.
func (m *Mapping) GroupOf(n NodeID) (int, bool) {
	g, ok := m.nodeToGroup[n]
	return g, ok
}

// Members expands contracted node i to the input nodes it contains.
func (m *Mapping) Members(i int) []NodeID {
	out := make([]NodeID, len(m.members[i]))
	copy(out, m.members[i])
	return out
}

// Sources expands contracted edge e to the input edges it was built from,
// in input edge order.
func (m *Mapping) Sources(e EdgeID) []EdgeID {
	src := m.edgeSources[e]
	out := make([]EdgeID, len(src))
	copy(out, src)
	return out
}

// DroppedEdges returns the input edges dropped as self-loops during
// contraction (only when KeepSelfLoops is false), in input edge order.
func (m *Mapping) DroppedEdges() []EdgeID {
	out := make([]EdgeID, len(m.dropped))
	copy(out, m.dropped)
	return out
}

// Then composes m (mapping g0 -> g1) with next (mapping g1 -> g2), yielding
// the mapping g0 -> g2. Provenance is expanded through m, so the composed
// mapping refers to g0's edges directly.
func (m *Mapping) Then(next *Mapping) (*Mapping, error) {
	out := &Mapping{
		nodeToGroup: make(map[NodeID]int),
		edgeSources: make(map[EdgeID][]EdgeID),
	}
	for n, g1 := range m.nodeToGroup {
		g2, ok := next.nodeToGroup[NodeID(g1)]
		if !ok {
			return nil, fmt.Errorf("graphcontract: intermediate node %d is not covered by the next mapping", g1)
		}
		out.nodeToGroup[n] = g2
	}
	out.members = make([][]NodeID, len(next.members))
	for n, g2 := range out.nodeToGroup {
		out.members[g2] = append(out.members[g2], n)
	}
	for e2, src1 := range next.edgeSources {
		var expanded []EdgeID
		for _, e1 := range src1 {
			expanded = append(expanded, m.edgeSources[e1]...)
		}
		out.edgeSources[e2] = expanded
	}
	out.dropped = append(out.dropped, m.dropped...)
	for _, e1 := range next.dropped {
		out.dropped = append(out.dropped, m.edgeSources[e1]...)
	}
	return out, nil
}

// edgeKey identifies a pair of contracted endpoints for parallel merging.
type edgeKey struct{ from, to int }

// Contract collapses g according to p: every group becomes one node of the
// contracted graph and every edge becomes an edge between the groups of its
// endpoints. The partition must cover exactly the node set of g. The
// returned mapping expands contracted nodes and edges back to g.
func Contract(g *Graph, p *Partition, opts ContractOptions) (*Graph, *Mapping, error) {
	if err := checkCoverage(g, p); err != nil {
		return nil, nil, err
	}
	agg := opts.Aggregate
	if agg == nil {
		agg = SumAttributes
	}

	out := New(g.Directed())
	for i := 0; i < p.NumGroups(); i++ {
		out.AddNode(NodeID(i))
	}
	m := &Mapping{
		nodeToGroup: make(map[NodeID]int),
		members:     p.Groups(),
		edgeSources: make(map[EdgeID][]EdgeID),
	}
	for n, gi := range p.groupOf {
		m.nodeToGroup[n] = gi
	}

	merged := make(map[edgeKey]EdgeID)
	for _, e := range g.edges {
		from, _ := p.GroupOf(e.From)
		to, _ := p.GroupOf(e.To)
		if from == to && !opts.KeepSelfLoops {
			m.dropped = append(m.dropped, e.ID)
			continue
		}
		if opts.MergeParallel {
			key := edgeKey{from: from, to: to}
			if !g.Directed() && key.to < key.from {
				key.from, key.to = key.to, key.from
			}
			if id, ok := merged[key]; ok {
				m.edgeSources[id] = append(m.edgeSources[id], e.ID)
				continue
			}
			id := out.AddEdge(NodeID(key.from), NodeID(key.to), nil)
			merged[key] = id
			m.edgeSources[id] = []EdgeID{e.ID}
			continue
		}
		id := out.AddEdge(NodeID(from), NodeID(to), nil)
		m.edgeSources[id] = []EdgeID{e.ID}
	}

	for id, sources := range m.edgeSources {
		attrs := make([]Attributes, len(sources))
		for i, s := range sources {
			attrs[i] = g.Edge(s).Attrs
		}
		out.Edge(id).Attrs = agg(attrs)
	}
	return out, m, nil
}

// checkCoverage ensures the partition covers exactly the nodes of g.
func checkCoverage(g *Graph, p *Partition) error {
	for _, n := range g.Nodes() {
		if _, ok := p.GroupOf(n); !ok {
			return fmt.Errorf("graphcontract: node %d is not covered by the partition", n)
		}
	}
	for _, members := range p.groups {
		for _, n := range members {
			if !g.HasNode(n) {
				return fmt.Errorf("graphcontract: partition contains node %d which is not in the graph", n)
			}
		}
	}
	return nil
}
