package coreidx

import (
	"fmt"
	"sort"
)

// OpKind identifies the kind of a batch operation.
type OpKind int

const (
	// OpAddEdge inserts edge (U, V); idempotent if the edge already exists.
	OpAddEdge OpKind = iota
	// OpDeleteEdge removes edge (U, V); a no-op if the edge is missing.
	OpDeleteEdge
	// OpAddNode inserts an isolated node U (V is ignored); idempotent.
	OpAddNode
)

// Op is a single graph mutation inside a batch.
type Op struct {
	Kind OpKind
	U, V int64
}

// AddEdgeOp returns an Op inserting edge (u, v).
func AddEdgeOp(u, v int64) Op { return Op{Kind: OpAddEdge, U: u, V: v} }

// DeleteEdgeOp returns an Op deleting edge (u, v).
func DeleteEdgeOp(u, v int64) Op { return Op{Kind: OpDeleteEdge, U: u, V: v} }

// AddNodeOp returns an Op inserting isolated node id.
func AddNodeOp(id int64) Op { return Op{Kind: OpAddNode, U: id} }

// CoreChange reports that the core number of Node moved from Old to New.
type CoreChange struct {
	Node     int64
	Old, New int
}

// UpdateResult is the outcome of a successful ApplyBatch.
type UpdateResult struct {
	// Changes lists every node whose core number changed during the batch,
	// sorted by node ID. Old/New are relative to the start/end of the batch.
	Changes []CoreChange
}

// ChangedNodes returns just the IDs of the affected nodes, ascending.
func (r UpdateResult) ChangedNodes() []int64 {
	if len(r.Changes) == 0 {
		return nil
	}
	ids := make([]int64, len(r.Changes))
	for i, c := range r.Changes {
		ids[i] = c.Node
	}
	return ids
}

// ApplyBatch applies a batch of mutations atomically and maintains the core
// numbers incrementally.
//
// Atomicity: if any operation fails (e.g. a self-loop), every operation of
// the batch — including the core-number index — is rolled back and an error
// is returned; the graph is left exactly as before the call.
//
// Incrementality: core numbers are updated by local propagation around the
// endpoints of each touched edge (Sariyuce et al., "Incremental K-core
// Decomposition in Dynamic Graphs"); no full-graph decomposition is run.
// For a single edge change only nodes with core number K = min(core(u),
// core(v)) in the affected K-level component can move, and only by one
// level, so the work is confined to that neighborhood.
func (g *Graph) ApplyBatch(ops []Op) (UpdateResult, error) {
	if !g.built {
		return UpdateResult{}, ErrNotBuilt
	}

	// oldCore records the pre-batch core number of every touched node, both
	// for the change report and for restoring the index on rollback.
	oldCore := make(map[int64]int)
	note := func(id int64) {
		if _, ok := oldCore[id]; !ok {
			oldCore[id] = g.core[id]
		}
	}

	// journal of structural undo actions, replayed in reverse on rollback
	var journal []func()
	rollback := func() {
		for id, c := range oldCore {
			g.core[id] = c
		}
		for i := len(journal) - 1; i >= 0; i-- {
			journal[i]()
		}
	}

	for i, op := range ops {
		switch op.Kind {
		case OpAddNode:
			if g.HasNode(op.U) {
				continue
			}
			id := op.U
			g.AddNode(id)
			g.core[id] = 0
			journal = append(journal, func() {
				delete(g.adj, id)
				delete(g.core, id)
			})
		case OpAddEdge:
			u, v := op.U, op.V
			if u == v {
				rollback()
				return UpdateResult{}, fmt.Errorf("coreidx: batch op %d: %w: (%d, %d)", i, ErrSelfLoop, u, v)
			}
			if g.HasEdge(u, v) {
				continue // idempotent
			}
			uNew := !g.HasNode(u)
			vNew := !g.HasNode(v)
			g.addEdgeRaw(u, v)
			journal = append(journal, func() {
				g.deleteEdgeRaw(u, v)
				if uNew {
					delete(g.adj, u)
					delete(g.core, u)
				}
				if vNew {
					delete(g.adj, v)
					delete(g.core, v)
				}
			})
			g.incInsert(u, v, note)
		case OpDeleteEdge:
			u, v := op.U, op.V
			if u == v {
				rollback()
				return UpdateResult{}, fmt.Errorf("coreidx: batch op %d: %w: (%d, %d)", i, ErrSelfLoop, u, v)
			}
			if !g.HasEdge(u, v) {
				continue // deleting a missing edge changes nothing
			}
			g.deleteEdgeRaw(u, v)
			journal = append(journal, func() { g.addEdgeRaw(u, v) })
			g.incDelete(u, v, note)
		default:
			rollback()
			return UpdateResult{}, fmt.Errorf("coreidx: batch op %d: unknown op kind %d", i, op.Kind)
		}
	}

	var changes []CoreChange
	for id, old := range oldCore {
		if now, ok := g.core[id]; ok && now != old {
			changes = append(changes, CoreChange{Node: id, Old: old, New: now})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Node < changes[j].Node })
	return UpdateResult{Changes: changes}, nil
}

// incInsert updates the core numbers after the edge (u, v) has been added to
// the adjacency structure. g.core still holds the pre-insertion values.
//
// Only nodes with core number K = min(core(u), core(v)) that are reachable
// from an endpoint with core K through nodes with core K can move, and they
// move to K+1. Such a node is promoted iff it survives (K+1)-level peeling
// inside its K-level component: iteratively discard candidate nodes with at
// most K neighbors whose core is >= K; the survivors form (together with the
// higher-core nodes) a subgraph of minimum degree >= K+1.
func (g *Graph) incInsert(u, v int64, note func(int64)) {
	K := g.core[u]
	if g.core[v] < K {
		K = g.core[v]
	}

	// Collect the K-level candidate component reachable from the endpoints
	// with core K, and each candidate's core degree: the number of neighbors
	// with core >= K (this count includes the newly inserted edge).
	cd := make(map[int64]int)
	var component []int64
	visited := make(map[int64]bool)
	var queue []int64
	push := func(x int64) {
		if g.core[x] == K && !visited[x] {
			visited[x] = true
			queue = append(queue, x)
		}
	}
	push(u)
	push(v)
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		component = append(component, w)
		deg := 0
		for x := range g.adj[w] {
			if g.core[x] >= K {
				deg++
			}
			push(x)
		}
		cd[w] = deg
	}

	// Peel candidates whose core degree is too low to reach level K+1.
	removed := make(map[int64]bool)
	var dying []int64
	for _, w := range component {
		if cd[w] <= K {
			removed[w] = true
			dying = append(dying, w)
		}
	}
	for len(dying) > 0 {
		w := dying[0]
		dying = dying[1:]
		for x := range g.adj[w] {
			if g.core[x] == K && !removed[x] {
				cd[x]--
				if cd[x] <= K {
					removed[x] = true
					dying = append(dying, x)
				}
			}
		}
	}

	// Survivors join the (K+1)-core.
	for _, w := range component {
		if !removed[w] {
			note(w)
			g.core[w] = K + 1
		}
	}
}

// incDelete updates the core numbers after the edge (u, v) has been removed
// from the adjacency structure. g.core still holds the pre-deletion values.
//
// Only nodes with core number K = min(core(u), core(v)) can move, and they
// drop to K-1. A node keeps core K iff it retains at least K neighbors with
// core >= K after evicting the nodes that lost their support; eviction
// propagates from the endpoints with core K through the K-level component.
func (g *Graph) incDelete(u, v int64, note func(int64)) {
	K := g.core[u]
	if g.core[v] < K {
		K = g.core[v]
	}

	evicted := make(map[int64]bool)
	mcd := make(map[int64]int) // max core degree: # neighbors with core >= K
	compute := func(w int64) int {
		c, ok := mcd[w]
		if !ok {
			for x := range g.adj[w] {
				if g.core[x] >= K {
					c++
				}
			}
			mcd[w] = c
		}
		return c
	}
	var queue []int64
	tryEvict := func(w int64) {
		if g.core[w] == K && !evicted[w] && compute(w) < K {
			evicted[w] = true
			queue = append(queue, w)
		}
	}
	tryEvict(u)
	tryEvict(v)
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		for x := range g.adj[w] {
			if g.core[x] == K && !evicted[x] {
				mcd[x] = compute(x) - 1
				if mcd[x] < K {
					evicted[x] = true
					queue = append(queue, x)
				}
			}
		}
	}

	for w := range evicted {
		note(w)
		g.core[w] = K - 1
	}
}
