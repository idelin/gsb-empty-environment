package bridgeindex

// ReferenceBridges is an independent, deliberately naive checker intended
// for validating Index on small graphs. For every edge it rebuilds the
// adjacency without that edge and recomputes connectivity from scratch
// with a fresh BFS; nothing (no DFS order, no component labels) is cached
// across edges. It returns the bridge set in the same shape as
// Index.Bridges plus the number of edges actually examined, so callers can
// confirm the check really ran per edge.
func ReferenceBridges(edges []Edge) (map[EdgeID]BridgeInfo, int) {
	compSize := componentSizes(edges)
	out := make(map[EdgeID]BridgeInfo)
	checked := 0
	for _, e := range edges {
		checked++
		reached := bfsWithout(edges, e.ID, e.U)
		if !reached[e.V] {
			a := len(reached)
			b := compSize[e.U] - a
			if a > b {
				a, b = b, a
			}
			out[e.ID] = BridgeInfo{ID: e.ID, U: e.U, V: e.V, SideA: a, SideB: b}
		}
	}
	return out, checked
}

func componentSizes(edges []Edge) map[int]int {
	sizes := make(map[int]int)
	seen := make(map[int]bool)
	for _, e := range edges {
		if seen[e.U] {
			continue
		}
		reached := bfsWithout(edges, 0, e.U)
		for v := range reached {
			seen[v] = true
			sizes[v] = len(reached)
		}
	}
	return sizes
}

// bfsWithout performs a fresh BFS from start over edges, ignoring the edge
// with ID skip (0 skips nothing).
func bfsWithout(edges []Edge, skip EdgeID, start int) map[int]bool {
	reached := map[int]bool{start: true}
	queue := []int{start}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, e := range edges {
			if e.ID == skip {
				continue
			}
			var to int
			switch v {
			case e.U:
				to = e.V
			case e.V:
				to = e.U
			default:
				continue
			}
			if !reached[to] {
				reached[to] = true
				queue = append(queue, to)
			}
		}
	}
	return reached
}
