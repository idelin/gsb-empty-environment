package netsec

import "container/heap"

// dijkstra runs label-setting search over manifest edges. Edge costs are
// non-negative, including zero, so zero-cost cycles never relax a settled
// node and termination is guaranteed.
func dijkstra(manifest *Manifest, adjacency [][]Edge, index map[string]int,
	gate []*RejectReason, start int) []string {
	const infinity = int(^uint(0) >> 1)

	nodeCount := len(manifest.Nodes)
	dist := make([]int, nodeCount)
	hops := make([]int, nodeCount)
	bestPath := make([][]string, nodeCount)
	settled := make([]bool, nodeCount)
	for i := range dist {
		dist[i] = infinity
	}
	dist[start] = 0
	bestPath[start] = []string{manifest.Nodes[start].ID}

	// The best eligible non-start destination seen so far. The current node
	// itself is only used as a fallback after migration candidates are known.
	var bestDest int
	haveDest := false

	queue := &priorityQueue{}
	heap.Init(queue)
	heap.Push(queue, &queueItem{
		node: start,
		cost: 0,
		hops: 0,
		path: bestPath[start],
	})

	for queue.Len() > 0 {
		item := heap.Pop(queue).(*queueItem)
		from := item.node
		if settled[from] {
			continue
		}
		if item.cost != dist[from] || item.hops != hops[from] ||
			!pathEqual(item.path, bestPath[from]) {
			continue
		}
		settled[from] = true

		if from != start && gate[from] == nil {
			if !haveDest || routeBetter(dist[from], hops[from], bestPath[from],
				dist[bestDest], hops[bestDest], bestPath[bestDest]) {
				bestDest = from
				haveDest = true
			}
		}

		for _, edge := range adjacency[from] {
			to := index[edge.To]
			if settled[to] || gate[to] != nil {
				continue
			}
			// Security level must never decrease along a migration.
			if manifest.Nodes[to].Security.Level < manifest.Nodes[from].Security.Level {
				continue
			}
			candidatePath := make([]string, 0, len(bestPath[from])+1)
			candidatePath = append(candidatePath, bestPath[from]...)
			candidatePath = append(candidatePath, manifest.Nodes[to].ID)
			candidateCost := dist[from] + edge.Cost
			candidateHops := hops[from] + 1

			if routeBetter(candidateCost, candidateHops, candidatePath,
				dist[to], hops[to], bestPath[to]) {
				dist[to] = candidateCost
				hops[to] = candidateHops
				bestPath[to] = candidatePath
				heap.Push(queue, &queueItem{
					node: to,
					cost: candidateCost,
					hops: candidateHops,
					path: candidatePath,
				})
			}
		}
	}

	// An eligible migration to another reachable node always wins over
	// staying; only when none exists do we hand back no migration, letting
	// Select fall back to the current node when it remains eligible.
	if haveDest {
		return bestPath[bestDest]
	}
	return nil
}

func routeBetter(cost, hops int, path []string, oldCost, oldHops int, oldPath []string) bool {
	const infinity = int(^uint(0) >> 1)
	if oldCost == infinity {
		return true
	}
	if cost != oldCost {
		return cost < oldCost
	}
	if hops != oldHops {
		return hops < oldHops
	}
	return comparePaths(path, oldPath) < 0
}

func comparePaths(a, b []string) int {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func pathEqual(a, b []string) bool {
	return comparePaths(a, b) == 0
}

type queueItem struct {
	node int
	cost int
	hops int
	path []string
}

type priorityQueue []*queueItem

func (q priorityQueue) Len() int { return len(q) }

func (q priorityQueue) Less(i, j int) bool {
	return routeBetter(q[i].cost, q[i].hops, q[i].path,
		q[j].cost, q[j].hops, q[j].path)
}

func (q priorityQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }

func (q *priorityQueue) Push(value any) {
	*q = append(*q, value.(*queueItem))
}

func (q *priorityQueue) Pop() any {
	old := *q
	last := len(old) - 1
	item := old[last]
	old[last] = nil
	*q = old[:last]
	return item
}
