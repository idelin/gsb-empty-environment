package dom

// DominatesByRemoval is an independent dominance checker that does
// not share any code with the Analysis dataflow computation. It
// answers "does a dominate b" directly from the definition: a
// dominates b iff b is reachable from the entry, and removing a from
// the graph makes b unreachable.
//
// It works on any graph shape, including cycles, self-loops, and
// multi-entry simulations where a synthetic super-entry fans out to
// several real entry blocks. Because it performs a full graph
// traversal per query it is O(V+E) and intended for cross-checking
// the Analysis results on small graphs.
func DominatesByRemoval(g *Graph, a, b int) bool {
	if !reachableExcept(g, -1, b) {
		return false // b unreachable: dominance is undefined
	}
	if a == b {
		return true // a reachable node dominates itself
	}
	return !reachableExcept(g, a, b)
}

// reachableExcept reports whether target is reachable from the graph
// entry without passing through the node "banned" (use a negative
// value to ban nothing). The banned node is treated as deleted: the
// search neither starts from nor traverses it.
func reachableExcept(g *Graph, banned, target int) bool {
	entry := g.Entry()
	if entry == banned {
		return false
	}
	visited := map[int]bool{entry: true}
	queue := []int{entry}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n == target {
			return true
		}
		for _, s := range g.Successors(n) {
			if s == banned || visited[s] {
				continue
			}
			visited[s] = true
			queue = append(queue, s)
		}
	}
	return false
}
