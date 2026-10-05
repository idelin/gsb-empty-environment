// Package edgecolor schedules the edges of a bipartite multigraph into
// rounds (matchings) so that edges sharing an endpoint never run in the
// same round. The number of rounds always equals the maximum degree
// (Konig's line-coloring theorem). Parallel edges are supported.
//
// Coloring is computed with the classical augmenting color-swap method:
// each edge either takes a color free at both endpoints, or frees one by
// flipping an alternating two-color (Kempe) path. Colors are then
// normalized to 0..k-1 by first appearance in ascending edge-ID order.
//
// The scheduler keeps no coloring state between calls: Rounds recomputes
// from the live edge set, so deleting and re-adding edges can never leave
// stale color indices behind.
package edgecolor

import (
	"fmt"
	"sort"
)

// Edge is one edge of the bipartite multigraph. Left and Right are vertex
// identifiers in two independent namespaces (the two parts).
type Edge struct {
	ID    int
	Left  int
	Right int
}

// Scheduler stores the graph and produces round assignments on demand.
type Scheduler struct {
	edges  map[int]Edge
	adjL   map[int]map[int]struct{} // left vertex  -> incident edge IDs
	adjR   map[int]map[int]struct{} // right vertex -> incident edge IDs
	nextID int
}

// New returns an empty scheduler.
func New() *Scheduler {
	return &Scheduler{
		edges: make(map[int]Edge),
		adjL:  make(map[int]map[int]struct{}),
		adjR:  make(map[int]map[int]struct{}),
	}
}

// AddEdge inserts an edge between left and right and returns its ID.
// IDs are never reused, even after RemoveEdge.
func (s *Scheduler) AddEdge(left, right int) int {
	id := s.nextID
	s.nextID++
	s.edges[id] = Edge{ID: id, Left: left, Right: right}
	addAdj(s.adjL, left, id)
	addAdj(s.adjR, right, id)
	return id
}

// RemoveEdge deletes the edge with the given ID. It reports whether the
// edge existed.
func (s *Scheduler) RemoveEdge(id int) bool {
	e, ok := s.edges[id]
	if !ok {
		return false
	}
	delete(s.edges, id)
	delAdj(s.adjL, e.Left, id)
	delAdj(s.adjR, e.Right, id)
	return true
}

func addAdj(adj map[int]map[int]struct{}, v, id int) {
	set, ok := adj[v]
	if !ok {
		set = make(map[int]struct{})
		adj[v] = set
	}
	set[id] = struct{}{}
}

func delAdj(adj map[int]map[int]struct{}, v, id int) {
	set, ok := adj[v]
	if !ok {
		return
	}
	delete(set, id)
	if len(set) == 0 {
		delete(adj, v)
	}
}

// Edges returns the live edges sorted by ID.
func (s *Scheduler) Edges() []Edge {
	ids := s.edgeIDs()
	out := make([]Edge, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.edges[id])
	}
	return out
}

func (s *Scheduler) edgeIDs() []int {
	ids := make([]int, 0, len(s.edges))
	for id := range s.edges {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// MaxDegree returns the maximum number of live edges incident to any
// vertex, i.e. the minimum possible number of rounds.
func (s *Scheduler) MaxDegree() int {
	d := 0
	for _, set := range s.adjL {
		if len(set) > d {
			d = len(set)
		}
	}
	for _, set := range s.adjR {
		if len(set) > d {
			d = len(set)
		}
	}
	return d
}

// Rounds assigns every live edge to a round and returns rounds[c] as the
// matching (list of edge IDs, ascending) executed in round c. The number
// of rounds equals MaxDegree. Round indices are normalized: scanning edge
// IDs in ascending order, the first edge uses round 0, the next
// never-seen round is 1, and so on.
func (s *Scheduler) Rounds() [][]int {
	delta := s.MaxDegree()
	if delta == 0 {
		return nil
	}
	ids := s.edgeIDs()
	color := s.color(ids, delta)

	// Normalize colors by first appearance in ascending edge-ID order.
	remap := make(map[int]int, delta)
	for _, id := range ids {
		c := color[id]
		if _, ok := remap[c]; !ok {
			remap[c] = len(remap)
		}
	}
	rounds := make([][]int, len(remap))
	for _, id := range ids {
		c := remap[color[id]]
		rounds[c] = append(rounds[c], id)
	}
	return rounds
}

// color assigns each edge a color in [0, delta) so that incident edges
// differ, using augmenting color swaps. ids must be sorted ascending.
func (s *Scheduler) color(ids []int, delta int) map[int]int {
	colorOf := make(map[int]int, len(ids))
	// Per-vertex maps: color -> incident edge ID carrying that color.
	lCol := make(map[int]map[int]int)
	rCol := make(map[int]map[int]int)

	get := func(m map[int]map[int]int, v int) map[int]int {
		mm, ok := m[v]
		if !ok {
			mm = make(map[int]int)
			m[v] = mm
		}
		return mm
	}
	freeColor := func(m map[int]int) int {
		for c := 0; c < delta; c++ {
			if _, taken := m[c]; !taken {
				return c
			}
		}
		return -1 // unreachable: degree <= delta and < delta edges colored so far
	}

	// swapPath flips colors a<->b along the maximal alternating path that
	// starts at left vertex `start` with a b-colored edge. a is free at
	// start and b is free at the target right vertex, so the path can
	// neither cycle back to start (no a-edge there) nor pass through the
	// target (no b-edge there); after the flip, b is free at start.
	swapPath := func(start, a, b int) {
		// Collect the path with reads only; flipping while walking would
		// make the just-flipped edge visible under its new color and
		// corrupt the walk.
		var path []int
		c := b
		v := start
		onLeft := true
		for {
			var m map[int]int
			if onLeft {
				m = lCol[v]
			} else {
				m = rCol[v]
			}
			eid, ok := m[c]
			if !ok {
				break
			}
			path = append(path, eid)
			e := s.edges[eid]
			if onLeft {
				v = e.Right
			} else {
				v = e.Left
			}
			onLeft = !onLeft
			c = a + b - c
		}
		// Flip in two phases: clear every old color slot first, then
		// install the new colors. Interleaving deletes and inserts can
		// clobber a slot shared by two path edges at the same vertex.
		for _, eid := range path {
			e := s.edges[eid]
			old := colorOf[eid]
			delete(lCol[e.Left], old)
			delete(rCol[e.Right], old)
		}
		for _, eid := range path {
			e := s.edges[eid]
			nc := a + b - colorOf[eid]
			colorOf[eid] = nc
			lCol[e.Left][nc] = eid
			rCol[e.Right][nc] = eid
		}
	}

	for _, id := range ids {
		e := s.edges[id]
		a := freeColor(get(lCol, e.Left))
		b := freeColor(get(rCol, e.Right))
		c := a
		if a != b {
			swapPath(e.Left, a, b)
			c = b
		}
		colorOf[id] = c
		lCol[e.Left][c] = id
		rCol[e.Right][c] = id
	}
	return colorOf
}

// Verify checks that rounds form a valid schedule for the current graph:
// the round count equals MaxDegree, every live edge appears exactly once,
// no unknown edges appear, and no round contains two edges sharing an
// endpoint.
func (s *Scheduler) Verify(rounds [][]int) error {
	if delta := s.MaxDegree(); len(rounds) != delta {
		return fmt.Errorf("round count %d != max degree %d", len(rounds), delta)
	}
	seen := make(map[int]bool, len(s.edges))
	for r, round := range rounds {
		usedL := make(map[int]bool)
		usedR := make(map[int]bool)
		for _, id := range round {
			e, ok := s.edges[id]
			if !ok {
				return fmt.Errorf("round %d: unknown edge ID %d", r, id)
			}
			if seen[id] {
				return fmt.Errorf("round %d: edge %d scheduled more than once", r, id)
			}
			seen[id] = true
			if usedL[e.Left] {
				return fmt.Errorf("round %d: left vertex %d used by two edges", r, e.Left)
			}
			if usedR[e.Right] {
				return fmt.Errorf("round %d: right vertex %d used by two edges", r, e.Right)
			}
			usedL[e.Left] = true
			usedR[e.Right] = true
		}
	}
	if len(seen) != len(s.edges) {
		return fmt.Errorf("scheduled %d of %d live edges", len(seen), len(s.edges))
	}
	return nil
}
