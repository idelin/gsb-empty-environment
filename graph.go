// Package fas implements a minimal approximator for the Feedback Arc Set
// problem on directed graphs: find a small-weight set of edges whose
// removal makes the graph acyclic.
package fas

// Edge is a weighted directed edge from From to To.
type Edge struct {
	From   int
	To     int
	Weight float64
}

// Graph is a directed weighted graph with nodes numbered 0..NumNodes-1.
type Graph struct {
	NumNodes int
	Edges    []Edge
}

// New builds a graph from the given edge list.
func New(numNodes int, edges ...Edge) *Graph {
	return &Graph{NumNodes: numNodes, Edges: append([]Edge(nil), edges...)}
}

// TotalWeight sums the weights of the given edges.
func TotalWeight(edges []Edge) float64 {
	var w float64
	for _, e := range edges {
		w += e.Weight
	}
	return w
}

// IsDAG reports whether the edge set is acyclic (Kahn's algorithm).
func IsDAG(numNodes int, edges []Edge) bool {
	_, ok := TopologicalOrder(numNodes, edges)
	return ok
}

// TopologicalOrder returns an ordering of the nodes such that every edge
// goes from an earlier node to a later node. The second return value is
// false when the edge set contains a cycle.
func TopologicalOrder(numNodes int, edges []Edge) ([]int, bool) {
	inDeg := make([]int, numNodes)
	succ := make([][]int, numNodes)
	for _, e := range edges {
		if e.From == e.To {
			return nil, false // self-loop is a cycle
		}
		succ[e.From] = append(succ[e.From], e.To)
		inDeg[e.To]++
	}
	var queue []int
	for v := 0; v < numNodes; v++ {
		if inDeg[v] == 0 {
			queue = append(queue, v)
		}
	}
	order := make([]int, 0, numNodes)
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		order = append(order, v)
		for _, w := range succ[v] {
			inDeg[w]--
			if inDeg[w] == 0 {
				queue = append(queue, w)
			}
		}
	}
	if len(order) != numNodes {
		return nil, false
	}
	return order, true
}

// ValidateTopologicalOrder checks that order is a permutation of all nodes
// and that every edge in edges goes forward in the ordering.
func ValidateTopologicalOrder(numNodes int, edges []Edge, order []int) bool {
	if len(order) != numNodes {
		return false
	}
	pos := make([]int, numNodes)
	seen := make([]bool, numNodes)
	for i, v := range order {
		if v < 0 || v >= numNodes || seen[v] {
			return false
		}
		seen[v] = true
		pos[v] = i
	}
	for _, e := range edges {
		if pos[e.From] >= pos[e.To] {
			return false
		}
	}
	return true
}

// hasPath reports whether a directed path from src to dst exists.
func hasPath(numNodes int, edges []Edge, src, dst int) bool {
	if src == dst {
		return true
	}
	succ := make([][]int, numNodes)
	for _, e := range edges {
		succ[e.From] = append(succ[e.From], e.To)
	}
	visited := make([]bool, numNodes)
	stack := []int{src}
	visited[src] = true
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, w := range succ[v] {
			if w == dst {
				return true
			}
			if !visited[w] {
				visited[w] = true
				stack = append(stack, w)
			}
		}
	}
	return false
}
