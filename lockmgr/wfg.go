package lockmgr

import "sort"

// buildWaitingFor constructs the precise waiting-for graph. Each blocked
// request contributes one outgoing edge per currently held lock that conflicts
// with it, so a request blocked on N shared holders produces N distinct edges.
// Self edges (same transaction) are excluded.
func (m *Manager) buildWaitingFor() ([]Edge, map[uint64]map[uint64][]Edge) {
	var edges []Edge
	adj := make(map[uint64]map[uint64][]Edge)

	resourceKeys := make([]string, 0, len(m.resources))
	for key := range m.resources {
		resourceKeys = append(resourceKeys, key)
	}
	sort.Strings(resourceKeys)

	for _, key := range resourceKeys {
		st := m.resources[key]
		for queuePos, w := range st.queue {
			from := w.txn.id

			// Edges to currently held conflicting locks: one edge per
			// conflicting shared holder (multiple holders -> many edges).
			holderIDs := make([]uint64, 0, len(st.holders))
			for id := range st.holders {
				if id == from {
					continue // exclude self edges
				}
				holderIDs = append(holderIDs, id)
			}
			sort.Slice(holderIDs, func(i, j int) bool { return holderIDs[i] < holderIDs[j] })
			for _, to := range holderIDs {
				holder := st.holders[to]
				if !compatible(holder.mode, w.want) {
					e := Edge{From: from, To: to, Resource: st.resource, Mode: holder.mode}
					edges = append(edges, e)
					if adj[from] == nil {
						adj[from] = make(map[uint64][]Edge)
					}
					adj[from][to] = append(adj[from][to], e)
				}
			}

			// Immediate upgrade deadlocks occur only between two upgrade
			// waiters that each already hold a lock on this resource and
			// request mutually incompatible stronger modes (e.g. S -> X on
			// both sides). Ordinary new waiters only queue FIFO and never
			// contribute waiter-to-waiter edges, so a reader sitting behind
			// a writer is not mistaken for a deadlock.
			for otherPos, other := range st.queue {
				if otherPos == queuePos {
					continue
				}
				to := other.txn.id
				if to == from {
					continue
				}
				if !w.upgrade || !other.upgrade {
					continue
				}
				if !compatible(other.want, w.want) {
					e := Edge{From: from, To: to, Resource: st.resource, Mode: other.want}
					edges = append(edges, e)
					if adj[from] == nil {
						adj[from] = make(map[uint64][]Edge)
					}
					adj[from][to] = append(adj[from][to], e)
				}
			}
		}
	}
	return edges, adj
}

// findAllCycles returns every elementary (minimal) directed cycle of the graph
// in adjacency-list form. Each cycle is rotated so that its smallest node id
// comes first and is returned once, even when parallel edges exist.
func findAllCycles(adj map[uint64]map[uint64][]Edge) [][]uint64 {
	nodes := make([]uint64, 0, len(adj))
	for from := range adj {
		nodes = append(nodes, from)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i] < nodes[j] })

	seen := make(map[string]bool)
	var cycles [][]uint64

	var dfs func(start, current uint64, path []uint64, onPath map[uint64]bool)
	dfs = func(start, current uint64, path []uint64, onPath map[uint64]bool) {
		neighbours := make([]uint64, 0, len(adj[current]))
		for to := range adj[current] {
			neighbours = append(neighbours, to)
		}
		sort.Slice(neighbours, func(i, j int) bool { return neighbours[i] < neighbours[j] })

		for _, next := range neighbours {
			if next < start {
				continue // every cycle is enumerated only from its smallest node
			}
			if next == start && len(path) >= 1 {
				cycle := append([]uint64(nil), path...)
				if !seen[cycleKey(cycle)] {
					seen[cycleKey(cycle)] = true
					cycles = append(cycles, cycle)
				}
				continue
			}
			if onPath[next] {
				continue
			}
			onPath[next] = true
			dfs(start, next, append(path, next), onPath)
			delete(onPath, next)
		}
	}

	for _, start := range nodes {
		onPath := map[uint64]bool{start: true}
		dfs(start, start, []uint64{start}, onPath)
	}

	// Deterministic overall ordering for arbitration and diagnostics.
	sort.Slice(cycles, func(i, j int) bool {
		return cycleKey(cycles[i]) < cycleKey(cycles[j])
	})
	return cycles
}

func cycleKey(cycle []uint64) string {
	buf := make([]byte, 0, len(cycle)*8)
	for i, id := range cycle {
		if i > 0 {
			buf = append(buf, '>')
		}
		buf = appendUint(buf, id)
	}
	return string(buf)
}

func appendUint(buf []byte, v uint64) []byte {
	if v == 0 {
		return append(buf, '0')
	}
	var digits [20]byte
	n := 0
	for v > 0 {
		digits[n] = byte('0' + v%10)
		n++
		v /= 10
	}
	for i := n - 1; i >= 0; i-- {
		buf = append(buf, digits[i])
	}
	return buf
}
