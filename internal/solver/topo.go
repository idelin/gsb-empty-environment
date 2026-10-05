package solver

import "sort"

// nodeIndex maps node names to dense indices, deriving the node set from the
// explicit Nodes list plus every edge endpoint.
func nodeIndex(g *Graph) ([]string, map[string]int) {
	idx := make(map[string]int)
	var names []string
	add := func(n string) {
		if _, ok := idx[n]; !ok {
			idx[n] = len(names)
			names = append(names, n)
		}
	}
	for _, n := range g.Nodes {
		add(n)
	}
	for _, e := range g.Edges {
		add(e.From)
		add(e.To)
	}
	return names, idx
}

// findCycle returns the edge IDs of one directed cycle in the subgraph
// induced by active edges, or nil if that subgraph is acyclic.
func findCycle(g *Graph, idx map[string]int, active []bool) []int {
	n := len(idx)
	adj := make([][]int, n)
	for i, e := range g.Edges {
		if active[i] {
			adj[idx[e.From]] = append(adj[idx[e.From]], i)
		}
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]int, n)
	var edgeStack []int
	var nodeStack []int

	var dfs func(u int) []int
	dfs = func(u int) []int {
		color[u] = gray
		nodeStack = append(nodeStack, u)
		for _, ei := range adj[u] {
			v := idx[g.Edges[ei].To]
			switch color[v] {
			case gray:
				// Found a back edge: extract the cycle from the stacks.
				start := 0
				for i, w := range nodeStack {
					if w == v {
						start = i
						break
					}
				}
				cycle := make([]int, 0, len(edgeStack)-start+1)
				cycle = append(cycle, edgeStack[start:]...)
				cycle = append(cycle, ei)
				return cycle
			case white:
				edgeStack = append(edgeStack, ei)
				if c := dfs(v); c != nil {
					return c
				}
				edgeStack = edgeStack[:len(edgeStack)-1]
			}
		}
		nodeStack = nodeStack[:len(nodeStack)-1]
		color[u] = black
		return nil
	}
	for u := 0; u < n; u++ {
		if color[u] == white {
			if c := dfs(u); c != nil {
				return c
			}
		}
	}
	return nil
}

// topoOrder returns a deterministic (lexicographically smallest) topological
// ordering of the subgraph induced by active edges. The caller must ensure
// the subgraph is acyclic.
func topoOrder(g *Graph, active []bool) []string {
	names, idx := nodeIndex(g)
	n := len(names)
	indeg := make([]int, n)
	adj := make([][]int, n)
	for i, e := range g.Edges {
		if !active[i] {
			continue
		}
		u, v := idx[e.From], idx[e.To]
		adj[u] = append(adj[u], v)
		indeg[v]++
	}
	ready := make([]string, 0, n)
	for u := 0; u < n; u++ {
		if indeg[u] == 0 {
			ready = append(ready, names[u])
		}
	}
	order := make([]string, 0, n)
	for len(ready) > 0 {
		// Pop the lexicographically smallest ready node.
		sort.Strings(ready)
		name := ready[0]
		ready = ready[1:]
		order = append(order, name)
		for _, v := range adj[idx[name]] {
			indeg[v]--
			if indeg[v] == 0 {
				ready = append(ready, names[v])
			}
		}
	}
	return order
}
