// Package diag provides fault-dependency diagnostics: it compresses a
// directed dependency graph into strongly connected components (SCCs),
// finds minimum candidate root sets that explain all observed faults via
// reachability, and builds minimum worst-case-cost decision trees that
// identify the true root set among candidate worlds using costly tests.
package diag

import "fmt"

// Graph is a directed fault-dependency graph. An edge u -> v means a fault
// in u can propagate to (and be observed at) v.
type Graph struct {
	names []string
	index map[string]int
	adj   [][]int
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{index: make(map[string]int)}
}

// AddNode registers a node; duplicate adds are ignored.
func (g *Graph) AddNode(name string) {
	if _, ok := g.index[name]; ok {
		return
	}
	g.index[name] = len(g.names)
	g.names = append(g.names, name)
	g.adj = append(g.adj, nil)
}

// AddEdge adds a directed edge, creating endpoints as needed.
func (g *Graph) AddEdge(from, to string) {
	g.AddNode(from)
	g.AddNode(to)
	u, v := g.index[from], g.index[to]
	for _, w := range g.adj[u] {
		if w == v {
			return
		}
	}
	g.adj[u] = append(g.adj[u], v)
}

// Nodes returns the node names in insertion order.
func (g *Graph) Nodes() []string { return append([]string(nil), g.names...) }

// NumNodes reports the number of nodes.
func (g *Graph) NumNodes() int { return len(g.names) }

func (g *Graph) nodeIndex(name string) (int, error) {
	i, ok := g.index[name]
	if !ok {
		return 0, fmt.Errorf("diag: unknown node %q", name)
	}
	return i, nil
}

// Components is the result of SCC compression.
type Components struct {
	// Comps lists each SCC as a slice of node indices.
	Comps [][]int
	// CompOf maps node index -> component index.
	CompOf []int
	// CompAdj is the condensation DAG adjacency (component -> components).
	CompAdj [][]int
	// Sources holds component indices with in-degree 0 in the condensation.
	Sources []int
}

// StronglyConnectedComponents compresses g with Tarjan's algorithm and
// builds the condensation DAG.
func StronglyConnectedComponents(g *Graph) *Components {
	n := len(g.names)
	const unvisited = -1
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = unvisited
	}
	var stack []int
	counter := 0
	var comps [][]int
	compOf := make([]int, n)

	var strongconnect func(v int)
	strongconnect = func(v int) {
		index[v], low[v] = counter, counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range g.adj[v] {
			if index[w] == unvisited {
				strongconnect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] == index[v] {
			var comp []int
			cid := len(comps)
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				compOf[w] = cid
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			comps = append(comps, comp)
		}
	}
	for v := 0; v < n; v++ {
		if index[v] == unvisited {
			strongconnect(v)
		}
	}

	// Condensation DAG.
	m := len(comps)
	adjSets := make([]map[int]bool, m)
	indeg := make([]int, m)
	for i := range adjSets {
		adjSets[i] = make(map[int]bool)
	}
	for v := 0; v < n; v++ {
		for _, w := range g.adj[v] {
			a, b := compOf[v], compOf[w]
			if a != b && !adjSets[a][b] {
				adjSets[a][b] = true
				indeg[b]++
			}
		}
	}
	compAdj := make([][]int, m)
	for a, set := range adjSets {
		for b := range set {
			compAdj[a] = append(compAdj[a], b)
		}
	}
	var sources []int
	for c := 0; c < m; c++ {
		if indeg[c] == 0 {
			sources = append(sources, c)
		}
	}
	return &Components{Comps: comps, CompOf: compOf, CompAdj: compAdj, Sources: sources}
}

// reachableFrom computes, for a single component, the set of components
// reachable from it in the condensation DAG (including itself).
func (c *Components) reachableFrom(src int) []bool {
	seen := make([]bool, len(c.Comps))
	stack := []int{src}
	seen[src] = true
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, w := range c.CompAdj[v] {
			if !seen[w] {
				seen[w] = true
				stack = append(stack, w)
			}
		}
	}
	return seen
}
