// Package graphsum provides canonical summarization of rooted, directed,
// labeled multigraphs with at most 8 nodes.
//
// Canonical form is defined by exact enumeration over all node numberings
// (n! <= 40320 for n <= 8): the canonical encoding is the lexicographically
// minimal serialization over every numbering. Edge insertion order is
// irrelevant; parallel edges are preserved as multiplicities. An independent
// permutation checker re-verifies every claimed result, so correctness never
// relies on recursive hashing that could be truncated at cycles.
package graphsum

import (
	"errors"
	"fmt"
	"sort"
)

// MaxNodes is the hard limit on graph size; it keeps exact enumeration
// tractable (8! = 40320 numberings).
const MaxNodes = 8

// Graph is a rooted, directed, labeled multigraph.
// Node IDs are stable identities chosen by the caller; they persist across
// edits and are what canonical numberings and orbits refer to.
type Graph struct {
	root    int
	hasRoot bool
	labels  map[int]string
	// adj[from][to][label] = multiplicity (parallel edges preserved)
	adj map[int]map[int]map[string]int
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{
		labels: make(map[int]string),
		adj:    make(map[int]map[int]map[string]int),
	}
}

// Clone deep-copies the graph.
func (g *Graph) Clone() *Graph {
	h := NewGraph()
	h.root = g.root
	h.hasRoot = g.hasRoot
	for id, l := range g.labels {
		h.labels[id] = l
	}
	for u, row := range g.adj {
		for v, cell := range row {
			for l, c := range cell {
				for k := 0; k < c; k++ {
					h.AddEdge(u, v, l)
				}
			}
		}
	}
	return h
}

// NumNodes returns the number of nodes.
func (g *Graph) NumNodes() int { return len(g.labels) }

// NodeIDs returns node IDs in ascending order (deterministic).
func (g *Graph) NodeIDs() []int {
	ids := make([]int, 0, len(g.labels))
	for id := range g.labels {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// Label returns the label of node id.
func (g *Graph) Label(id int) (string, bool) {
	l, ok := g.labels[id]
	return l, ok
}

// Root returns the root node id and whether a root is set.
func (g *Graph) Root() (int, bool) { return g.root, g.hasRoot }

// Multiplicity returns how many parallel edges carry (from,to,label).
func (g *Graph) Multiplicity(from, to int, label string) int {
	row, ok := g.adj[from]
	if !ok {
		return 0
	}
	cell, ok := row[to]
	if !ok {
		return 0
	}
	return cell[label]
}

// AddNode adds a node with a stable identity and label.
func (g *Graph) AddNode(id int, label string) error {
	if _, dup := g.labels[id]; dup {
		return fmt.Errorf("node %d already exists", id)
	}
	if len(g.labels) >= MaxNodes {
		return fmt.Errorf("graph exceeds %d nodes", MaxNodes)
	}
	g.labels[id] = label
	return nil
}

// RemoveNode removes a node and all incident edges.
func (g *Graph) RemoveNode(id int) error {
	if _, ok := g.labels[id]; !ok {
		return fmt.Errorf("node %d does not exist", id)
	}
	delete(g.labels, id)
	delete(g.adj, id)
	for _, row := range g.adj {
		delete(row, id)
	}
	if g.hasRoot && g.root == id {
		g.hasRoot = false
	}
	return nil
}

// SetLabel changes a node's label. Returns false if it was a no-op.
func (g *Graph) SetLabel(id int, label string) (bool, error) {
	old, ok := g.labels[id]
	if !ok {
		return false, fmt.Errorf("node %d does not exist", id)
	}
	if old == label {
		return false, nil
	}
	g.labels[id] = label
	return true, nil
}

// SetRoot designates the root node.
func (g *Graph) SetRoot(id int) (bool, error) {
	if _, ok := g.labels[id]; !ok {
		return false, fmt.Errorf("node %d does not exist", id)
	}
	if g.hasRoot && g.root == id {
		return false, nil
	}
	g.root = id
	g.hasRoot = true
	return true, nil
}

// AddEdge adds one parallel edge (from,to,label).
func (g *Graph) AddEdge(from, to int, label string) error {
	if _, ok := g.labels[from]; !ok {
		return fmt.Errorf("node %d does not exist", from)
	}
	if _, ok := g.labels[to]; !ok {
		return fmt.Errorf("node %d does not exist", to)
	}
	row, ok := g.adj[from]
	if !ok {
		row = make(map[int]map[string]int)
		g.adj[from] = row
	}
	cell, ok := row[to]
	if !ok {
		cell = make(map[string]int)
		row[to] = cell
	}
	cell[label]++
	return nil
}

// RemoveEdge removes one parallel edge (from,to,label); false if none existed.
func (g *Graph) RemoveEdge(from, to int, label string) bool {
	row, ok := g.adj[from]
	if !ok {
		return false
	}
	cell, ok := row[to]
	if !ok || cell[label] == 0 {
		return false
	}
	cell[label]--
	if cell[label] == 0 {
		delete(cell, label)
	}
	if len(cell) == 0 {
		delete(row, to)
	}
	if len(row) == 0 {
		delete(g.adj, from)
	}
	return true
}

// validateForSummary checks the graph is summarizable.
func (g *Graph) validateForSummary() error {
	if len(g.labels) > MaxNodes {
		return fmt.Errorf("graph exceeds %d nodes", MaxNodes)
	}
	if len(g.labels) > 0 && !g.hasRoot {
		return errors.New("root not set")
	}
	return nil
}
