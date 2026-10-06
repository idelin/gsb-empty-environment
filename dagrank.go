// Package dagrank ranks source-to-sink paths in a directed acyclic graph
// by lexicographic order of their edge-id sequences, and supports
// arbitrary-precision rank/unrank conversions.
//
// Ordering rules:
//   - Paths are compared element-wise by edge id; a sequence that is a
//     strict prefix of another sorts first.
//   - The empty path is valid exactly when source == sink, and ranks
//     first among that (source, sink) pair.
//   - Parallel edges (same endpoints, distinct ids) are independent.
//   - Nodes/edges that cannot reach the sink contribute nothing to ranks.
package dagrank

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// Edge is a directed edge identified by a unique id.
type Edge struct {
	ID   int
	From int
	To   int
}

// Graph is a DAG with precomputed per-node adjacency sorted by edge id.
type Graph struct {
	numNodes int
	adj      [][]Edge
	byID     map[int]Edge
}

// NewGraph builds a Graph from numNodes nodes (0..numNodes-1) and the
// given edges. It returns an error if a node index is out of range, an
// edge id is duplicated, or the edges contain a cycle.
func NewGraph(numNodes int, edges []Edge) (*Graph, error) {
	if numNodes < 0 {
		return nil, errors.New("dagrank: negative node count")
	}
	g := &Graph{
		numNodes: numNodes,
		adj:      make([][]Edge, numNodes),
		byID:     make(map[int]Edge, len(edges)),
	}
	for _, e := range edges {
		if e.From < 0 || e.From >= numNodes || e.To < 0 || e.To >= numNodes {
			return nil, fmt.Errorf("dagrank: edge %d references node out of range", e.ID)
		}
		if _, dup := g.byID[e.ID]; dup {
			return nil, fmt.Errorf("dagrank: duplicate edge id %d", e.ID)
		}
		g.byID[e.ID] = e
		g.adj[e.From] = append(g.adj[e.From], e)
	}
	for u := range g.adj {
		sort.Slice(g.adj[u], func(i, j int) bool { return g.adj[u][i].ID < g.adj[u][j].ID })
	}
	if err := g.checkAcyclic(); err != nil {
		return nil, err
	}
	return g, nil
}

func (g *Graph) checkAcyclic() error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]int, g.numNodes)
	var visit func(u int) error
	visit = func(u int) error {
		color[u] = gray
		for _, e := range g.adj[u] {
			switch color[e.To] {
			case gray:
				return fmt.Errorf("dagrank: cycle detected through edge %d", e.ID)
			case white:
				if err := visit(e.To); err != nil {
					return err
				}
			}
		}
		color[u] = black
		return nil
	}
	for u := 0; u < g.numNodes; u++ {
		if color[u] == white {
			if err := visit(u); err != nil {
				return err
			}
		}
	}
	return nil
}

// counter memoizes path counts to a fixed sink.
type counter struct {
	g    *Graph
	sink int
	memo map[int]*big.Int
}

func (g *Graph) newCounter(sink int) *counter {
	return &counter{g: g, sink: sink, memo: make(map[int]*big.Int)}
}

// count returns the number of distinct edge-id sequences from u to the
// sink. When u == sink the empty sequence counts as one path. Nodes that
// cannot reach the sink count zero.
func (c *counter) count(u int) *big.Int {
	if v, ok := c.memo[u]; ok {
		return v
	}
	total := new(big.Int)
	if u == c.sink {
		total.SetInt64(1) // empty path
	}
	for _, e := range c.g.adj[u] {
		total.Add(total, c.count(e.To))
	}
	c.memo[u] = total
	return total
}

// Count returns the number of source-to-sink paths, including the empty
// path when source == sink.
func (g *Graph) Count(source, sink int) (*big.Int, error) {
	if err := g.checkNode(source); err != nil {
		return nil, err
	}
	if err := g.checkNode(sink); err != nil {
		return nil, err
	}
	return g.newCounter(sink).count(source), nil
}

func (g *Graph) checkNode(u int) error {
	if u < 0 || u >= g.numNodes {
		return fmt.Errorf("dagrank: node %d out of range", u)
	}
	return nil
}

// Rank returns the zero-based lexicographic rank of the path given as an
// edge-id sequence from source to sink. The path must be contiguous,
// start at source, and end at sink; the empty path is accepted only when
// source == sink.
func (g *Graph) Rank(source, sink int, path []int) (*big.Int, error) {
	if err := g.checkNode(source); err != nil {
		return nil, err
	}
	if err := g.checkNode(sink); err != nil {
		return nil, err
	}
	c := g.newCounter(sink)
	rank := new(big.Int)
	u := source
	for _, id := range path {
		e, ok := g.byID[id]
		if !ok {
			return nil, fmt.Errorf("dagrank: unknown edge id %d", id)
		}
		if e.From != u {
			return nil, fmt.Errorf("dagrank: edge %d does not leave node %d", id, u)
		}
		for _, f := range g.adj[u] {
			if f.ID >= id {
				break
			}
			rank.Add(rank, c.count(f.To))
		}
		u = e.To
	}
	if u != sink {
		return nil, fmt.Errorf("dagrank: path ends at node %d, not sink %d", u, sink)
	}
	if len(path) == 0 && source != sink {
		return nil, errors.New("dagrank: empty path requires source == sink")
	}
	return rank, nil
}

// Unrank returns the edge-id sequence of the path with the given
// zero-based rank from source to sink. It returns an error if rank is
// negative or not smaller than Count(source, sink).
func (g *Graph) Unrank(source, sink int, rank *big.Int) ([]int, error) {
	if err := g.checkNode(source); err != nil {
		return nil, err
	}
	if err := g.checkNode(sink); err != nil {
		return nil, err
	}
	if rank == nil || rank.Sign() < 0 {
		return nil, errors.New("dagrank: negative rank")
	}
	c := g.newCounter(sink)
	if rank.Cmp(c.count(source)) >= 0 {
		return nil, fmt.Errorf("dagrank: rank %s out of range", rank.String())
	}
	r := new(big.Int).Set(rank)
	path := []int{}
	u := source
	for {
		if u == sink {
			if r.Sign() == 0 {
				return path, nil
			}
			r.Sub(r, big.NewInt(1)) // skip the empty path
		}
		for _, e := range g.adj[u] {
			cnt := c.count(e.To)
			if r.Cmp(cnt) < 0 {
				path = append(path, e.ID)
				u = e.To
				break
			}
			r.Sub(r, cnt)
		}
	}
}
