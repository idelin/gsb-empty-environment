// Package bridgeindex maintains the bridge set of an undirected multigraph
// under edge insertions and deletions, together with the vertex counts on
// both sides of every bridge.
//
// The graph allows parallel edges but forbids self-loops. Edges carry
// stable IDs assigned at insertion time. Updates are applied in atomic
// batches: a batch is validated as a whole before any mutation happens, so
// a rejected batch leaves the index untouched.
package bridgeindex

import (
	"errors"
	"fmt"
	"sort"
)

// EdgeID is a stable identifier assigned to an edge when it is inserted.
// IDs are never reused, even after the edge is removed.
type EdgeID int64

// Edge is an undirected edge of the multigraph.
type Edge struct {
	ID EdgeID
	U  int
	V  int
}

// UpdateKind selects the operation performed by an Update.
type UpdateKind int

const (
	// AddEdge inserts a new edge between U and V and assigns it a fresh ID.
	AddEdge UpdateKind = iota
	// RemoveEdge deletes the edge with the given ID.
	RemoveEdge
)

// Update is a single mutation inside a batch.
type Update struct {
	Kind UpdateKind
	U    int // AddEdge only
	V    int // AddEdge only
	ID   EdgeID
}

// BridgeInfo describes one bridge of the current graph.
type BridgeInfo struct {
	ID EdgeID
	U  int
	V  int
	// SideA and SideB are the vertex counts of the two components that
	// would result from deleting this edge. SideA <= SideB.
	SideA int
	SideB int
}

type adjEntry struct {
	to int
	id EdgeID
}

// Index maintains the bridge set of the graph.
type Index struct {
	edges   map[EdgeID]Edge
	adj     map[int][]adjEntry
	nextID  EdgeID
	bridges map[EdgeID]BridgeInfo
}

// New returns an empty index.
func New() *Index {
	return &Index{
		edges:   make(map[EdgeID]Edge),
		adj:     make(map[int][]adjEntry),
		bridges: make(map[EdgeID]BridgeInfo),
	}
}

// ErrSelfLoop is returned when a batch tries to insert a self-loop.
var ErrSelfLoop = errors.New("bridgeindex: self-loops are not allowed")

// ApplyBatch validates and applies a batch of updates atomically. If any
// update is invalid (self-loop, unknown or duplicated edge ID), no update
// of the batch is applied and an error is returned. On success it returns
// the IDs assigned to the inserted edges, in batch order.
func (ix *Index) ApplyBatch(batch []Update) ([]EdgeID, error) {
	removing := make(map[EdgeID]bool)
	for i, u := range batch {
		switch u.Kind {
		case AddEdge:
			if u.U == u.V {
				return nil, fmt.Errorf("update %d: %v", i, ErrSelfLoop)
			}
		case RemoveEdge:
			if _, ok := ix.edges[u.ID]; !ok {
				return nil, fmt.Errorf("update %d: unknown edge ID %d", i, u.ID)
			}
			if removing[u.ID] {
				return nil, fmt.Errorf("update %d: edge ID %d removed twice in batch", i, u.ID)
			}
			removing[u.ID] = true
		default:
			return nil, fmt.Errorf("update %d: unknown update kind %d", i, u.Kind)
		}
	}

	var added []EdgeID
	for _, u := range batch {
		switch u.Kind {
		case AddEdge:
			ix.nextID++
			id := ix.nextID
			ix.edges[id] = Edge{ID: id, U: u.U, V: u.V}
			ix.adj[u.U] = append(ix.adj[u.U], adjEntry{to: u.V, id: id})
			ix.adj[u.V] = append(ix.adj[u.V], adjEntry{to: u.U, id: id})
			added = append(added, id)
		case RemoveEdge:
			e := ix.edges[u.ID]
			delete(ix.edges, u.ID)
			ix.adj[e.U] = removeAdj(ix.adj[e.U], u.ID)
			ix.adj[e.V] = removeAdj(ix.adj[e.V], u.ID)
			if len(ix.adj[e.U]) == 0 {
				delete(ix.adj, e.U)
			}
			if len(ix.adj[e.V]) == 0 {
				delete(ix.adj, e.V)
			}
		}
	}
	ix.recompute()
	return added, nil
}

func removeAdj(entries []adjEntry, id EdgeID) []adjEntry {
	for i, e := range entries {
		if e.id == id {
			return append(entries[:i], entries[i+1:]...)
		}
	}
	return entries
}

// Bridges returns the current bridge set, sorted by edge ID.
func (ix *Index) Bridges() []BridgeInfo {
	out := make([]BridgeInfo, 0, len(ix.bridges))
	for _, b := range ix.bridges {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// EdgeCount returns the number of live edges.
func (ix *Index) EdgeCount() int { return len(ix.edges) }

// Edges returns a snapshot of the live edges, sorted by ID.
func (ix *Index) Edges() []Edge {
	out := make([]Edge, 0, len(ix.edges))
	for _, e := range ix.edges {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// recompute rebuilds the bridge set from scratch with an iterative
// low-link (Tarjan) DFS. Parallel edges are handled correctly because the
// DFS tracks the incoming edge ID, not the parent vertex: a second edge to
// an already visited endpoint lowers low[] and disqualifies the bridge.
func (ix *Index) recompute() {
	ix.bridges = make(map[EdgeID]BridgeInfo)
	disc := make(map[int]int, len(ix.adj))
	low := make(map[int]int, len(ix.adj))
	timer := 0

	type frame struct {
		v      int
		inEdge EdgeID
		next   int // index into adj[v]
		size   int // subtree size accumulated so far
		child  int // child currently being explored, -1 if none
		childE EdgeID
	}

	roots := make([]int, 0, len(ix.adj))
	for v := range ix.adj {
		roots = append(roots, v)
	}
	sort.Ints(roots)

	for _, root := range roots {
		if disc[root] != 0 {
			continue
		}
		// pending holds (edgeID, childSubtreeSize) for bridges found in
		// this component; the far side is resolved once the component
		// size is known.
		type pendingBridge struct {
			id  EdgeID
			sub int
		}
		var pending []pendingBridge

		timer++
		disc[root] = timer
		low[root] = timer
		stack := []frame{{v: root, inEdge: 0, size: 1, child: -1}}

		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.next < len(ix.adj[top.v]) {
				e := ix.adj[top.v][top.next]
				top.next++
				if e.id == top.inEdge {
					continue
				}
				if disc[e.to] == 0 {
					timer++
					disc[e.to] = timer
					low[e.to] = timer
					top.child = e.to
					top.childE = e.id
					stack = append(stack, frame{v: e.to, inEdge: e.id, size: 1, child: -1})
				} else if disc[e.to] < low[top.v] {
					low[top.v] = disc[e.to]
				}
				continue
			}
			// Frame finished: propagate low-link and subtree size.
			done := *top
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				// Root of the DFS tree: component size is done.size.
				for _, p := range pending {
					b := ix.bridges[p.id]
					a, bb := p.sub, done.size-p.sub
					if a > bb {
						a, bb = bb, a
					}
					b.SideA, b.SideB = a, bb
					ix.bridges[p.id] = b
				}
				break
			}
			parent := &stack[len(stack)-1]
			parent.size += done.size
			if low[done.v] < low[parent.v] {
				low[parent.v] = low[done.v]
			}
			if low[done.v] > disc[parent.v] {
				e := ix.edges[parent.childE]
				ix.bridges[parent.childE] = BridgeInfo{ID: parent.childE, U: e.U, V: e.V}
				pending = append(pending, pendingBridge{id: parent.childE, sub: done.size})
			}
			parent.child = -1
		}
	}
}
