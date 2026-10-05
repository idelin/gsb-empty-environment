package edgecolor

// bruteForceMinColors computes the chromatic index of a small bipartite
// multigraph exactly, by set-partition DP over matchings. It is
// exponential and intended only for cross-checking tiny graphs in tests.
func bruteForceMinColors(edges [][2]int) int {
	n := len(edges)
	if n == 0 {
		return 0
	}
	full := 1<<uint(n) - 1

	// isMatching[mask]: no two edges in mask share an endpoint.
	isMatching := make([]bool, full+1)
	isMatching[0] = true
	for mask := 1; mask <= full; mask++ {
		bit := mask & -mask
		i := 0
		for b := bit; b > 1; b >>= 1 {
			i++
		}
		rest := mask ^ bit
		ok := isMatching[rest]
		for j := 0; ok && j < n; j++ {
			if rest&(1<<uint(j)) != 0 &&
				(edges[j][0] == edges[i][0] || edges[j][1] == edges[i][1]) {
				ok = false
			}
		}
		isMatching[mask] = ok
	}

	// BFS: dist[mask] = min rounds to schedule exactly the edges in mask.
	dist := make([]int, full+1)
	for i := range dist {
		dist[i] = -1
	}
	dist[0] = 0
	queue := []int{0}
	for len(queue) > 0 {
		mask := queue[0]
		queue = queue[1:]
		rem := full ^ mask
		for sub := rem; sub > 0; sub = (sub - 1) & rem {
			if isMatching[sub] && dist[mask|sub] < 0 {
				dist[mask|sub] = dist[mask] + 1
				queue = append(queue, mask|sub)
			}
		}
	}
	return dist[full]
}
