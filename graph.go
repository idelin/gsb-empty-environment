package paretopath

import "fmt"

const MaxVertices = 10

type Edge struct {
	From int
	To   int
	Time int64
	Cost int64
}

type Graph struct {
	vertices int
	adj      [][]Edge
}

func NewGraph(vertices int) (*Graph, error) {
	if vertices < 1 || vertices > MaxVertices {
		return nil, fmt.Errorf("paretopath: vertex count %d outside [1, %d]", vertices, MaxVertices)
	}
	return &Graph{vertices: vertices, adj: make([][]Edge, vertices)}, nil
}

func (g *Graph) AddEdge(from, to int, time, cost int64) error {
	if from < 0 || from >= g.vertices || to < 0 || to >= g.vertices {
		return fmt.Errorf("paretopath: edge (%d, %d) outside vertex range [0, %d)", from, to, g.vertices)
	}
	if time < 0 || cost < 0 {
		return fmt.Errorf("paretopath: edge (%d, %d) has negative time or cost", from, to)
	}
	g.adj[from] = append(g.adj[from], Edge{From: from, To: to, Time: time, Cost: cost})
	return nil
}

func (g *Graph) Vertices() int {
	return g.vertices
}

func (g *Graph) Edges(from int) []Edge {
	return g.adj[from]
}
