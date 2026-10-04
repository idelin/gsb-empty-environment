package treepath

// NaiveDistinctColorsOnPaths is the reference implementation used for
// cross-checking. It applies ops strictly in stream order and answers each
// query by enumerating the u-v path node by node (BFS parent walk), so it
// has no notion of a root at all.
//
// Complexity: O(N) per query and O(1) per update, O(N + Q*N) total time,
// O(N) memory beyond the answers.
func NaiveDistinctColorsOnPaths(n int, edges []Edge, initColors []int, ops []Op) []int {
	adj := make([][]int, n)
	for _, e := range edges {
		adj[e.U] = append(adj[e.U], e.V)
		adj[e.V] = append(adj[e.V], e.U)
	}
	cur := append([]int(nil), initColors...)
	var answers []int
	for _, op := range ops {
		if op.Kind == OpRecolor {
			cur[op.A] = op.B
			continue
		}
		// BFS from A, then walk parents from B back to A.
		parent := make([]int, n)
		for i := range parent {
			parent[i] = -2
		}
		parent[op.A] = -1
		queue := []int{op.A}
		for len(queue) > 0 && parent[op.B] == -2 {
			v := queue[0]
			queue = queue[1:]
			for _, to := range adj[v] {
				if parent[to] == -2 {
					parent[to] = v
					queue = append(queue, to)
				}
			}
		}
		seen := make(map[int]struct{})
		for v := op.B; v != -1; v = parent[v] {
			seen[cur[v]] = struct{}{}
		}
		answers = append(answers, len(seen))
	}
	return answers
}
