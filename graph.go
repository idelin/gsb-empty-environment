package netsec

import "fmt"

// RoadGraph is a mutable directed adjacency-list implementation of Graph.
type RoadGraph struct {
	out map[Cell][]Edge
}

// NewRoadGraph returns an empty road graph.
func NewRoadGraph() *RoadGraph {
	return &RoadGraph{out: make(map[Cell][]Edge)}
}

// AddEdge inserts or replaces a directed edge of the given non-negative length.
func (g *RoadGraph) AddEdge(from, to Cell, length int) error {
	if length < 0 {
		return fmt.Errorf("%w: edge length must be non-negative", ErrInvalidProblem)
	}
	edges := g.out[from]
	for i := range edges {
		if edges[i].To == to {
			edges[i].Length = length
			g.out[from] = edges
			return nil
		}
	}
	g.out[from] = append(edges, Edge{To: to, Length: length})
	return nil
}

// AddUndirected inserts the link in both directions with the same length.
func (g *RoadGraph) AddUndirected(a, b Cell, length int) error {
	if err := g.AddEdge(a, b, length); err != nil {
		return err
	}
	return g.AddEdge(b, a, length)
}

// RemoveEdge deletes the directed edge from->to and reports whether it existed.
func (g *RoadGraph) RemoveEdge(from, to Cell) bool {
	edges := g.out[from]
	for i := range edges {
		if edges[i].To == to {
			g.out[from] = append(edges[:i], edges[i+1:]...)
			return true
		}
	}
	return false
}

// RemoveUndirected deletes the link in both directions.
func (g *RoadGraph) RemoveUndirected(a, b Cell) {
	g.RemoveEdge(a, b)
	g.RemoveEdge(b, a)
}

// Edges returns the directed edges leaving from.
func (g *RoadGraph) Edges(from Cell) []Edge {
	return g.out[from]
}
