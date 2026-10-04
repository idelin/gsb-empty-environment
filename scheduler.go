// Package scheduler assigns the edges of a bipartite multigraph to rounds
// (a proper edge coloring) so that two edges sharing an endpoint never run
// in the same round. The number of rounds always equals the maximum degree
// of the graph, which is optimal by Kőnig's line-coloring theorem.
//
// The coloring is computed with the classical augmenting-recoloring method:
// every edge is inserted one at a time, and when its two endpoints have no
// common free color, an alternating two-color path is flipped to free one.
package scheduler

import "sort"

// Edge is a single undirected edge between a left vertex U and a right
// vertex V. Left and right vertices live in independent namespaces: U=1
// and V=1 are different vertices. Parallel edges (same U and V) are
// allowed and receive distinct IDs.
type Edge struct {
	ID int
	U  int // left endpoint
	V  int // right endpoint
}

// Scheduler stores a bipartite multigraph and produces round assignments
// on demand. Use New to create one.
type Scheduler struct {
	edges  map[int]Edge         // id -> edge
	adjU   map[int]map[int]bool // left vertex -> set of edge IDs
	adjV   map[int]map[int]bool // right vertex -> set of edge IDs
	nextID int
}

// New returns an empty Scheduler.
func New() *Scheduler {
	return &Scheduler{
		edges: make(map[int]Edge),
		adjU:  make(map[int]map[int]bool),
		adjV:  make(map[int]map[int]bool),
	}
}

// AddEdge inserts an edge between left vertex u and right vertex v and
// returns its fresh edge ID. IDs are never reused, even after DeleteEdge,
// so a re-added edge can never be confused with a stale one.
func (s *Scheduler) AddEdge(u, v int) int {
	id := s.nextID
	s.nextID++
	s.edges[id] = Edge{ID: id, U: u, V: v}
	if s.adjU[u] == nil {
		s.adjU[u] = make(map[int]bool)
	}
	s.adjU[u][id] = true
	if s.adjV[v] == nil {
		s.adjV[v] = make(map[int]bool)
	}
	s.adjV[v][id] = true
	return id
}

// DeleteEdge removes the edge with the given ID. It reports whether the
// edge existed. All adjacency bookkeeping for the edge is dropped, so
// nothing about it (including any past color) can leak into later
// schedules.
func (s *Scheduler) DeleteEdge(id int) bool {
	e, ok := s.edges[id]
	if !ok {
		return false
	}
	delete(s.edges, id)
	delete(s.adjU[e.U], id)
	if len(s.adjU[e.U]) == 0 {
		delete(s.adjU, e.U)
	}
	delete(s.adjV[e.V], id)
	if len(s.adjV[e.V]) == 0 {
		delete(s.adjV, e.V)
	}
	return true
}

// Edges returns the live edges sorted by ID.
func (s *Scheduler) Edges() []Edge {
	ids := s.sortedIDs()
	out := make([]Edge, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.edges[id])
	}
	return out
}

// MaxDegree returns the maximum number of live edges incident to any
// single vertex, on either side. It is the optimal number of rounds.
func (s *Scheduler) MaxDegree() int {
	max := 0
	for _, set := range s.adjU {
		if len(set) > max {
			max = len(set)
		}
	}
	for _, set := range s.adjV {
		if len(set) > max {
			max = len(set)
		}
	}
	return max
}

func (s *Scheduler) sortedIDs() []int {
	ids := make([]int, 0, len(s.edges))
	for id := range s.edges {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}
