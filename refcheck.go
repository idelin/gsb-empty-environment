package bridgeindex

import "fmt"

// ReferenceCheck verifies the index against a brute-force reference on
// small graphs: for every live edge it rebuilds the graph without that
// edge, recomputes connected components from scratch (BFS), and compares
// the resulting bridge verdict and both side sizes with the index.
//
// It returns the number of edges actually checked. A mismatch is reported
// together with the count of edges checked up to and including the
// offending one.
func ReferenceCheck(ix *Index) (int, error) {
	edges := ix.Edges()
	checked := 0
	bridgeCount := 0
	for _, e := range edges {
		checked++

		// Rebuild adjacency without edge e.
		adj := make(map[int][]int)
		nodes := map[int]bool{e.U: true, e.V: true}
		for _, f := range edges {
			if f.ID == e.ID {
				continue
			}
			adj[f.U] = append(adj[f.U], f.V)
			adj[f.V] = append(adj[f.V], f.U)
			nodes[f.U] = true
			nodes[f.V] = true
		}

		// Recompute connected components from scratch.
		comp := make(map[int]int, len(nodes))
		compSize := make(map[int]int)
		nComp := 0
		for n := range nodes {
			if _, seen := comp[n]; seen {
				continue
			}
			queue := []int{n}
			comp[n] = nComp
			size := 0
			for len(queue) > 0 {
				x := queue[0]
				queue = queue[1:]
				size++
				for _, y := range adj[x] {
					if _, seen := comp[y]; !seen {
						comp[y] = nComp
						queue = append(queue, y)
					}
				}
			}
			compSize[nComp] = size
			nComp++
		}

		// With no self-loops, removing e disconnects its endpoints iff
		// e is a bridge (parallel edges keep them connected).
		refIsBridge := comp[e.U] != comp[e.V]
		info, idxIsBridge := ix.BridgeInfo(e.ID)
		if refIsBridge != idxIsBridge {
			return checked, fmt.Errorf("edge %d (%d-%d): index bridge=%v, reference bridge=%v",
				e.ID, e.U, e.V, idxIsBridge, refIsBridge)
		}
		if refIsBridge {
			bridgeCount++
			ra, rb := compSize[comp[e.U]], compSize[comp[e.V]]
			if info.SideA != ra || info.SideB != rb {
				return checked, fmt.Errorf("edge %d (%d-%d): index sides=(%d,%d), reference sides=(%d,%d)",
					e.ID, e.U, e.V, info.SideA, info.SideB, ra, rb)
			}
		}
	}
	if got := len(ix.Bridges()); got != bridgeCount {
		return checked, fmt.Errorf("index reports %d bridges, reference found %d", got, bridgeCount)
	}
	return checked, nil
}
