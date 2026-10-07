package traj

import (
	"errors"
	"fmt"
)

// Graph accumulates edges as they arrive in batches. Every imported
// batch bumps the version, which invalidates routes published against
// earlier edge sets.
type Graph struct {
	start   string
	edges   map[string]Edge
	order   []string
	version int
}

// NewGraph creates an empty graph whose trajectories must begin at
// start.
func NewGraph(start string) *Graph {
	return &Graph{start: start, edges: make(map[string]Edge)}
}

// ImportBatch appends a batch of newly arrived edges and bumps the
// edge-set version. Edge IDs must be unique across the whole graph;
// the batch is rejected atomically on duplicates or empty IDs.
func (g *Graph) ImportBatch(edges []Edge) error {
	seen := make(map[string]bool, len(edges))
	for _, e := range edges {
		if e.ID == "" {
			return errors.New("traj: edge with empty ID")
		}
		if seen[e.ID] {
			return fmt.Errorf("traj: duplicate edge ID %q within batch", e.ID)
		}
		if _, ok := g.edges[e.ID]; ok {
			return fmt.Errorf("traj: edge ID %q already imported", e.ID)
		}
		seen[e.ID] = true
	}
	for _, e := range edges {
		g.edges[e.ID] = e
		g.order = append(g.order, e.ID)
	}
	g.version++
	return nil
}

// Version is the current edge-set version. Routes published against an
// older version are stale.
func (g *Graph) Version() int { return g.version }

// Start is the required trajectory start point.
func (g *Graph) Start() string { return g.start }

// Edge looks up an edge by ID.
func (g *Graph) Edge(id string) (Edge, bool) {
	e, ok := g.edges[id]
	return e, ok
}

// Edges returns all edges in import order.
func (g *Graph) Edges() []Edge {
	out := make([]Edge, 0, len(g.order))
	for _, id := range g.order {
		out = append(out, g.edges[id])
	}
	return out
}

// Route is a published plan covering a set of edges, tagged with the
// edge-set version it was computed from.
type Route struct {
	Version int
	// From is the point where the route begins (the session's current
	// point at publish time).
	From    string
	EdgeIDs []string
}

// ErrStaleRoute is returned by CheckRoute when the route's version no
// longer matches the graph, i.e. new edges were imported after the
// route was published.
var ErrStaleRoute = errors.New("traj: route invalidated by newer edge batch")

// CheckRoute verifies that a published route is still valid for the
// current edge set.
func (g *Graph) CheckRoute(r *Route) error {
	if r.Version != g.version {
		return newError(ErrStaleRoute,
			"traj: route version %d stale, edge set now at version %d", r.Version, g.version)
	}
	return nil
}
