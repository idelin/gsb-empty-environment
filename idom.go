package flowgraph

// Dominators is the result of analyzing a Graph: the immediate dominator
// of every node reachable from the entry.
//
// Semantics:
//   - the entry node is its own immediate dominator;
//   - every other reachable node has a distinct immediate dominator;
//   - unreachable nodes have no dominator at all.
type Dominators struct {
	entry int
	idom  []int // -1 means "no dominator" (unreachable node)
}

// Analyze computes the immediate dominators of g from its entry node.
// It may be called again after edges are added; each call performs a
// fresh analysis of the current graph.
func (g *Graph) Analyze() *Dominators {
	n := g.numNodes
	idom := make([]int, n)
	for i := range idom {
		idom[i] = -1
	}
	idom[g.entry] = g.entry

	// Reverse postorder over the reachable subgraph (iterative DFS).
	rpo, rpoIndex := reversePostOrder(g)

	// Cooper-Harvey-Kennedy iterative algorithm.
	intersect := func(a, b int) int {
		for a != b {
			for rpoIndex[a] > rpoIndex[b] {
				a = idom[a]
			}
			for rpoIndex[b] > rpoIndex[a] {
				b = idom[b]
			}
		}
		return a
	}

	changed := true
	for changed {
		changed = false
		for _, v := range rpo {
			if v == g.entry {
				continue
			}
			newIDom := -1
			for _, p := range g.pred[v] {
				if idom[p] == -1 {
					continue // unreachable or not yet processed
				}
				if newIDom == -1 {
					newIDom = p
				} else {
					newIDom = intersect(p, newIDom)
				}
			}
			if newIDom != idom[v] {
				idom[v] = newIDom
				changed = true
			}
		}
	}

	return &Dominators{entry: g.entry, idom: idom}
}

// reversePostOrder returns the nodes reachable from the entry in reverse
// postorder, plus a per-node RPO index (-1 for unreachable nodes).
func reversePostOrder(g *Graph) ([]int, []int) {
	n := g.numNodes
	rpoIndex := make([]int, n)
	for i := range rpoIndex {
		rpoIndex[i] = -1
	}

	var postorder []int
	visited := make([]bool, n)
	type frame struct {
		node int
		next int // index of next successor to visit
	}
	stack := []frame{{node: g.entry}}
	visited[g.entry] = true
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.next < len(g.succ[top.node]) {
			s := g.succ[top.node][top.next]
			top.next++
			if !visited[s] {
				visited[s] = true
				stack = append(stack, frame{node: s})
			}
		} else {
			postorder = append(postorder, top.node)
			stack = stack[:len(stack)-1]
		}
	}

	rpo := make([]int, len(postorder))
	for i, v := range postorder {
		rpo[len(postorder)-1-i] = v
	}
	for i, v := range rpo {
		rpoIndex[v] = i
	}
	return rpo, rpoIndex
}

// Idom returns the immediate dominator of node. The second result is
// false when node is unreachable from the entry (no dominator). For the
// entry node it returns (entry, true).
func (d *Dominators) Idom(node int) (int, bool) {
	if node < 0 || node >= len(d.idom) || d.idom[node] == -1 {
		return 0, false
	}
	return d.idom[node], true
}

// Reachable reports whether node is reachable from the entry.
func (d *Dominators) Reachable(node int) bool {
	return node >= 0 && node < len(d.idom) && d.idom[node] != -1
}

// Entry returns the entry node of the analyzed graph.
func (d *Dominators) Entry() int { return d.entry }
