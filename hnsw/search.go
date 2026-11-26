package hnsw

import (
	"container/heap"
	"fmt"
	"sort"
)

// Result is one ranked search hit.
type Result struct {
	ID       int
	Distance float64
}

// candidate is one entry of the beam-search heaps. Smaller distance wins; ID
// breaks ties so ranking is deterministic for equal distances.
type candidate struct {
	id int
	d  float64
}

// minCandidates is a min-heap ordered by (distance, id).
type minCandidates []candidate

func (h minCandidates) Len() int { return len(h) }
func (h minCandidates) Less(i, j int) bool {
	if h[i].d != h[j].d {
		return h[i].d < h[j].d
	}
	return h[i].id < h[j].id
}
func (h minCandidates) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *minCandidates) Push(x any)   { *h = append(*h, x.(candidate)) }
func (h *minCandidates) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// maxCandidates is a max-heap ordered by (distance, id); the worst kept hit
// sits at index 0.
type maxCandidates []candidate

func (h maxCandidates) Len() int { return len(h) }
func (h maxCandidates) Less(i, j int) bool {
	if h[i].d != h[j].d {
		return h[i].d > h[j].d
	}
	return h[i].id > h[j].id
}
func (h maxCandidates) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *maxCandidates) Push(x any)   { *h = append(*h, x.(candidate)) }
func (h *maxCandidates) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// KNN returns the k nearest alive nodes to the query vector.
//
// Upper layers are traversed greedily (beam width 1). Layer 0 runs the
// standard ef-width beam search: tombstoned nodes are never expanded or
// returned, equal-distance ties are broken by node ID, and the result is
// sorted ascending by (distance, id). An empty graph yields no results.
func (g *Graph[T]) KNN(vec T, k, ef int) ([]Result, error) {
	if k <= 0 {
		return nil, fmt.Errorf("hnsw: k must be >= 1, got %d", k)
	}
	if ef <= 0 {
		return nil, fmt.Errorf("hnsw: ef must be >= 1, got %d", ef)
	}
	if g.entry == -1 {
		return []Result{}, nil
	}
	if ef < k {
		ef = k
	}

	cache := map[int]float64{}
	distanceTo := func(id int) float64 {
		if d, ok := cache[id]; ok {
			return d
		}
		d := g.dist(vec, g.nodes[id].vec)
		cache[id] = d
		return d
	}

	entryID := g.entry
	current := entryID
	currentDist := distanceTo(entryID)

	// Greedy descent through every layer above layer 0.
	for layer := g.nodes[entryID].level; layer > 0; layer-- {
		next, nextDist := g.greedyStep(layer, current, currentDist, distanceTo)
		current, currentDist = next, nextDist
	}

	hits := g.beamSearch(0, current, currentDist, ef, distanceTo)
	if k < len(hits) {
		hits = hits[:k]
	}
	out := make([]Result, len(hits))
	for i, hit := range hits {
		out[i] = Result{ID: hit.id, Distance: hit.d}
	}
	return out, nil
}

// KNNFrom runs KNN with the vector of an existing alive node as the query.
// The query node itself is excluded from the results.
func (g *Graph[T]) KNNFrom(nodeID, k, ef int) ([]Result, error) {
	ns, ok := g.nodes[nodeID]
	if !ok {
		return nil, fmt.Errorf("hnsw: unknown node %d", nodeID)
	}
	if ns.deleted {
		return nil, fmt.Errorf("hnsw: query node %d is deleted", nodeID)
	}
	hits, err := g.KNN(ns.vec, k+1, ef)
	if err != nil {
		return nil, err
	}
	out := make([]Result, 0, k)
	for _, hit := range hits {
		if hit.ID != nodeID {
			out = append(out, hit)
		}
	}
	if len(out) > k {
		out = out[:k]
	}
	return out, nil
}

// greedyStep walks one upper layer by repeatedly moving to the closest alive
// neighbor until no neighbor improves on the current node.
func (g *Graph[T]) greedyStep(layer, start int, startDist float64, distanceTo func(int) float64) (int, float64) {
	current, currentDist := start, startDist
	visited := map[int]struct{}{start: {}}
	for {
		ns := g.nodes[current]
		best, bestDist := current, currentDist
		for _, neighborID := range ns.conns[layer] {
			neighbor := g.nodes[neighborID]
			if neighbor.deleted {
				continue
			}
			if _, seen := visited[neighborID]; seen {
				continue
			}
			visited[neighborID] = struct{}{}
			if d := distanceTo(neighborID); d < bestDist || (d == bestDist && neighborID < best) {
				best, bestDist = neighborID, d
			}
		}
		if best == current {
			return current, currentDist
		}
		current, currentDist = best, bestDist
	}
}

// beamSearch implements the ef-width search loop on one layer. Candidates is
// a min-heap of frontier nodes; found is a max-heap capped at ef.
func (g *Graph[T]) beamSearch(layer, entryID int, entryDist float64, ef int, distanceTo func(int) float64) []candidate {
	frontier := &minCandidates{{id: entryID, d: entryDist}}
	heap.Init(frontier)
	found := &maxCandidates{{id: entryID, d: entryDist}}
	heap.Init(found)
	visited := map[int]struct{}{entryID: {}}

	for frontier.Len() > 0 {
		closest := heap.Pop(frontier).(candidate)
		worst := (*found)[0]
		if closest.d > worst.d || (closest.d == worst.d && closest.id > worst.id) {
			break
		}

		ns := g.nodes[closest.id]
		for _, neighborID := range ns.conns[layer] {
			neighbor := g.nodes[neighborID]
			if neighbor.deleted {
				continue
			}
			if _, seen := visited[neighborID]; seen {
				continue
			}
			visited[neighborID] = struct{}{}
			d := distanceTo(neighborID)
			if !finiteDistance(d) {
				continue
			}

			if found.Len() < ef || d < (*found)[0].d || (d == (*found)[0].d && neighborID < (*found)[0].id) {
				heap.Push(frontier, candidate{id: neighborID, d: d})
				heap.Push(found, candidate{id: neighborID, d: d})
				if found.Len() > ef {
					heap.Pop(found)
				}
			}
		}
	}

	out := make([]candidate, found.Len())
	copy(out, *found)
	sort.Slice(out, func(i, j int) bool {
		if out[i].d != out[j].d {
			return out[i].d < out[j].d
		}
		return out[i].id < out[j].id
	})
	return out
}
