package graphdec

import "fmt"

// analyze is the independent graph analyzer. It resolves every reference
// to a node index, enforces that all declared nodes are reachable from
// the root, computes the SCC condensation of the reachable graph, and
// checks the depth and cycle-node quotas. It returns the resolved
// adjacency (node index -> successor indices) on success.
//
// All traversals are iterative so hostile inputs cannot exhaust the
// call stack.
func analyze(nodes []*node, ids []uint64, root int, lim Limits) ([][]int, error) {
	n := len(nodes)
	index := make(map[uint64]int, n)
	for i, id := range ids {
		index[id] = i
	}
	// Resolve references. Duplicate ids were rejected during parsing, so
	// every reference is guaranteed to resolve here; a miss means the
	// staging invariants were broken and is reported as an illegal
	// offset rather than silently ignored.
	edges := make([][]int, n)
	for i := 0; i < n; i++ {
		nd := nodes[i]
		e := make([]int, len(nd.refs))
		for j, id := range nd.refs {
			t, ok := index[id]
			if !ok {
				return nil, fmt.Errorf("graphdec: node %d references unknown id %d", ids[i], id)
			}
			e[j] = t
		}
		edges[i] = e
	}

	// Reachability: iterative DFS from the root. Unreachable nodes are
	// rejected so garbage cannot hide behind illegal offsets.
	visited := make([]bool, n)
	order := make([]int, 0, n)
	type frame struct{ v, next int }
	visited[root] = true
	stack := []frame{{v: root}}
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.next < len(edges[f.v]) {
			w := edges[f.v][f.next]
			f.next++
			if !visited[w] {
				visited[w] = true
				stack = append(stack, frame{v: w})
			}
		} else {
			order = append(order, f.v)
			stack = stack[:len(stack)-1]
		}
	}
	if len(order) != n {
		return nil, fmt.Errorf("graphdec: %d of %d nodes unreachable from root", n-len(order), n)
	}

	// Kosaraju second pass on the transpose graph, processing vertices
	// in decreasing finish time. Components are numbered in topological
	// order of the condensation DAG (edges go low -> high).
	radj := make([][]int, n)
	for v := 0; v < n; v++ {
		for _, w := range edges[v] {
			radj[w] = append(radj[w], v)
		}
	}
	comp := make([]int, n)
	for i := range comp {
		comp[i] = -1
	}
	compSize := make([]int, 0, n)
	for i := n - 1; i >= 0; i-- {
		s := order[i]
		if comp[s] != -1 {
			continue
		}
		cid := len(compSize)
		size := 0
		comp[s] = cid
		dfs := []int{s}
		for len(dfs) > 0 {
			v := dfs[len(dfs)-1]
			dfs = dfs[:len(dfs)-1]
			size++
			for _, w := range radj[v] {
				if comp[w] == -1 {
					comp[w] = cid
					dfs = append(dfs, w)
				}
			}
		}
		compSize = append(compSize, size)
	}
	numComp := len(compSize)

	// Cycle quota: nodes in non-trivial SCCs, plus self-looping nodes.
	cycleNodes := 0
	for _, s := range compSize {
		if s > 1 {
			cycleNodes += s
		}
	}
	for v := 0; v < n; v++ {
		if compSize[comp[v]] == 1 {
			for _, w := range edges[v] {
				if w == v {
					cycleNodes++
					break
				}
			}
		}
	}
	if cycleNodes > lim.MaxCycleNodes {
		return nil, fmt.Errorf("graphdec: %d nodes in cycles exceeds cycle-node budget %d", cycleNodes, lim.MaxCycleNodes)
	}

	// Depth: longest path (in components) of the condensation DAG from
	// the root component. Component numbers are a topological order, so
	// a reverse scan yields longest paths directly.
	compAdj := make([][]int, numComp)
	for v := 0; v < n; v++ {
		for _, w := range edges[v] {
			if comp[v] != comp[w] {
				compAdj[comp[v]] = append(compAdj[comp[v]], comp[w])
			}
		}
	}
	dp := make([]int, numComp)
	for c := numComp - 1; c >= 0; c-- {
		best := 0
		for _, succ := range compAdj[c] {
			if dp[succ] > best {
				best = dp[succ]
			}
		}
		dp[c] = 1 + best
	}
	depth := dp[comp[root]]
	if depth > lim.MaxDepth {
		return nil, fmt.Errorf("graphdec: condensation depth %d exceeds depth budget %d", depth, lim.MaxDepth)
	}

	return edges, nil
}
