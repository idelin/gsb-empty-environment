package iflow

// ProvNode is one node in the provenance DAG. Descr is a human-readable
// description of the program point; Prev points at the nodes this value was
// derived from. Nodes with no Prev are sources (labelled inputs).
type ProvNode struct {
	ID    int
	Descr string
	Prev  []*ProvNode
}

// ProvGraph accumulates provenance nodes during analysis.
type ProvGraph struct {
	nodes []*ProvNode
}

// Add appends a node with the given predecessors and returns it.
func (g *ProvGraph) Add(descr string, prev ...*ProvNode) *ProvNode {
	n := &ProvNode{ID: len(g.nodes), Descr: descr, Prev: prev}
	g.nodes = append(g.nodes, n)
	return n
}

// ShortestChain runs BFS from `from` backwards along Prev edges and returns
// the shortest path to a source node, ordered source-first (ending in `from`).
func (g *ProvGraph) ShortestChain(from *ProvNode) []*ProvNode {
	if from == nil {
		return nil
	}
	visited := map[*ProvNode]bool{from: true}
	next := map[*ProvNode]*ProvNode{}
	queue := []*ProvNode{from}
	var src *ProvNode
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if len(cur.Prev) == 0 {
			src = cur
			break
		}
		for _, p := range cur.Prev {
			if !visited[p] {
				visited[p] = true
				next[p] = cur
				queue = append(queue, p)
			}
		}
	}
	if src == nil {
		return []*ProvNode{from}
	}
	var chain []*ProvNode
	for n := src; ; n = next[n] {
		chain = append(chain, n)
		if n == from {
			break
		}
	}
	return chain
}
