package rematch

import "sort"

// buildChains decomposes the symmetric difference between the original and
// the new assignment into alternating adjustment chains. Each employee or
// position touches at most one old and one new edge, so the components of
// the difference are paths or cycles. Employees whose position is unchanged
// appear in no chain.
func buildChains(n, m int, orig, newPos []int, empNames, posNames []string) [][]ChainStep {
	adj := make(map[int][]int) // node -> adjacent nodes; employee i is i, position j is n+j
	addEdge := func(a, b int) {
		adj[a] = append(adj[a], b)
		adj[b] = append(adj[b], a)
	}
	for i := 0; i < n; i++ {
		if orig[i] == newPos[i] {
			continue
		}
		if orig[i] != -1 {
			addEdge(i, n+orig[i])
		}
		if newPos[i] != -1 {
			addEdge(i, n+newPos[i])
		}
	}
	visited := make(map[int]bool)
	var chains [][]ChainStep
	nodes := make([]int, 0, len(adj))
	for v := range adj {
		nodes = append(nodes, v)
	}
	sort.Ints(nodes)
	for _, start := range nodes {
		if visited[start] {
			continue
		}
		var comp []int
		queue := []int{start}
		visited[start] = true
		for len(queue) > 0 {
			v := queue[0]
			queue = queue[1:]
			comp = append(comp, v)
			for _, w := range adj[v] {
				if !visited[w] {
					visited[w] = true
					queue = append(queue, w)
				}
			}
		}
		chains = append(chains, walkComponent(comp, adj, n, orig, newPos, empNames, posNames))
	}
	sort.Slice(chains, func(a, b int) bool {
		return chains[a][0].Employee < chains[b][0].Employee
	})
	return chains
}

// walkComponent orders the employees of one path/cycle component along the
// alternating walk and emits one step per employee.
func walkComponent(comp []int, adj map[int][]int, n int, orig, newPos []int, empNames, posNames []string) []ChainStep {
	inComp := make(map[int]bool, len(comp))
	deg := make(map[int]int, len(comp))
	minEmp := -1
	for _, v := range comp {
		inComp[v] = true
		for _, w := range adj[v] {
			if inComp[w] || contains(comp, w) {
				deg[v]++
			}
		}
		if v < n && (minEmp == -1 || v < minEmp) {
			minEmp = v
		}
	}
	// Pick the walk start: a degree-1 endpoint for paths (preferring an
	// employee endpoint), otherwise the smallest employee for cycles.
	start := minEmp
	var endpoints []int
	for _, v := range comp {
		if deg[v] == 1 {
			endpoints = append(endpoints, v)
		}
	}
	if len(endpoints) > 0 {
		sort.Ints(endpoints)
		start = endpoints[0]
		for _, e := range endpoints {
			if e < n {
				start = e
				break
			}
		}
	}
	// Walk the component, collecting employees in walk order.
	var order []int
	prev, cur := -1, start
	for {
		if cur < n {
			order = append(order, cur)
		}
		next := -1
		for _, w := range adj[cur] {
			if inComp[w] && w != prev {
				if next == -1 || w < next {
					next = w
				}
			}
		}
		if next == -1 || next == start {
			break
		}
		prev, cur = cur, next
	}
	steps := make([]ChainStep, 0, len(order))
	for _, e := range order {
		steps = append(steps, ChainStep{
			Employee: empNames[e],
			From:     posName(orig[e], posNames),
			To:       posName(newPos[e], posNames),
		})
	}
	return steps
}

func posName(j int, posNames []string) string {
	if j == -1 {
		return ""
	}
	return posNames[j]
}

func contains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
