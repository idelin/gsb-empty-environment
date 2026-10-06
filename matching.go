// Package matching computes maximum-cardinality matchings in bipartite
// (assignment) graphs and identifies essential edges: edges that appear in
// every maximum matching. A graph is not required to have a perfect
// matching.
//
// Input edges carry an ID so that duplicate edges connecting the same
// vertex pair remain distinguishable in the input. Matching semantics
// deduplicate by vertex pair: parallel edges between the same pair are
// treated as one edge.
package matching

import "sort"

// Edge is a single input edge between a left and a right vertex.
// ID distinguishes multiple input edges that connect the same vertex pair.
type Edge struct {
	ID    int
	Left  int
	Right int
}

// MaximumMatching returns the IDs of edges forming a maximum-cardinality
// matching, sorted ascending. When a matched pair was given by several
// input edges, the smallest ID is reported. It returns nil for a graph
// with no edges.
//
// It panics if any endpoint is outside [0, nLeft) x [0, nRight).
func MaximumMatching(nLeft, nRight int, edges []Edge) []int {
	g := newGraph(nLeft, nRight, edges)
	matchL, _ := hopcroftKarp(g)
	var ids []int
	for l, r := range matchL {
		if r >= 0 {
			ids = append(ids, g.minID[pair{l, r}])
		}
	}
	sort.Ints(ids)
	return ids
}

type pair [2]int

// graph is the pair-deduplicated view of the input edges.
type graph struct {
	nLeft  int
	nRight int
	adj    [][]int      // left vertex -> sorted unique right vertices
	minID  map[pair]int // vertex pair -> smallest input edge ID
}

func newGraph(nLeft, nRight int, edges []Edge) *graph {
	g := &graph{
		nLeft:  nLeft,
		nRight: nRight,
		adj:    make([][]int, nLeft),
		minID:  make(map[pair]int),
	}
	for _, e := range edges {
		if e.Left < 0 || e.Left >= nLeft || e.Right < 0 || e.Right >= nRight {
			panic("matching: edge endpoint out of range")
		}
		p := pair{e.Left, e.Right}
		if id, ok := g.minID[p]; !ok || e.ID < id {
			g.minID[p] = e.ID
		}
	}
	for p := range g.minID {
		g.adj[p[0]] = append(g.adj[p[0]], p[1])
	}
	for l := range g.adj {
		sort.Ints(g.adj[l])
	}
	return g
}

// hopcroftKarp returns the matching as matchL[l] = r and matchR[r] = l,
// with -1 for unmatched vertices.
func hopcroftKarp(g *graph) (matchL, matchR []int) {
	nL, nR := g.nLeft, g.nRight
	matchL = make([]int, nL)
	matchR = make([]int, nR)
	for i := range matchL {
		matchL[i] = -1
	}
	for i := range matchR {
		matchR[i] = -1
	}
	const inf = int(^uint(0) >> 1)
	dist := make([]int, nL)

	bfs := func() bool {
		queue := make([]int, 0, nL)
		for l := 0; l < nL; l++ {
			if matchL[l] == -1 {
				dist[l] = 0
				queue = append(queue, l)
			} else {
				dist[l] = inf
			}
		}
		found := false
		for len(queue) > 0 {
			l := queue[0]
			queue = queue[1:]
			for _, r := range g.adj[l] {
				m := matchR[r]
				if m == -1 {
					found = true
				} else if dist[m] == inf {
					dist[m] = dist[l] + 1
					queue = append(queue, m)
				}
			}
		}
		return found
	}

	var dfs func(l int) bool
	dfs = func(l int) bool {
		for _, r := range g.adj[l] {
			m := matchR[r]
			if m == -1 || (dist[m] == dist[l]+1 && dfs(m)) {
				matchL[l] = r
				matchR[r] = l
				return true
			}
		}
		dist[l] = inf
		return false
	}

	for bfs() {
		for l := 0; l < nL; l++ {
			if matchL[l] == -1 {
				dfs(l)
			}
		}
	}
	return matchL, matchR
}
