package main

import (
	"bufio"
	"fmt"
	"io"
	"sort"
)

// Graph is a directed graph with non-negative vertex deletion costs.
type Graph struct {
	N    int
	Adj  [][]int // outgoing edges (deduplicated)
	Radj [][]int // incoming edges (deduplicated)
	Cost []int64 // non-negative deletion cost per vertex
}

// ParseGraph reads a graph from r.
// Format:
//   line 1: n m
//   line 2: c0 c1 ... c(n-1)   (non-negative integer costs)
//   next m lines: u v          (directed edge u -> v, 0-based; self loops allowed)
func ParseGraph(r io.Reader) (*Graph, error) {
	in := bufio.NewReader(r)
	var n, m int
	if _, err := fmt.Fscan(in, &n, &m); err != nil {
		return nil, fmt.Errorf("reading n m: %v", err)
	}
	if n < 0 || m < 0 {
		return nil, fmt.Errorf("negative n or m")
	}
	g := &Graph{
		N:    n,
		Adj:  make([][]int, n),
		Radj: make([][]int, n),
		Cost: make([]int64, n),
	}
	for i := 0; i < n; i++ {
		if _, err := fmt.Fscan(in, &g.Cost[i]); err != nil {
			return nil, fmt.Errorf("reading cost of %d: %v", i, err)
		}
		if g.Cost[i] < 0 {
			return nil, fmt.Errorf("vertex %d has negative cost %d", i, g.Cost[i])
		}
	}
	seen := make(map[[2]int]bool)
	for i := 0; i < m; i++ {
		var u, v int
		if _, err := fmt.Fscan(in, &u, &v); err != nil {
			return nil, fmt.Errorf("reading edge %d: %v", i, err)
		}
		if u < 0 || u >= n || v < 0 || v >= n {
			return nil, fmt.Errorf("edge %d out of range: %d %d", i, u, v)
		}
		if seen[[2]int{u, v}] {
			continue
		}
		seen[[2]int{u, v}] = true
		g.Adj[u] = append(g.Adj[u], v)
		g.Radj[v] = append(g.Radj[v], u)
	}
	return g, nil
}

// peel marks vertices that cannot lie on any directed cycle within the alive
// subgraph (iterative removal of in-degree-0 or out-degree-0 vertices).
// A self-loop keeps its vertex unpeeled. Returned slice is freshly allocated.
func (g *Graph) peel(alive []bool) []bool {
	indeg := make([]int, g.N)
	outdeg := make([]int, g.N)
	for v := 0; v < g.N; v++ {
		if !alive[v] {
			continue
		}
		for _, w := range g.Adj[v] {
			if alive[w] {
				outdeg[v]++
				indeg[w]++
			}
		}
	}
	peeled := make([]bool, g.N)
	queue := make([]int, 0, g.N)
	for v := 0; v < g.N; v++ {
		if alive[v] && (indeg[v] == 0 || outdeg[v] == 0) {
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		if peeled[v] {
			continue
		}
		peeled[v] = true
		for _, w := range g.Adj[v] {
			if alive[w] && !peeled[w] {
				indeg[w]--
				if indeg[w] == 0 {
					queue = append(queue, w)
				}
			}
		}
		for _, u := range g.Radj[v] {
			if alive[u] && !peeled[u] {
				outdeg[u]--
				if outdeg[u] == 0 {
					queue = append(queue, u)
				}
			}
		}
	}
	return peeled
}

// findCycle returns one directed cycle in the subgraph induced by
// vertices that are alive and not peeled, or nil if that subgraph is
// acyclic. A self-loop yields a single-vertex cycle.
func (g *Graph) findCycle(alive, peeled []bool) []int {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]int, g.N)
	var stack []int
	var cycle []int
	var dfs func(v int) bool
	dfs = func(v int) bool {
		color[v] = gray
		stack = append(stack, v)
		for _, w := range g.Adj[v] {
			if !alive[w] || peeled[w] {
				continue
			}
			if w == v {
				cycle = []int{v}
				return true
			}
			if color[w] == white {
				if dfs(w) {
					return true
				}
			} else if color[w] == gray {
				idx := -1
				for i := len(stack) - 1; i >= 0; i-- {
					if stack[i] == w {
						idx = i
						break
					}
				}
				cycle = append([]int(nil), stack[idx:]...)
				return true
			}
		}
		stack = stack[:len(stack)-1]
		color[v] = black
		return false
	}
	for v := 0; v < g.N; v++ {
		if alive[v] && !peeled[v] && color[v] == white {
			if dfs(v) {
				return cycle
			}
		}
	}
	return nil
}

// IsAcyclicAfter reports whether the graph becomes acyclic after deleting
// the given set of vertices.
func (g *Graph) IsAcyclicAfter(deleted []bool) bool {
	alive := make([]bool, g.N)
	for v := 0; v < g.N; v++ {
		alive[v] = !deleted[v]
	}
	peeled := g.peel(alive)
	return g.findCycle(alive, peeled) == nil
}

// TopoOrder returns a topological ordering of the graph induced by the
// alive vertices, or ok=false if it still contains a cycle. This is the
// feasibility certificate for a candidate feedback vertex set.
func (g *Graph) TopoOrder(alive []bool) (order []int, ok bool) {
	indeg := make([]int, g.N)
	for v := 0; v < g.N; v++ {
		if !alive[v] {
			continue
		}
		for _, w := range g.Adj[v] {
			if alive[w] {
				indeg[w]++
			}
		}
	}
	queue := make([]int, 0, g.N)
	for v := 0; v < g.N; v++ {
		if alive[v] && indeg[v] == 0 {
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		order = append(order, v)
		for _, w := range g.Adj[v] {
			if alive[w] {
				indeg[w]--
				if indeg[w] == 0 {
					queue = append(queue, w)
				}
			}
		}
	}
	cnt := 0
	for v := 0; v < g.N; v++ {
		if alive[v] {
			cnt++
		}
	}
	if len(order) != cnt {
		return nil, false
	}
	return order, true
}

// VerifyTopoOrder checks that order is a valid topological ordering of the
// graph induced by the alive vertices.
func (g *Graph) VerifyTopoOrder(alive []bool, order []int) bool {
	cnt := 0
	for _, a := range alive {
		if a {
			cnt++
		}
	}
	if len(order) != cnt {
		return false
	}
	pos := make([]int, g.N)
	seen := make([]bool, g.N)
	for i, v := range order {
		if v < 0 || v >= g.N || !alive[v] || seen[v] {
			return false
		}
		seen[v] = true
		pos[v] = i
	}
	for v := 0; v < g.N; v++ {
		if !alive[v] {
			continue
		}
		for _, w := range g.Adj[v] {
			if alive[w] && pos[v] >= pos[w] {
				return false
			}
		}
	}
	return true
}

// sortedCopy returns a sorted copy of s.
func sortedCopy(s []int) []int {
	out := append([]int(nil), s...)
	sort.Ints(out)
	return out
}
