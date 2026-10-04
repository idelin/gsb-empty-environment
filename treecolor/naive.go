package treecolor

// SolveNaive is the reference implementation: it replays the operation
// sequence one by one, and for every query enumerates the simple path
// between the endpoints with BFS, counting distinct colors directly.
// It runs in O(n) per operation and exists to cross-check Solve.
func SolveNaive(n int, edges [][2]int, init []int64, ops []Op) []int {
	adj := make([][]int, n)
	for _, e := range edges {
		adj[e[0]] = append(adj[e[0]], e[1])
		adj[e[1]] = append(adj[e[1]], e[0])
	}
	colors := make([]int64, n)
	copy(colors, init)

	answers := []int{}
	for _, op := range ops {
		if op.Kind == OpUpdate {
			colors[op.Node] = op.Color
			continue
		}
		// BFS from U to V, tracking parents to reconstruct the path.
		prev := make([]int, n)
		for i := range prev {
			prev[i] = -1
		}
		prev[op.U] = op.U
		queue := []int{op.U}
		for len(queue) > 0 && prev[op.V] == -1 {
			v := queue[0]
			queue = queue[1:]
			for _, to := range adj[v] {
				if prev[to] == -1 {
					prev[to] = v
					queue = append(queue, to)
				}
			}
		}
		seen := map[int64]bool{}
		for v := op.V; ; v = prev[v] {
			seen[colors[v]] = true
			if v == op.U {
				break
			}
		}
		answers = append(answers, len(seen))
	}
	return answers
}
