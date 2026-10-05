package main

import (
	"fmt"
	"sort"
)

// VertexInput is one vertex of the JSON input. Cost defaults to 1 when
// omitted and must be non-negative.
type VertexInput struct {
	ID   string `json:"id"`
	Cost *int64 `json:"cost,omitempty"`
}

// Input is the JSON document describing the directed graph:
//
//	{"vertices":[{"id":"a","cost":2},...],"edges":[["a","b"],...]}
type Input struct {
	Vertices []VertexInput `json:"vertices"`
	Edges    [][2]string   `json:"edges"`
}

// Graph is a directed graph with non-negative vertex deletion costs.
type Graph struct {
	IDs   []string
	Cost  []int64
	Adj   [][]int // outgoing edges
	Radj  [][]int // incoming edges
	Self  []bool  // Self[v] reports a v -> v loop
	Index map[string]int
}

// BuildGraph validates the input and builds the internal representation.
func BuildGraph(in *Input) (*Graph, error) {
	n := len(in.Vertices)
	g := &Graph{
		IDs:   make([]string, n),
		Cost:  make([]int64, n),
		Adj:   make([][]int, n),
		Radj:  make([][]int, n),
		Self:  make([]bool, n),
		Index: make(map[string]int, n),
	}
	for i, v := range in.Vertices {
		if v.ID == "" {
			return nil, fmt.Errorf("vertex %d: empty id", i)
		}
		if _, dup := g.Index[v.ID]; dup {
			return nil, fmt.Errorf("duplicate vertex id %q", v.ID)
		}
		cost := int64(1)
		if v.Cost != nil {
			if *v.Cost < 0 {
				return nil, fmt.Errorf("vertex %q: negative cost %d", v.ID, *v.Cost)
			}
			cost = *v.Cost
		}
		g.IDs[i] = v.ID
		g.Cost[i] = cost
		g.Index[v.ID] = i
	}
	seen := make(map[[2]int]bool)
	for _, e := range in.Edges {
		u, ok := g.Index[e[0]]
		if !ok {
			return nil, fmt.Errorf("edge from unknown vertex %q", e[0])
		}
		w, ok := g.Index[e[1]]
		if !ok {
			return nil, fmt.Errorf("edge to unknown vertex %q", e[1])
		}
		key := [2]int{u, w}
		if seen[key] {
			continue
		}
		seen[key] = true
		g.Adj[u] = append(g.Adj[u], w)
		g.Radj[w] = append(g.Radj[w], u)
		if u == w {
			g.Self[u] = true
		}
	}
	return g, nil
}

// SCCs returns the strongly connected components of the subgraph induced by
// the alive vertices (Kosaraju, iterative).
func (g *Graph) SCCs(alive []bool) [][]int {
	n := len(g.IDs)
	visited := make([]bool, n)
	order := make([]int, 0, n)
	for s := 0; s < n; s++ {
		if !alive[s] || visited[s] {
			continue
		}
		type frame struct{ v, i int }
		stack := []frame{{v: s}}
		visited[s] = true
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			if f.i < len(g.Adj[f.v]) {
				w := g.Adj[f.v][f.i]
				f.i++
				if alive[w] && !visited[w] {
					visited[w] = true
					stack = append(stack, frame{v: w})
				}
			} else {
				order = append(order, f.v)
				stack = stack[:len(stack)-1]
			}
		}
	}
	assigned := make([]bool, n)
	var comps [][]int
	for i := len(order) - 1; i >= 0; i-- {
		s := order[i]
		if assigned[s] {
			continue
		}
		var members []int
		stack := []int{s}
		assigned[s] = true
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			members = append(members, v)
			for _, w := range g.Radj[v] {
				if alive[w] && !assigned[w] {
					assigned[w] = true
					stack = append(stack, w)
				}
			}
		}
		comps = append(comps, members)
	}
	return comps
}

// anyCycle returns one directed cycle in the subgraph induced by active, or
// nil if that subgraph is acyclic. Self-loops are reported as 1-cycles.
func (g *Graph) anyCycle(active []bool) []int {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	n := len(g.IDs)
	color := make([]int, n)
	parent := make([]int, n)
	for s := 0; s < n; s++ {
		if !active[s] || color[s] != white {
			continue
		}
		type frame struct{ v, i int }
		stack := []frame{{v: s}}
		color[s] = gray
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			if f.i < len(g.Adj[f.v]) {
				w := g.Adj[f.v][f.i]
				f.i++
				if !active[w] {
					continue
				}
				switch color[w] {
				case gray:
					cyc := []int{w}
					for x := f.v; x != w; x = parent[x] {
						cyc = append(cyc, x)
					}
					for i, j := 0, len(cyc)-1; i < j; i, j = i+1, j-1 {
						cyc[i], cyc[j] = cyc[j], cyc[i]
					}
					return cyc
				case white:
					color[w] = gray
					parent[w] = f.v
					stack = append(stack, frame{v: w})
				}
			} else {
				color[f.v] = black
				stack = stack[:len(stack)-1]
			}
		}
	}
	return nil
}

// shortestCycle returns a shortest directed cycle in the subgraph induced by
// active (BFS from every vertex), or nil if acyclic.
func (g *Graph) shortestCycle(active []bool) []int {
	n := len(g.IDs)
	var best []int
	dist := make([]int, n)
	prev := make([]int, n)
	for s := 0; s < n; s++ {
		if !active[s] {
			continue
		}
		if best != nil && len(best) == 2 {
			break // cannot beat a 2-cycle once self-loops are excluded
		}
		for i := range dist {
			dist[i] = -1
		}
		dist[s] = 0
		queue := []int{s}
		for len(queue) > 0 {
			v := queue[0]
			queue = queue[1:]
			if best != nil && dist[v]+1 >= len(best) {
				continue
			}
			for _, w := range g.Adj[v] {
				if !active[w] {
					continue
				}
				if w == s {
					if best == nil || dist[v]+1 < len(best) {
						cyc := []int{}
						for x := v; x != s; x = prev[x] {
							cyc = append(cyc, x)
						}
						cyc = append(cyc, s)
						for i, j := 0, len(cyc)-1; i < j; i, j = i+1, j-1 {
							cyc[i], cyc[j] = cyc[j], cyc[i]
						}
						best = cyc
					}
					continue
				}
				if dist[w] == -1 {
					dist[w] = dist[v] + 1
					prev[w] = v
					queue = append(queue, w)
				}
			}
		}
	}
	return best
}

// cyclePackingBound greedily collects vertex-disjoint cycles in the subgraph
// induced by active. Any feedback vertex set must delete at least one vertex
// per cycle, so the summed per-cycle minimum cost (and the cycle count) are
// valid lower bounds on the additional deletion cost (and count).
func (g *Graph) cyclePackingBound(active []bool) (cost int64, count int) {
	avail := make([]bool, len(active))
	copy(avail, active)
	for {
		cyc := g.anyCycle(avail)
		if cyc == nil {
			return cost, count
		}
		minCost := int64(-1)
		for _, v := range cyc {
			if minCost == -1 || g.Cost[v] < minCost {
				minCost = g.Cost[v]
			}
			avail[v] = false
		}
		cost += minCost
		count++
	}
}

// TopoOrder returns a deterministic topological order of the subgraph that
// remains after deleting the given vertices. ok is false when the remaining
// graph still contains a cycle.
func (g *Graph) TopoOrder(deleted []bool) (order []string, ok bool) {
	n := len(g.IDs)
	indeg := make([]int, n)
	remaining := 0
	for v := 0; v < n; v++ {
		if deleted[v] {
			continue
		}
		remaining++
		for _, w := range g.Adj[v] {
			if !deleted[w] {
				indeg[w]++
			}
		}
	}
	var ready []int
	for v := 0; v < n; v++ {
		if !deleted[v] && indeg[v] == 0 {
			ready = append(ready, v)
		}
	}
	for len(ready) > 0 {
		mi := 0
		for i, v := range ready {
			if g.IDs[v] < g.IDs[ready[mi]] {
				mi = i
			}
		}
		v := ready[mi]
		ready = append(ready[:mi], ready[mi+1:]...)
		order = append(order, g.IDs[v])
		for _, w := range g.Adj[v] {
			if deleted[w] {
				continue
			}
			indeg[w]--
			if indeg[w] == 0 {
				ready = append(ready, w)
			}
		}
	}
	if len(order) != remaining {
		return nil, false
	}
	if order == nil {
		order = []string{}
	}
	return order, true
}

// AcyclicExcluding reports whether deleting the given vertices leaves an
// acyclic graph.
func (g *Graph) AcyclicExcluding(deleted []bool) bool {
	_, ok := g.TopoOrder(deleted)
	return ok
}

// VerifyCertificate checks that deletedIDs is a feedback vertex set and that
// order is a valid topological order of the remaining graph.
func VerifyCertificate(g *Graph, deletedIDs, order []string) error {
	deleted := make([]bool, len(g.IDs))
	for _, id := range deletedIDs {
		i, ok := g.Index[id]
		if !ok {
			return fmt.Errorf("unknown deleted vertex %q", id)
		}
		if deleted[i] {
			return fmt.Errorf("duplicate deleted vertex %q", id)
		}
		deleted[i] = true
	}
	pos := make(map[string]int, len(order))
	for i, id := range order {
		j, ok := g.Index[id]
		if !ok {
			return fmt.Errorf("unknown vertex %q in topological order", id)
		}
		if deleted[j] {
			return fmt.Errorf("deleted vertex %q appears in topological order", id)
		}
		if _, dup := pos[id]; dup {
			return fmt.Errorf("duplicate vertex %q in topological order", id)
		}
		pos[id] = i
	}
	for i, id := range g.IDs {
		if !deleted[i] {
			if _, ok := pos[id]; !ok {
				return fmt.Errorf("vertex %q missing from topological order", id)
			}
		}
	}
	for u := range g.IDs {
		if deleted[u] {
			continue
		}
		for _, v := range g.Adj[u] {
			if deleted[v] {
				continue
			}
			if pos[g.IDs[u]] >= pos[g.IDs[v]] {
				return fmt.Errorf("edge %q -> %q violates topological order", g.IDs[u], g.IDs[v])
			}
		}
	}
	return nil
}

// sortedIDs returns the sorted IDs of the deleted vertices.
func sortedIDs(g *Graph, deleted []bool) []string {
	ids := []string{}
	for i, d := range deleted {
		if d {
			ids = append(ids, g.IDs[i])
		}
	}
	sort.Strings(ids)
	return ids
}
