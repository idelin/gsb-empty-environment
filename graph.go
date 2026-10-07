// Package certcache implements a certificate-based verification cache for
// graph queries. Certificates (reachability witnesses and shortest-path
// proofs) record the versions of the edges they depend on, so that graph
// mutations can invalidate exactly the affected certificates instead of
// flushing the whole cache.
package certcache

import (
	"errors"
	"sync"
)

// ErrVersionWraparound is returned when an edge's version counter would
// exceed the configured maximum (or wrap the uint64 counter). The mutation
// is rejected so that a stale certificate can never accidentally match a
// recycled version number.
var ErrVersionWraparound = errors.New("certcache: edge version wraparound, mutation rejected")

// EdgeKey identifies a directed edge.
type EdgeKey struct {
	From string
	To   string
}

// Edge is a versioned, weighted directed edge.
type Edge struct {
	Key     EdgeKey
	Weight  int64
	Version uint64
}

// Graph is a versioned directed weighted graph. Every edge carries a
// monotonically increasing version; the per-edge counter survives deletion,
// so a re-added edge never reuses an old version. Versions are bounded by
// maxVersion and any mutation that would overflow the bound is rejected
// with ErrVersionWraparound.
type Graph struct {
	mu sync.RWMutex

	edges map[EdgeKey]*Edge
	// nextVersion keeps the last issued version per edge key, including
	// for edges that have been deleted, so versions are never recycled.
	nextVersion map[EdgeKey]uint64
	maxVersion  uint64
	// addEpoch is bumped on every edge insertion. Shortest-path
	// certificates record the epoch they were computed in; a mismatch
	// means a new edge may have created a shorter path.
	addEpoch uint64
}

// NewGraph creates a graph whose edge versions are bounded by maxVersion.
// A maxVersion of 0 means unbounded (uint64 range).
func NewGraph(maxVersion uint64) *Graph {
	if maxVersion == 0 {
		maxVersion = ^uint64(0)
	}
	return &Graph{
		edges:       make(map[EdgeKey]*Edge),
		nextVersion: make(map[EdgeKey]uint64),
		maxVersion:  maxVersion,
	}
}

// bumpVersion issues the next version for key, rejecting wraparound.
// Caller must hold the write lock.
func (g *Graph) bumpVersion(key EdgeKey) (uint64, error) {
	next := g.nextVersion[key] + 1
	if next == 0 || next > g.maxVersion {
		return 0, ErrVersionWraparound
	}
	g.nextVersion[key] = next
	return next, nil
}

// AddEdge inserts a new edge. It returns an error if the edge already
// exists (use SetWeight to change a weight) or if the version counter
// would wrap. A successful insertion bumps the graph's add epoch.
func (g *Graph) AddEdge(from, to string, weight int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := EdgeKey{From: from, To: to}
	if _, ok := g.edges[key]; ok {
		return errors.New("certcache: edge already exists")
	}
	version, err := g.bumpVersion(key)
	if err != nil {
		return err
	}
	g.edges[key] = &Edge{Key: key, Weight: weight, Version: version}
	g.addEpoch++
	return nil
}

// RemoveEdge deletes an edge. It reports whether the edge existed.
func (g *Graph) RemoveEdge(from, to string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := EdgeKey{From: from, To: to}
	if _, ok := g.edges[key]; !ok {
		return false
	}
	delete(g.edges, key)
	return true
}

// SetWeight changes the weight of an existing edge and bumps its version.
// It returns an error if the edge does not exist or the version wraps.
// A weight decrease can create shorter paths through this edge, so it is
// treated like an insertion and also bumps the graph's add epoch.
func (g *Graph) SetWeight(from, to string, weight int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := EdgeKey{From: from, To: to}
	edge, ok := g.edges[key]
	if !ok {
		return errors.New("certcache: edge does not exist")
	}
	version, err := g.bumpVersion(key)
	if err != nil {
		return err
	}
	if weight < edge.Weight {
		g.addEpoch++
	}
	edge.Weight = weight
	edge.Version = version
	return nil
}

// Edge returns a snapshot of the edge, or nil if absent.
func (g *Graph) Edge(from, to string) *Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if e, ok := g.edges[EdgeKey{From: from, To: to}]; ok {
		cp := *e
		return &cp
	}
	return nil
}

// AddEpoch returns the current edge-insertion epoch.
func (g *Graph) AddEpoch() uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.addEpoch
}

// snapshot returns a consistent copy of the edges and the add epoch.
func (g *Graph) snapshot() (map[EdgeKey]Edge, uint64) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	edges := make(map[EdgeKey]Edge, len(g.edges))
	for k, e := range g.edges {
		edges[k] = *e
	}
	return edges, g.addEpoch
}
