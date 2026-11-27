package txn

import "sort"

// edgeNode is a mutable ordering edge used while cycles are being broken.
type edgeNode struct {
	from, to string
	reversed bool
	reason   string
}

// buildOrderEdges projects the static Before/After relations onto retained
// jobs. Requirements contribute no ordering edges (orthogonality). When either
// endpoint is a stop job the edge direction is reversed so that shutdown
// executes in the opposite order of startup.
func buildOrderEdges(jobs map[string]*job, idx *depIndex) []edgeNode {
	edges := []edgeNode{}
	seen := map[[2]string]struct{}{}
	for _, rel := range idx.orders {
		if _, ok := jobs[rel.x]; !ok {
			continue
		}
		if _, ok := jobs[rel.y]; !ok {
			continue
		}
		reversed := !jobs[rel.x].typ.startLike() || !jobs[rel.y].typ.startLike()
		from, to := rel.x, rel.y
		if reversed {
			from, to = rel.y, rel.x
		}
		key := [2]string{from, to}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		edges = append(edges, edgeNode{
			from:     from,
			to:       to,
			reversed: reversed,
			reason:   rel.reason,
		})
	}
	return edges
}

// breakCycles repeatedly locates the shortest (minimal) cycle inside a
// strongly connected component and deletes one deterministically chosen edge
// until the ordering graph is a DAG. No jobs are removed to break a cycle.
//
// Edge choice: an edge is critical when either endpoint is an anchor job.
// Non-critical edges are preferred; ties, including all-critical cycles, are
// resolved by the (from,to) lexicographic order.
func breakCycles(nodes []string, edges []edgeNode, jobs map[string]*job) ([]edgeNode, []CutReport) {
	cuts := []CutReport{}
	for {
		adj := adjacency(nodes, edges)
		component, ok := firstCyclicComponent(adj, nodes)
		if !ok {
			return edges, cuts
		}
		cycle := minimalCycle(adj, component)
		cutIdx := selectCycleEdge(edges, cycle, jobs)
		cut := edges[cutIdx]
		cuts = append(cuts, CutReport{
			Cycle: append([]string(nil), cycle...),
			Edge: OrderEdge{
				From:     cut.from,
				To:       cut.to,
				Reversed: cut.reversed,
				Reason:   cut.reason,
			},
		})
		edges = append(edges[:cutIdx], edges[cutIdx+1:]...)
	}
}

func adjacency(nodes []string, edges []edgeNode) map[string][]string {
	adj := map[string][]string{}
	for _, n := range nodes {
		adj[n] = []string{}
	}
	for _, e := range edges {
		adj[e.from] = append(adj[e.from], e.to)
	}
	for k := range adj {
		sort.Strings(adj[k])
	}
	return adj
}

// firstCyclicComponent runs Tarjan's SCC algorithm and returns the first
// component (by smallest member name) that contains a cycle, including a
// self-loop.
func firstCyclicComponent(adj map[string][]string, nodes []string) ([]string, bool) {
	index := 0
	stack := []string{}
	onStack := map[string]bool{}
	indices := map[string]int{}
	low := map[string]int{}
	components := [][]string{}

	var strongConnect func(v string)
	strongConnect = func(v string) {
		indices[v] = index
		low[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range adj[v] {
			if _, seen := indices[w]; !seen {
				strongConnect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && indices[w] < low[v] {
				low[v] = indices[w]
			}
		}
		if low[v] == indices[v] {
			comp := []string{}
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			sort.Strings(comp)
			components = append(components, comp)
		}
	}

	sorted := append([]string(nil), nodes...)
	sort.Strings(sorted)
	for _, n := range sorted {
		if _, seen := indices[n]; !seen {
			strongConnect(n)
		}
	}

	sort.Slice(components, func(i, j int) bool {
		return components[i][0] < components[j][0]
	})
	for _, comp := range components {
		if isCyclic(adj, comp) {
			return comp, true
		}
	}
	return nil, false
}

// isCyclic reports whether an SCC really contains a cycle: a self-loop or at
// least one internal edge (a multi-node SCC always does).
func isCyclic(adj map[string][]string, comp []string) bool {
	if len(comp) > 1 {
		return true
	}
	only := comp[0]
	for _, to := range adj[only] {
		if to == only {
			return true
		}
	}
	return false
}

// minimalCycle returns the shortest directed cycle contained in the component.
// BFS is run from every member (in sorted order); ties are resolved by the
// lexicographically smallest ordered list of units, guaranteeing determinism.
func minimalCycle(adj map[string][]string, comp []string) []string {
	member := map[string]bool{}
	for _, n := range comp {
		member[n] = true
	}
	var best []string
	for _, start := range comp {
		parent := map[string]string{}
		visited := map[string]bool{start: true}
		queue := []string{start}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, next := range adj[cur] {
				if !member[next] {
					continue
				}
				if next == start {
					if cur == start {
						best = betterCycle(best, []string{start})
						queue = nil
						break
					}
					cycle := reconstructCycle(parent, cur, start)
					best = betterCycle(best, cycle)
					queue = nil
					break
				}
				if !visited[next] {
					visited[next] = true
					parent[next] = cur
					queue = append(queue, next)
				}
			}
		}
	}
	return best
}

func reconstructCycle(parent map[string]string, end, start string) []string {
	path := []string{end}
	for path[len(path)-1] != start {
		path = append(path, parent[path[len(path)-1]])
	}
	// Reverse so the list starts at the closing node and follows edges.
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

func betterCycle(best, candidate []string) []string {
	if best == nil || len(candidate) < len(best) {
		return candidate
	}
	if len(candidate) == len(best) {
		for i := range candidate {
			if candidate[i] != best[i] {
				if candidate[i] < best[i] {
					return candidate
				}
				return best
			}
		}
	}
	return best
}

// selectCycleEdge returns the index of the edge to delete for the given
// cycle, preferring non-critical edges and breaking ties lexicographically.
func selectCycleEdge(edges []edgeNode, cycle []string, jobs map[string]*job) int {
	inCycle := map[[2]string]bool{}
	for i := range cycle {
		from := cycle[i]
		to := cycle[(i+1)%len(cycle)]
		inCycle[[2]string{from, to}] = true
	}
	critical := func(e edgeNode) bool {
		return jobs[e.from].anchor || jobs[e.to].anchor
	}
	bestIdx := -1
	for i, e := range edges {
		if !inCycle[[2]string{e.from, e.to}] {
			continue
		}
		if bestIdx == -1 {
			bestIdx = i
			continue
		}
		best := edges[bestIdx]
		curCritical, bestCritical := critical(e), critical(best)
		if curCritical != bestCritical {
			if !curCritical {
				bestIdx = i
			}
			continue
		}
		if e.from != best.from {
			if e.from < best.from {
				bestIdx = i
			}
			continue
		}
		if e.to < best.to {
			bestIdx = i
		}
	}
	return bestIdx
}

// executionLayers performs Kahn's algorithm on the DAG, emitting parallel
// layers with lexicographically sorted members for deterministic scheduling.
func executionLayers(nodes []string, edges []edgeNode) [][]string {
	indegree := map[string]int{}
	adj := map[string][]string{}
	for _, n := range nodes {
		indegree[n] = 0
		adj[n] = []string{}
	}
	for _, e := range edges {
		adj[e.from] = append(adj[e.from], e.to)
		indegree[e.to]++
	}

	layers := [][]string{}
	ready := []string{}
	for _, n := range nodes {
		if indegree[n] == 0 {
			ready = append(ready, n)
		}
	}
	for len(ready) > 0 {
		sort.Strings(ready)
		layers = append(layers, ready)
		next := []string{}
		for _, n := range ready {
			for _, to := range adj[n] {
				indegree[to]--
				if indegree[to] == 0 {
					next = append(next, to)
				}
			}
		}
		ready = next
	}
	return layers
}
