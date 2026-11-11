package policyeval

import (
	"fmt"
	"sort"
	"strings"
)

// node identifies one concrete policy OID at one layer of a layered graph.
type node struct {
	layer  int
	policy OID
}

// edge is a top-down admissible relation between adjacent layers. Every edge
// is tagged with the chain that declared it, which is what makes cross-chain
// splicing visible in the union graph.
type edge struct {
	from     node
	to       node
	mapping  *Mapping
	identity bool
	chainID  string
}

type layeredGraph struct {
	nodes    map[node]struct{}
	out      map[node][]edge
	edges    []edge
	universe []OID
}

// universeOf collects the finite concrete policy universe of a chain plus the
// requested leaf usage. The literal anyPolicy wildcard is not a concrete
// policy: it is expanded implicitly through allowedAt against this universe.
func universeOf(chain *Chain, leafUsage []OID) []OID {
	set := map[OID]struct{}{}
	for _, layer := range chain.Layers {
		for _, policy := range layer.AllowedPolicies {
			if policy != AnyPolicy {
				set[policy] = struct{}{}
			}
		}
	}
	for _, usage := range leafUsage {
		if usage != AnyPolicy {
			set[usage] = struct{}{}
		}
	}
	out := make([]OID, 0, len(set))
	for policy := range set {
		out = append(out, policy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// buildGraph constructs the exhaustive top-down relation graph for one chain:
// one node per admissible concrete policy per layer, and one edge per
// admissible identity or declared-mapping relation across each boundary.
func buildGraph(chain *Chain, leafUsage []OID) *layeredGraph {
	graph := &layeredGraph{
		nodes:    map[node]struct{}{},
		out:      map[node][]edge{},
		universe: universeOf(chain, leafUsage),
	}

	// A layer is entered only by listing a concrete policy (or anyPolicy) in
	// its certificatePolicies. A mapping whose endpoint is not admitted by
	// the relevant layer is inert, mirroring the reverse evaluator, so no
	// extra nodes are synthesized for mapping endpoints.
	for li, layer := range chain.Layers {
		for _, policy := range graph.universe {
			if allowedAt(layer, policy) {
				graph.nodes[node{li, policy}] = struct{}{}
			}
		}
	}

	edgeSeen := map[string]struct{}{}
	addEdge := func(e edge) {
		key := fmt.Sprintf("%d|%s|%d|%s|%t|%s",
			e.from.layer, e.from.policy, e.to.layer, e.to.policy, e.identity, e.chainID)
		if _, ok := edgeSeen[key]; ok {
			return
		}
		edgeSeen[key] = struct{}{}
		graph.out[e.from] = append(graph.out[e.from], e)
		graph.edges = append(graph.edges, e)
	}

	for li := 0; li+1 < len(chain.Layers); li++ {
		lower := chain.Layers[li+1]

		for _, policy := range graph.universe {
			from := node{li, policy}
			to := node{li + 1, policy}
			if _, ok := graph.nodes[from]; !ok {
				continue
			}
			if _, ok := graph.nodes[to]; ok {
				addEdge(edge{
					from:     from,
					to:       to,
					identity: true,
					chainID:  chain.ID,
				})
			}
		}

		if lower.InhibitMapping {
			continue
		}
		for mi := range lower.Mappings {
			mapping := lower.Mappings[mi]
			from := node{li, mapping.IssuerDomain}
			to := node{li + 1, mapping.SubjectDomain}
			if _, ok := graph.nodes[from]; !ok {
				continue
			}
			// The subject endpoint is produced by this mapping at the lower
			// layer; it need not be independently listed there.
			addEdge(edge{
				from:    from,
				to:      to,
				mapping: &lower.Mappings[mi],
				chainID: chain.ID,
			})
		}
	}

	for n := range graph.out {
		sort.Slice(graph.out[n], func(i, j int) bool {
			if graph.out[n][i].to.policy != graph.out[n][j].to.policy {
				return graph.out[n][i].to.policy < graph.out[n][j].to.policy
			}
			return graph.out[n][i].identity
		})
	}
	return graph
}

// walkStep is one traversal record in an enumerated graph walk.
type walkStep struct {
	layer   int
	policy  OID
	chainID string
	mapping *Mapping
}

// enumerateWalks returns every top-down walk from a layer-0 node to a leaf
// node whose leaf policy is one of the requested usage policies. Because
// every edge moves exactly one layer down, the graph is acyclic and every
// walk terminates.
func (g *layeredGraph) enumerateWalks(leafUsage []OID, depth int, chainID string) [][]walkStep {
	wanted := map[OID]struct{}{}
	for _, usage := range leafUsage {
		wanted[usage] = struct{}{}
	}

	var roots []node
	for n := range g.nodes {
		if n.layer == 0 {
			roots = append(roots, n)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].policy < roots[j].policy })

	var walks [][]walkStep
	var dfs func(n node, prefix []walkStep)
	dfs = func(n node, prefix []walkStep) {
		if n.layer == depth-1 {
			if _, ok := wanted[n.policy]; ok {
				walk := make([]walkStep, len(prefix))
				copy(walk, prefix)
				walks = append(walks, walk)
			}
			return
		}
		for _, e := range g.out[n] {
			dfs(e.to, append(prefix, walkStep{
				layer:   e.to.layer,
				policy:  e.to.policy,
				chainID: e.chainID,
				mapping: e.mapping,
			}))
		}
	}

	for _, root := range roots {
		dfs(root, []walkStep{{layer: 0, policy: root.policy, chainID: chainID}})
	}
	return walks
}

// canonicalWalk renders a walk as a stable string for set comparison.
func canonicalWalk(steps []walkStep) string {
	var b strings.Builder
	for i, step := range steps {
		if i > 0 {
			b.WriteByte('|')
		}
		fmt.Fprintf(&b, "%d:%s:", step.layer, step.policy)
		if step.mapping != nil {
			fmt.Fprintf(&b, ":m=%s>%s", step.mapping.IssuerDomain, step.mapping.SubjectDomain)
		} else {
			b.WriteString(":id")
		}
	}
	return b.String()
}

// canonicalPath renders an evaluator Path in the same shape as canonicalWalk.
func canonicalPath(path Path) string {
	var b strings.Builder
	for i, step := range path.Steps {
		if i > 0 {
			b.WriteByte('|')
		}
		fmt.Fprintf(&b, "%d:%s:", step.Layer, step.Policy)
		if step.AppliedMapping != nil {
			fmt.Fprintf(&b, ":m=%s>%s", step.AppliedMapping.IssuerDomain, step.AppliedMapping.SubjectDomain)
		} else {
			b.WriteString(":id")
		}
	}
	return b.String()
}

// RingReport describes one cycle found in a chain's abstract mapping graph.
type RingReport struct {
	// ChainID is the chain whose mappings contain the cycle.
	ChainID string

	// Cycle lists the OIDs around the ring in traversal order, with the
	// starting OID repeated at the end.
	Cycle []OID
}

// detectRings finds cycles in a chain's mapping edges viewed as an abstract
// directed OID graph. Mappings at different boundaries are pooled, so a ring
// spanning several layers is detected just as readily as a self-mapping.
func detectRings(chain *Chain) []RingReport {
	adjacency := map[OID][]OID{}
	edgeSeen := map[string]struct{}{}
	for _, layer := range chain.Layers {
		for _, mapping := range layer.Mappings {
			key := string(mapping.IssuerDomain) + ">" + string(mapping.SubjectDomain)
			if _, ok := edgeSeen[key]; ok {
				continue
			}
			edgeSeen[key] = struct{}{}
			adjacency[mapping.IssuerDomain] = append(adjacency[mapping.IssuerDomain], mapping.SubjectDomain)
		}
	}
	for from := range adjacency {
		sort.Slice(adjacency[from], func(i, j int) bool {
			return adjacency[from][i] < adjacency[from][j]
		})
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[OID]int{}
	var stack []OID
	var reports []RingReport
	reported := map[string]struct{}{}

	var dfs func(policy OID)
	dfs = func(policy OID) {
		color[policy] = gray
		stack = append(stack, policy)
		for _, next := range adjacency[policy] {
			switch color[next] {
			case white:
				dfs(next)
			case gray:
				cycle := extractCycle(stack, next)
				key := cycleKey(cycle)
				if _, ok := reported[key]; !ok {
					reported[key] = struct{}{}
					reports = append(reports, RingReport{ChainID: chain.ID, Cycle: cycle})
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[policy] = black
	}

	var vertices []OID
	for vertex := range adjacency {
		vertices = append(vertices, vertex)
	}
	sort.Slice(vertices, func(i, j int) bool { return vertices[i] < vertices[j] })
	for _, vertex := range vertices {
		if color[vertex] == white {
			dfs(vertex)
		}
	}
	return reports
}

// extractCycle pulls the cycle portion out of a DFS stack once a back edge is
// observed, repeating the target OID at the end to close the ring.
func extractCycle(stack []OID, back OID) []OID {
	start := 0
	for i, policy := range stack {
		if policy == back {
			start = i
			break
		}
	}
	cycle := make([]OID, 0, len(stack)-start+1)
	cycle = append(cycle, stack[start:]...)
	cycle = append(cycle, back)
	return cycle
}

// cycleKey canonicalizes a cycle so the same ring is reported once regardless
// of where DFS enters it.
func cycleKey(cycle []OID) string {
	ring := cycle[:len(cycle)-1]
	minStart := 0
	for i := 1; i < len(ring); i++ {
		if ring[i] < ring[minStart] {
			minStart = i
		}
	}
	var b strings.Builder
	for i := 0; i < len(ring); i++ {
		if i > 0 {
			b.WriteByte('>')
		}
		b.WriteString(string(ring[(minStart+i)%len(ring)]))
	}
	return b.String()
}
