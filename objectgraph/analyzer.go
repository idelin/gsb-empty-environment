package objectgraph

import "sort"

// Stats is the result of an independent graph analysis. The analysis works on
// plain adjacency lists and is used by the decoder before it publishes any
// object; it is also exported for standalone use in tests and tooling.
type Stats struct {
	// Reachable are node indices reachable from Root, including Root.
	Reachable []int
	// Unreachable are indices that cannot be reached from Root.
	Unreachable []int
	// Components lists SCC members. Each component's members are sorted, and
	// components are ordered by smallest member.
	Components [][]int
	// CompOf gives the component id of each node (-1 if unreachable).
	CompOf []int
	// CyclicNodes is the number of distinct reachable entities that belong
	// to a non-trivial SCC (more than one member, or a self-loop).
	CyclicNodes int
	// Depth is the number of SCC vertices on the longest path in the
	// reachable SCC condensation DAG. Cycles do not increase Depth: a graph
	// that is one big SCC has Depth 1 regardless of DFS traversal order.
	Depth int
}

// AnalyzeGraph performs reachability, SCC and condensation-depth analysis.
//
// Depth is defined structurally: compress the root-reachable graph into its
// SCC condensation DAG, then take the longest path measured in vertices.
// Because SCCs are order independent, the answer does not depend on any DFS
// edge ordering. All traversals are iterative, so pathological graphs do not
// overflow the goroutine stack.
func AnalyzeGraph(adj [][]int, root int) (*Stats, error) {
	n := len(adj)
	if root < 0 || root >= n {
		return nil, wrapf(ErrRootIndex, "root %d with %d nodes", root, n)
	}

	// Reachability from root (iterative).
	reachable := make([]bool, n)
	order := make([]int, 0, n)
	stack := make([]int, 0, 16)
	reachable[root] = true
	stack = append(stack, root)
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		order = append(order, u)
		for _, v := range adj[u] {
			if v < 0 || v >= n {
				return nil, wrapf(ErrBadRef, "node %d edge -> %d (nodes=%d)", u, v, n)
			}
			if !reachable[v] {
				reachable[v] = true
				stack = append(stack, v)
			}
		}
	}
	unreachable := make([]int, 0)
	for i := 0; i < n; i++ {
		if !reachable[i] {
			unreachable = append(unreachable, i)
		}
	}

	// Local indices for the reachable subgraph, keeping original ids.
	local := make([]int, n)
	for i := range local {
		local[i] = -1
	}
	ids := make([]int, 0, len(order))
	for _, u := range order {
		local[u] = len(ids)
		ids = append(ids, u)
	}
	rn := len(ids)

	radj := make([][]int, rn)
	fadj := make([][]int, rn)
	for li, u := range ids {
		for _, v := range adj[u] {
			lj := local[v]
			if lj < 0 {
				continue // edges to unreachable nodes cannot occur for reachable u
			}
			fadj[li] = append(fadj[li], lj)
			radj[lj] = append(radj[lj], li)
		}
	}

	// Kosaraju pass 1: finishing order on fadj.
	seen := make([]bool, rn)
	finish := make([]int, 0, rn)
	for s := 0; s < rn; s++ {
		if seen[s] {
			continue
		}
		// Iterative DFS with an explicit per-node edge cursor.
		vStack := []int{s}
		eStack := []int{0}
		seen[s] = true
		for len(vStack) > 0 {
			u := vStack[len(vStack)-1]
			e := eStack[len(eStack)-1]
			if e < len(fadj[u]) {
				w := fadj[u][e]
				eStack[len(eStack)-1] = e + 1
				if !seen[w] {
					seen[w] = true
					vStack = append(vStack, w)
					eStack = append(eStack, 0)
				}
				continue
			}
			finish = append(finish, u)
			vStack = vStack[:len(vStack)-1]
			eStack = eStack[:len(eStack)-1]
		}
	}

	// Pass 2 on reverse graph in decreasing finish order assigns SCCs.
	compOfLocal := make([]int, rn)
	for i := range compOfLocal {
		compOfLocal[i] = -1
	}
	comps := make([][]int, 0)
	for k := rn - 1; k >= 0; k-- {
		s := finish[k]
		if compOfLocal[s] != -1 {
			continue
		}
		cid := len(comps)
		members := make([]int, 0)
		st := []int{s}
		compOfLocal[s] = cid
		for len(st) > 0 {
			u := st[len(st)-1]
			st = st[:len(st)-1]
			members = append(members, ids[u])
			for _, w := range radj[u] {
				if compOfLocal[w] == -1 {
					compOfLocal[w] = cid
					st = append(st, w)
				}
			}
		}
		sort.Ints(members)
		comps = append(comps, members)
	}
	// Kosaraju emits components in decreasing finish order; reorder by the
	// smallest original member for stable, traversal-independent output.
	sortComponents(comps)

	compOf := make([]int, n)
	for i := range compOf {
		compOf[i] = -1
	}
	for li := 0; li < rn; li++ {
		compOf[ids[li]] = compOfLocal[li]
	}

	// Cyclic entities: SCCs of size > 1, or single-node SCCs with self-loops.
	cn := len(comps)
	cyclic := make([]bool, cn)
	cyclicNodes := 0
	for ci, members := range comps {
		if len(members) > 1 {
			cyclic[ci] = true
			cyclicNodes += len(members)
			continue
		}
		u := members[0]
		for _, v := range adj[u] {
			if v == u {
				cyclic[ci] = true
				cyclicNodes++
				break
			}
		}
	}

	// Condensation edges (deduped).
	cadj := make([]map[int]struct{}, cn)
	for i := range cadj {
		cadj[i] = make(map[int]struct{})
	}
	indeg := make([]int, cn)
	for li, u := range ids {
		ci := compOfLocal[li]
		for _, v := range adj[u] {
			cj := compOfLocal[local[v]]
			if cj == ci {
				continue
			}
			if _, ok := cadj[ci][cj]; !ok {
				cadj[ci][cj] = struct{}{}
				indeg[cj]++
			}
		}
	}

	// Longest path in vertices over the condensation DAG (Kahn + DP).
	longest := make([]int, cn)
	queue := make([]int, 0, cn)
	for i := 0; i < cn; i++ {
		longest[i] = 1
		if indeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	depth := 0
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		if longest[u] > depth {
			depth = longest[u]
		}
		for v := range cadj[u] {
			if cand := longest[u] + 1; cand > longest[v] {
				longest[v] = cand
			}
			indeg[v]--
			if indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}

	return &Stats{
		Reachable:   append([]int(nil), order...),
		Unreachable: unreachable,
		Components:  comps,
		CompOf:      compOf,
		CyclicNodes: cyclicNodes,
		Depth:       depth,
	}, nil
}

func sortInts(a []int) {
	// insertion sort is avoided for large slices; use a simple quicksort.
	if len(a) < 2 {
		return
	}
	quickSort(a, 0, len(a))
}

func quickSort(a []int, lo, hi int) {
	for hi-lo > 12 {
		mid := lo + (hi-lo)/2
		// median-of-three pivot placement
		if a[lo] > a[mid] {
			a[lo], a[mid] = a[mid], a[lo]
		}
		if a[lo] > a[hi-1] {
			a[lo], a[hi-1] = a[hi-1], a[lo]
		}
		if a[mid] < a[hi-1] {
			a[mid], a[hi-1] = a[hi-1], a[mid]
		}
		pivot := a[hi-1]
		i := lo
		for j := lo; j < hi-1; j++ {
			if a[j] < pivot {
				a[i], a[j] = a[j], a[i]
				i++
			}
		}
		a[i], a[hi-1] = a[hi-1], a[i]
		quickSort(a, lo, i)
		lo = i + 1
	}
	for i := lo + 1; i < hi; i++ {
		for j := i; j > lo && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

func sortComponents(comps [][]int) {
	// Simple insertion sort: component counts are modest relative to nodes.
	for i := 1; i < len(comps); i++ {
		for j := i; j > 0 && comps[j-1][0] > comps[j][0]; j-- {
			comps[j-1], comps[j] = comps[j], comps[j-1]
		}
	}
}
