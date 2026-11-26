// Package hnsw maintains a multi-layer HNSW-style undirected graph and
// performs tombstone-aware batch deletion with diversity-preserving
// neighborhood reconstruction.
//
// The graph is imported layer by layer: callers register nodes (coordinates
// plus their maximum layer) and undirected edges. Layer 0 allows up to M0=2M
// connections per node; every higher layer allows up to M connections.
// Distances are computed through a caller-injected distance function, so the
// package is agnostic to the concrete vector representation.
package hnsw

import (
	"errors"
	"fmt"
	"sort"
)

// DistanceFunc computes the distance between two vector coordinates.
type DistanceFunc[T any] func(a, b T) float64

// Config configures a Graph.
type Config[T any] struct {
	// M is the maximum number of neighbors per node on every layer above
	// layer 0. Layer 0 uses M0 = 2*M.
	M int
	// Distance is the caller-injected distance function. It must be
	// symmetric and deterministic.
	Distance DistanceFunc[T]
	// KeepPrunedConnections controls the optional keepPrunedConnections
	// branch of HNSW Algorithm 4: when true, pruned candidates refill the
	// degree budget in ascending distance order.
	KeepPrunedConnections bool
}

// Node is an exported snapshot of a graph node.
type Node[T any] struct {
	ID     int
	Vector T
	Level  int
}

// nodeState stores a node and its per-layer adjacency lists. Every adjacency
// list is a sorted, duplicate-free slice of neighbor IDs.
type nodeState[T any] struct {
	id      int
	vec     T
	level   int
	deleted bool
	conns   [][]int
}

// Graph is a multi-layer undirected HNSW graph.
type Graph[T any] struct {
	m          int
	dist       DistanceFunc[T]
	keepPruned bool
	nodes      map[int]*nodeState[T]
	entry      int
}

// NewGraph validates the configuration and returns an empty graph.
func NewGraph[T any](cfg Config[T]) (*Graph[T], error) {
	if cfg.M < 1 {
		return nil, errors.New("hnsw: M must be >= 1")
	}
	if cfg.Distance == nil {
		return nil, errors.New("hnsw: Distance function is required")
	}
	return &Graph[T]{
		m:          cfg.M,
		dist:       cfg.Distance,
		keepPruned: cfg.KeepPrunedConnections,
		nodes:      map[int]*nodeState[T]{},
		entry:      -1,
	}, nil
}

// M returns the configured per-node degree bound M (layer 0 uses 2*M).
func (g *Graph[T]) M() int { return g.m }

// KeepPrunedConnections reports whether pruned candidates refill degree slots.
func (g *Graph[T]) KeepPrunedConnections() bool { return g.keepPruned }

// MaxDegree returns the degree upper bound for the given layer.
func (g *Graph[T]) MaxDegree(layer int) int {
	if layer == 0 {
		return 2 * g.m
	}
	return g.m
}

// AddNode registers a node with the given identifier, coordinates and maximum
// layer. The first inserted node becomes the entry point; later nodes only
// replace it when they live on a higher layer.
func (g *Graph[T]) AddNode(id int, vec T, level int) error {
	if id < 0 {
		return fmt.Errorf("hnsw: node id must be >= 0, got %d", id)
	}
	if level < 0 {
		return fmt.Errorf("hnsw: node %d level must be >= 0, got %d", id, level)
	}
	if _, exists := g.nodes[id]; exists {
		return fmt.Errorf("hnsw: duplicate node id %d", id)
	}
	g.nodes[id] = &nodeState[T]{
		id:    id,
		vec:   vec,
		level: level,
		conns: make([][]int, level+1),
	}
	if g.entry == -1 || level > g.nodes[g.entry].level ||
		(level == g.nodes[g.entry].level && id < g.entry) {
		g.entry = id
	}
	return nil
}

// AddEdge inserts a symmetric undirected edge between a and b on layer. Both
// nodes must exist, be alive and span the given layer. Degree limits are not
// enforced on import; CheckInvariants reports any imported violation.
func (g *Graph[T]) AddEdge(layer, a, b int) error {
	if a == b {
		return fmt.Errorf("hnsw: self edge on node %d", a)
	}
	an, err := g.liveNode(a, "edge endpoint")
	if err != nil {
		return err
	}
	bn, err := g.liveNode(b, "edge endpoint")
	if err != nil {
		return err
	}
	if layer < 0 || layer > an.level || layer > bn.level {
		return fmt.Errorf("hnsw: layer %d out of range for edge %d-%d", layer, a, b)
	}
	an.conns[layer] = insertID(an.conns[layer], b)
	bn.conns[layer] = insertID(bn.conns[layer], a)
	return nil
}

// SetEntry overrides the entry point. The node must exist and be alive.
// The node must also live on the current highest layer, so the entry
// invariant stays satisfiable.
func (g *Graph[T]) SetEntry(id int) error {
	if _, err := g.liveNode(id, "entry point"); err != nil {
		return err
	}
	maxLevel := -1
	for _, ns := range g.nodes {
		if !ns.deleted && ns.level > maxLevel {
			maxLevel = ns.level
		}
	}
	if g.nodes[id].level != maxLevel {
		return fmt.Errorf("hnsw: entry node %d level %d is below highest layer %d", id, g.nodes[id].level, maxLevel)
	}
	g.entry = id
	return nil
}

// Entry returns the current entry point. The boolean is false on an empty
// graph (no alive nodes).
func (g *Graph[T]) Entry() (int, bool) {
	return g.entry, g.entry != -1
}

// Alive returns the number of non-deleted nodes.
func (g *Graph[T]) Alive() int {
	n := 0
	for _, ns := range g.nodes {
		if !ns.deleted {
			n++
		}
	}
	return n
}

// Node returns an exported snapshot of the node.
func (g *Graph[T]) Node(id int) (Node[T], error) {
	ns, ok := g.nodes[id]
	if !ok {
		return Node[T]{}, fmt.Errorf("hnsw: unknown node %d", id)
	}
	return Node[T]{ID: ns.id, Vector: ns.vec, Level: ns.level}, nil
}

// IsDeleted reports whether id is known and whether it carries a tombstone.
func (g *Graph[T]) IsDeleted(id int) (exists, deleted bool) {
	ns, ok := g.nodes[id]
	if !ok {
		return false, false
	}
	return true, ns.deleted
}

// Level returns the maximum layer of a node.
func (g *Graph[T]) Level(id int) (int, error) {
	ns, ok := g.nodes[id]
	if !ok {
		return 0, fmt.Errorf("hnsw: unknown node %d", id)
	}
	return ns.level, nil
}

// Neighbors returns a copy of the node's sorted adjacency list on layer.
// Deleted nodes report empty neighborhoods.
func (g *Graph[T]) Neighbors(id, layer int) ([]int, error) {
	ns, ok := g.nodes[id]
	if !ok {
		return nil, fmt.Errorf("hnsw: unknown node %d", id)
	}
	if layer < 0 || layer > ns.level {
		return nil, fmt.Errorf("hnsw: layer %d out of range for node %d", layer, id)
	}
	return append([]int(nil), ns.conns[layer]...), nil
}

func (g *Graph[T]) liveNode(id int, what string) (*nodeState[T], error) {
	ns, ok := g.nodes[id]
	if !ok {
		return nil, fmt.Errorf("hnsw: unknown %s node %d", what, id)
	}
	if ns.deleted {
		return nil, fmt.Errorf("hnsw: %s node %d is deleted", what, id)
	}
	return ns, nil
}

// insertID inserts id into a sorted slice while keeping it sorted and unique.
func insertID(list []int, id int) []int {
	idx := sort.SearchInts(list, id)
	if idx < len(list) && list[idx] == id {
		return list
	}
	list = append(list, 0)
	copy(list[idx+1:], list[idx:])
	list[idx] = id
	return list
}

// removeID removes id from a sorted slice.
func removeID(list []int, id int) []int {
	idx := sort.SearchInts(list, id)
	if idx >= len(list) || list[idx] != id {
		return list
	}
	return append(list[:idx], list[idx+1:]...)
}

// containsID reports whether the sorted slice contains id.
func containsID(list []int, id int) bool {
	idx := sort.SearchInts(list, id)
	return idx < len(list) && list[idx] == id
}

func sortedKeys(set map[int]struct{}) []int {
	out := make([]int, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

func intSet(list []int) map[int]struct{} {
	out := make(map[int]struct{}, len(list))
	for _, id := range list {
		out[id] = struct{}{}
	}
	return out
}
