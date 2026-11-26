package hnsw

import (
	"math"
	"sort"
)

// scoredCandidate pairs a candidate node with its distance to the target.
type scoredCandidate struct {
	id int
	d  float64
}

// selectNeighbors implements HNSW paper Algorithm 4.
//
// Candidates are scanned in ascending distance to target (node ID breaks
// ties deterministically). A candidate is selected only when its distance to
// the target is strictly smaller than its distance to every already selected
// neighbor, i.e. the candidate is closer to the target than to any chosen
// node. When keepPruned is enabled, skipped candidates backfill remaining
// degree slots in ascending distance order until the bound is reached.
//
// The target itself and tombstoned nodes are never selected.
func (g *Graph[T]) selectNeighbors(target int, candidates map[int]struct{}, layer, maxConn int) []int {
	entries := make([]scoredCandidate, 0, len(candidates))
	targetNode := g.nodes[target]
	for id := range candidates {
		if id == target {
			continue
		}
		ns, ok := g.nodes[id]
		if !ok || ns.deleted {
			continue
		}
		entries = append(entries, scoredCandidate{id: id, d: g.dist(targetNode.vec, ns.vec)})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].d != entries[j].d {
			return entries[i].d < entries[j].d
		}
		return entries[i].id < entries[j].id
	})

	selected := make([]int, 0, maxConn)
	pruned := make([]int, 0)
	for _, cand := range entries {
		if len(selected) >= maxConn {
			// Every remaining candidate is pruned; keep them ordered so a
			// caller with keepPruned can backfill later rounds if needed.
			pruned = append(pruned, cand.id)
			continue
		}
		closerToTargetThanAny := true
		for _, chosenID := range selected {
			if g.dist(g.nodes[cand.id].vec, g.nodes[chosenID].vec) <= cand.d {
				closerToTargetThanAny = false
				break
			}
		}
		if closerToTargetThanAny {
			selected = append(selected, cand.id)
		} else {
			pruned = append(pruned, cand.id)
		}
	}

	if g.keepPruned && len(selected) < maxConn {
		for _, id := range pruned {
			if len(selected) >= maxConn {
				break
			}
			selected = append(selected, id)
		}
	}
	sort.Ints(selected)
	return selected
}

// addLink inserts a symmetric edge on a layer and enqueues any endpoint that
// exceeds the layer degree bound for heuristic shrinking.
func (g *Graph[T]) addLink(layer, a, b int, queue *[]int, queued map[int]struct{}) {
	an, bn := g.nodes[a], g.nodes[b]
	an.conns[layer] = insertID(an.conns[layer], b)
	bn.conns[layer] = insertID(bn.conns[layer], a)
	bound := g.MaxDegree(layer)
	if len(an.conns[layer]) > bound {
		g.markShrink(a, queue, queued)
	}
	if len(bn.conns[layer]) > bound {
		g.markShrink(b, queue, queued)
	}
}

// removeLink deletes both directions of an edge on a layer.
func (g *Graph[T]) removeLink(layer, a, b int) {
	an, bn := g.nodes[a], g.nodes[b]
	an.conns[layer] = removeID(an.conns[layer], b)
	bn.conns[layer] = removeID(bn.conns[layer], a)
}

// markShrink adds id to the deterministic shrink work queue once.
func (g *Graph[T]) markShrink(id int, queue *[]int, queued map[int]struct{}) {
	if _, ok := queued[id]; !ok {
		queued[id] = struct{}{}
		*queue = append(*queue, id)
	}
}

// repairLayerDegree runs the symmetric fixpoint for one layer: every node
// above the degree bound is re-selected with Algorithm 4 over its current
// alive neighborhood. Removing an edge removes both directions, which may
// push the opposite endpoint over bound (its bound never rises), so the
// queue drains only once every node satisfies the layer limit.
func (g *Graph[T]) repairLayerDegree(layer int, queue []int) {
	queued := map[int]struct{}{}
	for _, id := range queue {
		queued[id] = struct{}{}
	}
	bound := g.MaxDegree(layer)

	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		delete(queued, id)

		ns, ok := g.nodes[id]
		if !ok || ns.deleted || layer > ns.level {
			continue
		}
		if len(ns.conns[layer]) <= bound {
			continue
		}

		wanted := g.selectNeighbors(id, intSet(ns.conns[layer]), layer, bound)
		wantedSet := intSet(wanted)
		for _, neighborID := range append([]int(nil), ns.conns[layer]...) {
			if _, keep := wantedSet[neighborID]; !keep {
				g.removeLink(layer, id, neighborID)
				g.markShrinkIfOverBound(neighborID, layer, &queue, queued)
			}
		}
		// The node itself may still be over bound only if keepPruned or the
		// heuristic returned fewer entries than currently linked; re-enqueue
		// defensively to guarantee termination at a legal state.
		if len(ns.conns[layer]) > bound {
			g.markShrink(id, &queue, queued)
		}
	}
}

func (g *Graph[T]) markShrinkIfOverBound(id, layer int, queue *[]int, queued map[int]struct{}) {
	ns, ok := g.nodes[id]
	if !ok || ns.deleted || layer > ns.level {
		return
	}
	if len(ns.conns[layer]) > g.MaxDegree(layer) {
		g.markShrink(id, queue, queued)
	}
}

// nearestAlive returns the up-to-limit closest alive nodes that span layer,
// excluding the target itself, ordered by (distance, id). It is the
// connectivity fallback when the local deletion candidate pool contains no
// surviving node.
func (g *Graph[T]) nearestAlive(target, layer, limit int) []int {
	targetNS := g.nodes[target]
	entries := make([]scoredCandidate, 0)
	for id, ns := range g.nodes {
		if id == target || ns.deleted || ns.level < layer {
			continue
		}
		entries = append(entries, scoredCandidate{id: id, d: g.dist(targetNS.vec, ns.vec)})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].d != entries[j].d {
			return entries[i].d < entries[j].d
		}
		return entries[i].id < entries[j].id
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	out := make([]int, len(entries))
	for i, entry := range entries {
		out[i] = entry.id
	}
	sort.Ints(out)
	return out
}

// finiteDistance guards against misbehaving distance functions in searches.
func finiteDistance(d float64) bool {
	return !math.IsNaN(d) && !math.IsInf(d, 0)
}
