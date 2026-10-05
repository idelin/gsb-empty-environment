// Package coreindex maintains core numbers (k-core decomposition) of an
// undirected simple graph under edge insertions and deletions.
//
// Node IDs are stable and never renumbered. Isolated nodes are kept
// forever once created. Duplicate edge insertions are idempotent,
// deletions of missing edges are no-ops, and self-loops are rejected.
package coreindex

import (
	"errors"
	"sort"
)

// ErrSelfLoop is returned when an operation references a self-loop edge.
var ErrSelfLoop = errors.New("coreindex: self-loop edges are not allowed")

// Index is an undirected simple graph with incrementally maintained
// core numbers.
type Index struct {
	adj  map[int64]map[int64]struct{}
	core map[int64]int
}

// New returns an empty Index.
func New() *Index {
	return &Index{
		adj:  make(map[int64]map[int64]struct{}),
		core: make(map[int64]int),
	}
}

// EnsureNode adds an isolated node if it does not exist yet.
func (ix *Index) EnsureNode(id int64) {
	if _, ok := ix.adj[id]; !ok {
		ix.adj[id] = make(map[int64]struct{})
		ix.core[id] = 0
	}
}

// HasNode reports whether id is present (isolated nodes included).
func (ix *Index) HasNode(id int64) bool {
	_, ok := ix.adj[id]
	return ok
}

// HasEdge reports whether edge (u,v) exists.
func (ix *Index) HasEdge(u, v int64) bool {
	_, ok := ix.adj[u][v]
	return ok
}

// Degree returns the current degree of id (0 for unknown nodes).
func (ix *Index) Degree(id int64) int {
	return len(ix.adj[id])
}

// Core returns the maintained core number of id.
func (ix *Index) Core(id int64) (int, bool) {
	c, ok := ix.core[id]
	return c, ok
}

// Cores returns a copy of all maintained core numbers.
func (ix *Index) Cores() map[int64]int {
	out := make(map[int64]int, len(ix.core))
	for id, c := range ix.core {
		out[id] = c
	}
	return out
}

// Nodes returns all node IDs in ascending order (isolated nodes included).
func (ix *Index) Nodes() []int64 {
	ids := make([]int64, 0, len(ix.adj))
	for id := range ix.adj {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Edges returns all edges as [u,v] pairs with u < v, sorted.
func (ix *Index) Edges() [][2]int64 {
	edges := make([][2]int64, 0)
	for u, nbrs := range ix.adj {
		for v := range nbrs {
			if u < v {
				edges = append(edges, [2]int64{u, v})
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i][0] != edges[j][0] {
			return edges[i][0] < edges[j][0]
		}
		return edges[i][1] < edges[j][1]
	})
	return edges
}

// Neighbors returns the sorted neighbor list of id.
func (ix *Index) Neighbors(id int64) []int64 {
	out := make([]int64, 0, len(ix.adj[id]))
	for w := range ix.adj[id] {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// addEdgeRaw inserts an edge without touching core numbers.
// It reports whether the graph actually changed.
func (ix *Index) addEdgeRaw(u, v int64) bool {
	ix.EnsureNode(u)
	ix.EnsureNode(v)
	if ix.HasEdge(u, v) {
		return false
	}
	ix.adj[u][v] = struct{}{}
	ix.adj[v][u] = struct{}{}
	return true
}

// removeEdgeRaw deletes an edge without touching core numbers.
// It reports whether the graph actually changed.
func (ix *Index) removeEdgeRaw(u, v int64) bool {
	if !ix.HasEdge(u, v) {
		return false
	}
	delete(ix.adj[u], v)
	delete(ix.adj[v], u)
	return true
}

// snapshot deep-copies the mutable state for rollback.
func (ix *Index) snapshot() *Index {
	s := New()
	for id, nbrs := range ix.adj {
		cp := make(map[int64]struct{}, len(nbrs))
		for w := range nbrs {
			cp[w] = struct{}{}
		}
		s.adj[id] = cp
	}
	for id, c := range ix.core {
		s.core[id] = c
	}
	return s
}

func (ix *Index) restore(s *Index) {
	ix.adj = s.adj
	ix.core = s.core
}
