// Package main implements Régin's filtering algorithm for the
// AllDifferent global constraint.
//
// The algorithm builds a bipartite graph between variables and values,
// computes a maximum matching (Hopcroft–Karp), then analyses the
// residual directed graph (alternating reachability from free value
// nodes + strongly connected components via Tarjan) to remove every
// variable-value edge that cannot belong to any maximum matching,
// i.e. any value that cannot appear in any feasible solution.
package main

import (
	"fmt"
	"sort"
	"strings"
)

// Variable is a finite-domain integer variable.
type Variable struct {
	Name   string
	Domain map[int]bool
}

// NewVariable creates a variable whose domain is the given values.
func NewVariable(name string, values ...int) *Variable {
	d := make(map[int]bool, len(values))
	for _, v := range values {
		d[v] = true
	}
	return &Variable{Name: name, Domain: d}
}

// SortedDomain returns the domain values in ascending order.
func (v *Variable) SortedDomain() []int {
	out := make([]int, 0, len(v.Domain))
	for val := range v.Domain {
		out = append(out, val)
	}
	sort.Ints(out)
	return out
}

func (v *Variable) String() string {
	parts := make([]string, 0, len(v.Domain))
	for _, val := range v.SortedDomain() {
		parts = append(parts, fmt.Sprintf("%d", val))
	}
	return fmt.Sprintf("%s ∈ {%s}", v.Name, strings.Join(parts, ", "))
}

// AllDifferent is the global constraint: every variable must take a
// distinct value.
type AllDifferent struct {
	Vars []*Variable
}

// NewAllDifferent builds the constraint over the given variables.
func NewAllDifferent(vars ...*Variable) *AllDifferent {
	return &AllDifferent{Vars: vars}
}

// matching holds the result of the bipartite maximum matching between
// variables (left) and values (right).
type matching struct {
	matchVar  map[int]int // variable index -> matched value
	matchVal  map[int]int // value -> matched variable index
	size      int         // number of matched edges
	values    []int       // sorted list of all values in the union of domains
	valueNode map[int]int // value -> node id in the residual graph
}

// maximumMatching runs Hopcroft–Karp on the variable/value bipartite graph.
func (c *AllDifferent) maximumMatching() *matching {
	n := len(c.Vars)

	valueSet := make(map[int]bool)
	for _, v := range c.Vars {
		for val := range v.Domain {
			valueSet[val] = true
		}
	}
	values := make([]int, 0, len(valueSet))
	for val := range valueSet {
		values = append(values, val)
	}
	sort.Ints(values)

	m := &matching{
		matchVar:  make(map[int]int),
		matchVal:  make(map[int]int),
		values:    values,
		valueNode: make(map[int]int),
	}
	for i, val := range values {
		m.valueNode[val] = n + i
	}

	// Hopcroft–Karp. Left side: variables 0..n-1.
	pairU := make([]int, n)    // var -> matched value-node id, -1 if free
	pairV := make(map[int]int) // value-node id -> matched var, -1 if free
	dist := make([]int, n)
	for i := range pairU {
		pairU[i] = -1
	}
	for _, val := range values {
		pairV[m.valueNode[val]] = -1
	}

	adj := make([][]int, n) // var -> adjacent value-node ids
	for i, v := range c.Vars {
		for _, val := range v.SortedDomain() {
			adj[i] = append(adj[i], m.valueNode[val])
		}
	}

	const inf = int(^uint(0) >> 1)

	bfs := func() bool {
		queue := make([]int, 0, n)
		foundFree := false
		for u := 0; u < n; u++ {
			if pairU[u] == -1 {
				dist[u] = 0
				queue = append(queue, u)
			} else {
				dist[u] = inf
			}
		}
		for head := 0; head < len(queue); head++ {
			u := queue[head]
			for _, vnode := range adj[u] {
				w := pairV[vnode]
				if w == -1 {
					foundFree = true
				} else if dist[w] == inf {
					dist[w] = dist[u] + 1
					queue = append(queue, w)
				}
			}
		}
		return foundFree
	}

	var dfs func(u int) bool
	dfs = func(u int) bool {
		for _, vnode := range adj[u] {
			w := pairV[vnode]
			if w == -1 || (dist[w] == dist[u]+1 && dfs(w)) {
				pairU[u] = vnode
				pairV[vnode] = u
				return true
			}
		}
		dist[u] = inf
		return false
	}

	for bfs() {
		for u := 0; u < n; u++ {
			if pairU[u] == -1 && dfs(u) {
				m.size++
			}
		}
	}

	for u := 0; u < n; u++ {
		if pairU[u] != -1 {
			m.matchVar[u] = values[pairU[u]-n]
		}
	}
	for vnode, u := range pairV {
		if u != -1 {
			m.matchVal[values[vnode-n]] = u
		}
	}
	return m
}

// Propagate runs Régin's filtering algorithm.
//
// It returns false when the constraint is already infeasible (the
// maximum matching cannot cover all variables — detected instantly,
// before any search), and true otherwise. In the feasible case every
// value that cannot belong to any maximum matching is removed from its
// variable's domain (generalised arc consistency).
func (c *AllDifferent) Propagate() bool {
	n := len(c.Vars)
	m := c.maximumMatching()
	if m.size < n {
		// Hall's condition violated: fewer values than variables can be
		// matched, so no assignment can make all variables different.
		return false
	}

	// Residual directed graph over n variable nodes and len(m.values)
	// value nodes:
	//   unmatched edge (x,v): x -> v
	//   matched   edge (x,v): v -> x
	total := n + len(m.values)
	adj := make([][]int, total)
	matchedEdge := make(map[[2]int]bool) // [varIndex, value]
	for i, v := range c.Vars {
		matchedVal, isMatched := m.matchVar[i]
		for _, val := range v.SortedDomain() {
			vnode := m.valueNode[val]
			if isMatched && val == matchedVal {
				adj[vnode] = append(adj[vnode], i)
				matchedEdge[[2]int{i, val}] = true
			} else {
				adj[i] = append(adj[i], vnode)
			}
		}
	}

	// Nodes reachable from free (unmatched) value nodes via alternating
	// paths. Since all variables are matched, only value nodes can be free.
	reachable := make([]bool, total)
	var markReachable func(start int)
	markReachable = func(start int) {
		if reachable[start] {
			return
		}
		stack := []int{start}
		reachable[start] = true
		for len(stack) > 0 {
			u := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, w := range adj[u] {
				if !reachable[w] {
					reachable[w] = true
					stack = append(stack, w)
				}
			}
		}
	}
	for _, val := range m.values {
		if _, matched := m.matchVal[val]; !matched {
			markReachable(m.valueNode[val])
		}
	}

	// Strongly connected components (Tarjan, iterative).
	scc := tarjanSCC(adj)

	// An edge (x,v) belongs to some maximum matching iff it is matched,
	// or v is reachable from a free value node, or x and v lie in the
	// same SCC. Everything else is pruned.
	for i, v := range c.Vars {
		for _, val := range v.SortedDomain() {
			if matchedEdge[[2]int{i, val}] {
				continue
			}
			vnode := m.valueNode[val]
			if reachable[vnode] || scc[i] == scc[vnode] {
				continue
			}
			delete(v.Domain, val)
		}
		if len(v.Domain) == 0 {
			return false
		}
	}
	return true
}

// tarjanSCC computes strongly connected components of a directed graph
// given as adjacency lists. It returns, for each node, the id of its
// component (nodes in the same component share the same id).
func tarjanSCC(adj [][]int) []int {
	n := len(adj)
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	comp := make([]int, n)
	for i := range index {
		index[i] = -1
		comp[i] = -1
	}
	counter := 0
	ncomp := 0
	var stack []int

	type frame struct {
		u, child int
	}
	for s := 0; s < n; s++ {
		if index[s] != -1 {
			continue
		}
		callStack := []frame{{s, 0}}
		index[s] = counter
		low[s] = counter
		counter++
		stack = append(stack, s)
		onStack[s] = true

		for len(callStack) > 0 {
			f := &callStack[len(callStack)-1]
			u := f.u
			if f.child < len(adj[u]) {
				w := adj[u][f.child]
				f.child++
				if index[w] == -1 {
					index[w] = counter
					low[w] = counter
					counter++
					stack = append(stack, w)
					onStack[w] = true
					callStack = append(callStack, frame{w, 0})
				} else if onStack[w] && index[w] < low[u] {
					low[u] = index[w]
				}
			} else {
				callStack = callStack[:len(callStack)-1]
				if low[u] == index[u] {
					for {
						w := stack[len(stack)-1]
						stack = stack[:len(stack)-1]
						onStack[w] = false
						comp[w] = ncomp
						if w == u {
							break
						}
					}
					ncomp++
				}
				if len(callStack) > 0 {
					p := callStack[len(callStack)-1].u
					if low[u] < low[p] {
						low[p] = low[u]
					}
				}
			}
		}
	}
	return comp
}
