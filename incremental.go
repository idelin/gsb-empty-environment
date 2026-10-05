package coreindex

// EdgeOp is a single edge insertion or deletion inside a batch.
type EdgeOp struct {
	Add  bool // true: insert, false: delete
	U, V int64
}

// AddOp returns an insertion op for edge (u,v).
func AddOp(u, v int64) EdgeOp { return EdgeOp{Add: true, U: u, V: v} }

// DelOp returns a deletion op for edge (u,v).
func DelOp(u, v int64) EdgeOp { return EdgeOp{Add: false, U: u, V: v} }

// ApplyBatch applies a batch of edge insertions/deletions atomically
// and maintains core numbers incrementally.
//
// Semantics:
//   - self-loops are rejected: the whole batch fails with ErrSelfLoop
//     and every change made so far is rolled back;
//   - inserting an existing edge is a no-op (idempotent);
//   - deleting a missing edge is a no-op;
//   - node IDs listed in ensure are created as isolated nodes first
//     (also rolled back on failure).
//
// It returns the nodes whose core number changed as a net effect of
// the batch, mapped to their new core value.
//
// The update never recomputes a full-graph decomposition: each edge
// op only visits the connected component, inside the subgraph of
// nodes with core == K, that contains an endpoint, where
// K = min(core(u), core(v)).
func (ix *Index) ApplyBatch(ops []EdgeOp, ensure ...int64) (map[int64]int, error) {
	snap := ix.snapshot()
	for _, id := range ensure {
		ix.EnsureNode(id)
	}
	changed := make(map[int64]int)
	for _, op := range ops {
		if op.U == op.V {
			ix.restore(snap)
			return nil, ErrSelfLoop
		}
		if op.Add {
			if ix.addEdgeRaw(op.U, op.V) {
				ix.insertCore(op.U, op.V, changed)
			}
		} else {
			if ix.removeEdgeRaw(op.U, op.V) {
				ix.deleteCore(op.U, op.V, changed)
			}
		}
	}
	// Report only net changes relative to the pre-batch state.
	for id, c := range changed {
		if snap.core[id] == c {
			delete(changed, id)
		}
	}
	return changed, nil
}

// AddEdge inserts a single edge (convenience wrapper around ApplyBatch).
func (ix *Index) AddEdge(u, v int64) (map[int64]int, error) {
	return ix.ApplyBatch([]EdgeOp{AddOp(u, v)})
}

// DeleteEdge deletes a single edge (convenience wrapper around ApplyBatch).
func (ix *Index) DeleteEdge(u, v int64) (map[int64]int, error) {
	return ix.ApplyBatch([]EdgeOp{DelOp(u, v)})
}

// componentAt collects the connected component containing start inside
// the subgraph induced by nodes with core == k. visited is shared
// across calls so multiple components are never merged twice.
func (ix *Index) componentAt(start int64, k int, visited map[int64]bool) []int64 {
	comp := []int64{start}
	visited[start] = true
	for i := 0; i < len(comp); i++ {
		x := comp[i]
		for w := range ix.adj[x] {
			if !visited[w] && ix.core[w] == k {
				visited[w] = true
				comp = append(comp, w)
			}
		}
	}
	return comp
}

// coreDegree counts neighbors of w whose core number is at least k.
func (ix *Index) coreDegree(w int64, k int) int {
	c := 0
	for x := range ix.adj[w] {
		if ix.core[x] >= k {
			c++
		}
	}
	return c
}

// insertCore maintains core numbers after edge (u,v) was inserted.
//
// Theory (Sariyuce et al., incremental k-core): an insertion can only
// raise the core of nodes with core == K = min(core(u), core(v)) that
// are reachable from an endpoint through core-K nodes, and only by
// one. Inside that component we peel nodes whose "core degree"
// (neighbors with core >= K, counting only unpeeled component nodes)
// drops to K or below; the survivors move to K+1.
func (ix *Index) insertCore(u, v int64, changed map[int64]int) {
	if ix.core[u] > ix.core[v] {
		u, v = v, u
	}
	k := ix.core[u]
	comp := ix.componentAt(u, k, make(map[int64]bool))

	inComp := make(map[int64]bool, len(comp))
	cd := make(map[int64]int, len(comp))
	evicted := make(map[int64]bool, len(comp))
	var queue []int64
	for _, w := range comp {
		inComp[w] = true
	}
	for _, w := range comp {
		cd[w] = ix.coreDegree(w, k)
		if cd[w] <= k {
			evicted[w] = true
			queue = append(queue, w)
		}
	}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for w := range ix.adj[x] {
			if inComp[w] && !evicted[w] {
				cd[w]--
				if cd[w] <= k {
					evicted[w] = true
					queue = append(queue, w)
				}
			}
		}
	}
	for _, w := range comp {
		if !evicted[w] {
			ix.core[w] = k + 1
			changed[w] = k + 1
		}
	}
}

// deleteCore maintains core numbers after edge (u,v) was deleted.
//
// Dual of insertCore: only nodes with core == K = min(core(u), core(v))
// in the K-component(s) of the endpoints can drop, and only by one.
// Both endpoints may lie in different K-components once the edge is
// gone, so each component is processed. Nodes whose core degree falls
// below K are peeled and move to K-1.
func (ix *Index) deleteCore(u, v int64, changed map[int64]int) {
	k := ix.core[u]
	if ix.core[v] < k {
		k = ix.core[v]
	}
	visited := make(map[int64]bool)
	for _, s := range [2]int64{u, v} {
		if ix.core[s] != k || visited[s] {
			continue
		}
		comp := ix.componentAt(s, k, visited)

		inComp := make(map[int64]bool, len(comp))
		cd := make(map[int64]int, len(comp))
		evicted := make(map[int64]bool, len(comp))
		var queue []int64
		for _, w := range comp {
			inComp[w] = true
		}
		for _, w := range comp {
			cd[w] = ix.coreDegree(w, k)
			if cd[w] < k {
				evicted[w] = true
				queue = append(queue, w)
			}
		}
		for len(queue) > 0 {
			x := queue[0]
			queue = queue[1:]
			for w := range ix.adj[x] {
				if inComp[w] && !evicted[w] {
					cd[w]--
					if cd[w] < k {
						evicted[w] = true
						queue = append(queue, w)
					}
				}
			}
		}
		for _, w := range comp {
			if evicted[w] {
				ix.core[w] = k - 1
				changed[w] = k - 1
			}
		}
	}
}
