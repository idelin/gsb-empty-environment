// Package coreidx maintains core numbers (k-core decomposition index) of an
// undirected simple graph under edge insertions and deletions.
//
// Usage:
//  1. Construct the graph with AddNode / AddEdge / DeleteEdge.
//  2. Call Build once to compute the initial core numbers. Build also
//     returns the deterministic peeling order and the remaining degree of
//     every node at the moment it was peeled (for verification).
//  3. Apply subsequent changes with ApplyBatch. Batches are atomic: if any
//     operation is invalid (e.g. a self-loop) the whole batch is rolled
//     back. ApplyBatch maintains core numbers incrementally (it never
//     recomputes a full-graph decomposition) and reports exactly the set of
//     nodes whose core number changed.
//
// Invariants: node IDs are stable int64 values chosen by the caller;
// adding an existing edge is idempotent; deleting a missing edge is a no-op;
// self-loops are rejected; isolated nodes are never garbage collected.
package coreidx

import (
	"errors"
	"fmt"
	"sort"
)

// ErrSelfLoop is returned when an operation would create a self-loop.
var ErrSelfLoop = errors.New("coreidx: self-loops are not allowed")

// ErrNotBuilt is returned by ApplyBatch when Build has not been called yet.
var ErrNotBuilt = errors.New("coreidx: Build must be called before ApplyBatch")

// Graph is an undirected simple graph with an incremental core-number index.
type Graph struct {
	adj   map[int64]map[int64]struct{}
	core  map[int64]int
	built bool
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{
		adj:  make(map[int64]map[int64]struct{}),
		core: make(map[int64]int),
	}
}

// AddNode inserts an isolated node. It is idempotent: adding an existing
// node keeps its edges and core number.
func (g *Graph) AddNode(id int64) {
	if _, ok := g.adj[id]; !ok {
		g.adj[id] = make(map[int64]struct{})
	}
}

// HasNode reports whether id is present (isolated nodes count).
func (g *Graph) HasNode(id int64) bool {
	_, ok := g.adj[id]
	return ok
}

// HasEdge reports whether the edge (u, v) exists.
func (g *Graph) HasEdge(u, v int64) bool {
	nbrs, ok := g.adj[u]
	if !ok {
		return false
	}
	_, ok = nbrs[v]
	return ok
}

// AddEdge inserts the edge (u, v), creating endpoint nodes as needed.
// Adding an existing edge is idempotent. A self-loop is rejected with
// ErrSelfLoop and leaves the graph unchanged.
//
// AddEdge is a construction primitive: it does not maintain core numbers.
// Call Build afterwards, or use ApplyBatch for incremental maintenance.
func (g *Graph) AddEdge(u, v int64) error {
	if u == v {
		return fmt.Errorf("%w: (%d, %d)", ErrSelfLoop, u, v)
	}
	g.addEdgeRaw(u, v)
	return nil
}

// DeleteEdge removes the edge (u, v). Deleting a missing edge is a no-op.
// The endpoint nodes are kept even if they become isolated.
//
// Like AddEdge, this is a construction primitive; use ApplyBatch for
// incremental core maintenance.
func (g *Graph) DeleteEdge(u, v int64) {
	g.deleteEdgeRaw(u, v)
}

func (g *Graph) addEdgeRaw(u, v int64) {
	g.AddNode(u)
	g.AddNode(v)
	g.adj[u][v] = struct{}{}
	g.adj[v][u] = struct{}{}
}

func (g *Graph) deleteEdgeRaw(u, v int64) {
	if !g.HasEdge(u, v) {
		return
	}
	delete(g.adj[u], v)
	delete(g.adj[v], u)
}

// Nodes returns all node IDs (including isolated ones) in ascending order.
func (g *Graph) Nodes() []int64 {
	ids := make([]int64, 0, len(g.adj))
	for id := range g.adj {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Edge is an unordered pair of node IDs with U < V.
type Edge struct{ U, V int64 }

// Edges returns all edges, sorted by (U, V).
func (g *Graph) Edges() []Edge {
	var edges []Edge
	for u, nbrs := range g.adj {
		for v := range nbrs {
			if u < v {
				edges = append(edges, Edge{u, v})
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].U != edges[j].U {
			return edges[i].U < edges[j].U
		}
		return edges[i].V < edges[j].V
	})
	return edges
}

// Core returns the current core number of id. The second return value is
// false if the node does not exist or Build has not been called yet.
func (g *Graph) Core(id int64) (int, bool) {
	if !g.built {
		return 0, false
	}
	c, ok := g.core[id]
	return c, ok
}

// Cores returns a copy of the current core-number map.
func (g *Graph) Cores() map[int64]int {
	out := make(map[int64]int, len(g.core))
	for id, c := range g.core {
		out[id] = c
	}
	return out
}

// Degree returns the current number of neighbors of id.
func (g *Graph) Degree(id int64) int {
	return len(g.adj[id])
}
