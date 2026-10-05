// Package bridgeindex maintains the bridge set of an undirected multigraph
// (parallel edges allowed, self-loops rejected) under atomic batch updates.
// For every bridge it also maintains the number of nodes on each of the two
// sides that would result from deleting that bridge.
//
// Edge IDs are stable: they are assigned by the caller and never rewritten
// by the index. Nodes are implicit; a node exists while at least one edge
// is incident to it, so isolated nodes are not tracked.
//
// The index never reuses a cached DFS order: after every applied batch the
// bridge set and side sizes are recomputed from the full current edge set.
package bridgeindex

import (
	"fmt"
	"sort"
)

// Edge is an undirected edge between two distinct nodes.
type Edge struct {
	ID   int
	U, V int
}

// BridgeInfo describes one bridge in the current graph.
type BridgeInfo struct {
	EdgeID int
	// SideA is the number of nodes on Edge.U's side after deleting the bridge.
	SideA int
	// SideB is the number of nodes on Edge.V's side after deleting the bridge.
	SideB int
}

// Update is one atomic batch of edge insertions and removals.
type Update struct {
	Add    []Edge
	Remove []int // edge IDs
}

// Index is a dynamic bridge index over an undirected multigraph.
type Index struct {
	edges   map[int]Edge       // edge ID -> edge
	adj     map[int][]int      // node -> incident edge IDs
	bridges map[int]BridgeInfo // edge ID -> info, only for current bridges
}

// New returns an empty index.
func New() *Index {
	return &Index{
		edges:   make(map[int]Edge),
		adj:     make(map[int][]int),
		bridges: make(map[int]BridgeInfo),
	}
}

// Apply validates the whole batch and, only if it is valid, applies it
// atomically and recomputes the bridge set from scratch. On any validation
// error the index is left completely unchanged.
func (ix *Index) Apply(u Update) error {
	if err := ix.validate(u); err != nil {
		return err
	}
	for _, id := range u.Remove {
		e := ix.edges[id]
		delete(ix.edges, id)
		ix.adj[e.U] = removeEdgeID(ix.adj[e.U], id)
		ix.adj[e.V] = removeEdgeID(ix.adj[e.V], id)
	}
	for _, e := range u.Add {
		ix.edges[e.ID] = e
		ix.adj[e.U] = append(ix.adj[e.U], e.ID)
		ix.adj[e.V] = append(ix.adj[e.V], e.ID)
	}
	ix.recompute()
	return nil
}

// validate checks the batch against the current state without mutating it.
// Effective edge set after the batch: (current - Remove) + Add.
func (ix *Index) validate(u Update) error {
	removed := make(map[int]bool, len(u.Remove))
	for _, id := range u.Remove {
		if _, ok := ix.edges[id]; !ok {
			return fmt.Errorf("remove: edge %d does not exist", id)
		}
		if removed[id] {
			return fmt.Errorf("remove: edge %d listed twice", id)
		}
		removed[id] = true
	}
	added := make(map[int]bool, len(u.Add))
	for _, e := range u.Add {
		if e.U == e.V {
			return fmt.Errorf("add: edge %d is a self-loop on node %d", e.ID, e.U)
		}
		if added[e.ID] {
			return fmt.Errorf("add: edge id %d listed twice", e.ID)
		}
		added[e.ID] = true
		if _, ok := ix.edges[e.ID]; ok && !removed[e.ID] {
			return fmt.Errorf("add: edge id %d already exists", e.ID)
		}
	}
	return nil
}

// recompute rebuilds the bridge set and both side sizes from the full
// current edge set using Tarjan's low-link algorithm keyed by edge ID, so
// parallel edges between the same endpoints are never mistaken for bridges.
func (ix *Index) recompute() {
	ix.bridges = make(map[int]BridgeInfo)

	visited := make(map[int]bool, len(ix.adj))
	disc := make(map[int]int, len(ix.adj))
	low := make(map[int]int, len(ix.adj))
	sub := make(map[int]int, len(ix.adj))  // DFS subtree sizes
	root := make(map[int]int, len(ix.adj)) // DFS tree root per node
	timer := 0

	type pendingBridge struct {
		edgeID int
		child  int // endpoint that roots the detached subtree
	}
	var pending []pendingBridge

	var dfs func(u, parentEdgeID, treeRoot int) int
	dfs = func(u, parentEdgeID, treeRoot int) int {
		visited[u] = true
		root[u] = treeRoot
		timer++
		disc[u] = timer
		low[u] = timer
		size := 1
		for _, eid := range ix.adj[u] {
			if eid == parentEdgeID {
				continue // skip only the edge we arrived on, by ID
			}
			e := ix.edges[eid]
			v := e.U
			if v == u {
				v = e.V
			}
			if !visited[v] {
				childSize := dfs(v, eid, treeRoot)
				size += childSize
				if low[v] < low[u] {
					low[u] = low[v]
				}
				if low[v] > disc[u] {
					// A parallel edge would have lowered low[v] to
					// disc[u], so this fires only for true bridges.
					pending = append(pending, pendingBridge{eid, v})
					sub[v] = childSize
				}
			} else if disc[v] < low[u] {
				low[u] = disc[v]
			}
		}
		sub[u] = size
		return size
	}

	for node := range ix.adj {
		if !visited[node] {
			dfs(node, -1, node)
		}
	}

	// Component sizes are known only after a whole DFS tree finishes,
	// so resolve the far-side sizes in a second pass.
	for _, pb := range pending {
		e := ix.edges[pb.edgeID]
		childSide := sub[pb.child]
		otherSide := sub[root[pb.child]] - childSide
		info := BridgeInfo{EdgeID: pb.edgeID}
		if e.U == pb.child {
			info.SideA, info.SideB = childSide, otherSide
		} else {
			info.SideA, info.SideB = otherSide, childSide
		}
		ix.bridges[pb.edgeID] = info
	}
}

func removeEdgeID(ids []int, id int) []int {
	for i, x := range ids {
		if x == id {
			return append(ids[:i], ids[i+1:]...)
		}
	}
	return ids
}

// Edges returns the current edges sorted by edge ID.
func (ix *Index) Edges() []Edge {
	out := make([]Edge, 0, len(ix.edges))
	for _, e := range ix.edges {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Bridges returns the current bridges sorted by edge ID.
func (ix *Index) Bridges() []BridgeInfo {
	out := make([]BridgeInfo, 0, len(ix.bridges))
	for _, b := range ix.bridges {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EdgeID < out[j].EdgeID })
	return out
}

// IsBridge reports whether the edge with the given ID is currently a bridge.
func (ix *Index) IsBridge(id int) bool {
	_, ok := ix.bridges[id]
	return ok
}

// BridgeInfo returns the bridge record for the given edge ID.
func (ix *Index) BridgeInfo(id int) (BridgeInfo, bool) {
	b, ok := ix.bridges[id]
	return b, ok
}

// EdgeCount returns the number of live edges.
func (ix *Index) EdgeCount() int { return len(ix.edges) }

// NodeCount returns the number of nodes incident to at least one edge.
func (ix *Index) NodeCount() int { return len(ix.adj) }
