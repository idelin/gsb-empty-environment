package hnsw

import "sort"

// reconnectLayer0 restores a single alive connected component on layer 0.
// Nodes outside the entry-point component are attached to their closest
// alive node in the main component; newly added edges respect the M0 bound
// and run the symmetric shrink fixpoint when an endpoint overflows.
func (g *Graph[T]) reconnectLayer0(repairedSet map[int]struct{}) {
	if g.entry == -1 {
		return
	}
	for {
		main := g.aliveComponent(g.entry, 0)
		outside := make([]int, 0)
		for id, ns := range g.nodes {
			if ns.deleted || ns.level < 0 {
				continue
			}
			if _, in := main[id]; !in {
				outside = append(outside, id)
			}
		}
		if len(outside) == 0 {
			return
		}
		sort.Ints(outside)

		queue := []int{}
		queued := map[int]struct{}{}
		for _, id := range outside {
			target := g.nodes[id]
			bestID := -1
			bestDist := 0.0
			for anchorID := range main {
				d := g.dist(target.vec, g.nodes[anchorID].vec)
				if bestID == -1 || d < bestDist || (d == bestDist && anchorID < bestID) {
					bestID, bestDist = anchorID, d
				}
			}
			if bestID != -1 {
				g.addLink(0, id, bestID, &queue, queued)
				repairedSet[id] = struct{}{}
			}
		}
		if len(queue) > 0 {
			g.repairLayerDegree(0, queue)
		}
	}
}

// aliveComponent returns the set of alive nodes reachable from start on
// layer 0 through alive edges.
func (g *Graph[T]) aliveComponent(start, layer int) map[int]struct{} {
	seen := map[int]struct{}{start: {}}
	queue := []int{start}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		ns := g.nodes[id]
		for _, neighborID := range ns.conns[0] {
			neighbor := g.nodes[neighborID]
			if neighbor.deleted {
				continue
			}
			if _, ok := seen[neighborID]; ok {
				continue
			}
			seen[neighborID] = struct{}{}
			queue = append(queue, neighborID)
		}
	}
	return seen
}

// Layer0Connected reports whether every alive node is reachable from the
// entry point through alive layer-0 edges. A graph with zero or one alive
// node is connected by definition.
func (g *Graph[T]) Layer0Connected() bool {
	if g.entry == -1 {
		return true
	}
	seen := map[int]struct{}{g.entry: {}}
	queue := []int{g.entry}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, neighborID := range g.nodes[id].conns[0] {
			neighbor := g.nodes[neighborID]
			if neighbor.deleted {
				continue
			}
			if _, ok := seen[neighborID]; ok {
				continue
			}
			seen[neighborID] = struct{}{}
			queue = append(queue, neighborID)
		}
	}
	for _, ns := range g.nodes {
		if ns.deleted {
			continue
		}
		if _, ok := seen[ns.id]; !ok {
			return false
		}
	}
	return true
}

// AliveIDs returns the sorted IDs of all non-deleted nodes.
func (g *Graph[T]) AliveIDs() []int {
	out := make([]int, 0, len(g.nodes))
	for id, ns := range g.nodes {
		if !ns.deleted {
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out
}
