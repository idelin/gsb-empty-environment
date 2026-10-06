// Package dagpath ranks and unranks source-to-sink walks in a directed
// acyclic graph. Walks are ordered lexicographically by their edge ID
// sequence; the empty walk (valid only when source == sink) sorts first.
// Parallel edges are independent: each edge ID is its own rankable choice.
// Counts and ranks use big.Int, so graphs with astronomically many walks
// are supported. Branches that can never reach the sink contribute zero
// to every count and therefore occupy no ranks.
package dagpath

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// Edge is a single directed arc. IDs need not be contiguous or unique
// across the graph, but two edges leaving the same node must not share
// an ID, and parallel edges (same From/To) must have distinct IDs.
type Edge struct {
	ID       int
	From, To int
}

// Indexer ranks and unranks walks ending at a fixed sink.
type Indexer struct {
	sink  int
	adj   map[int][]Edge // outgoing edges per node, sorted by ID
	count map[int]*big.Int
}

// NewIndexer builds an Indexer for the given edge set and sink.
// It returns an error if the graph contains a directed cycle or a
// duplicate edge ID on the same source node.
func NewIndexer(edges []Edge, sink int) (*Indexer, error) {
	ix := &Indexer{
		sink:  sink,
		adj:   make(map[int][]Edge),
		count: make(map[int]*big.Int),
	}
	for _, e := range edges {
		ix.adj[e.From] = append(ix.adj[e.From], e)
	}
	for from, out := range ix.adj {
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		for i := 1; i < len(out); i++ {
			if out[i].ID == out[i-1].ID {
				return nil, fmt.Errorf("dagpath: duplicate edge id %d on node %d", out[i].ID, from)
			}
		}
		ix.adj[from] = out
	}
	// Memoized path counting with cycle detection: 0=unvisited, 1=in
	// the DFS stack, 2=done.
	state := make(map[int]int)
	var visit func(node int) (*big.Int, error)
	visit = func(node int) (*big.Int, error) {
		switch state[node] {
		case 1:
			return nil, fmt.Errorf("dagpath: cycle reachable from node %d", node)
		case 2:
			return ix.count[node], nil
		}
		state[node] = 1
		total := new(big.Int)
		if node == sink {
			// The empty walk from sink to sink.
			total.SetInt64(1)
		}
		for _, e := range ix.adj[node] {
			sub, err := visit(e.To)
			if err != nil {
				return nil, err
			}
			total.Add(total, sub)
		}
		state[node] = 2
		ix.count[node] = total
		return total, nil
	}
	for node := range ix.adj {
		if _, err := visit(node); err != nil {
			return nil, err
		}
	}
	if _, err := visit(sink); err != nil {
		return nil, err
	}
	return ix, nil
}

// Count returns the number of walks from src to the sink. A node that
// cannot reach the sink has count 0. src == sink counts the empty walk.
func (ix *Indexer) Count(src int) *big.Int {
	if c, ok := ix.count[src]; ok {
		return new(big.Int).Set(c)
	}
	return new(big.Int)
}

// Rank returns the lexicographic rank (by edge ID sequence) of the walk
// from src described by path, a sequence of edge IDs. The walk must be
// valid and end exactly at the sink.
func (ix *Indexer) Rank(src int, path []int) (*big.Int, error) {
	rank := new(big.Int)
	node := src
	for i, id := range path {
		if node == ix.sink {
			// The empty suffix sorts before every outgoing edge.
			rank.Add(rank, big.NewInt(1))
		}
		out := ix.adj[node]
		idx := sort.Search(len(out), func(j int) bool { return out[j].ID >= id })
		if idx >= len(out) || out[idx].ID != id {
			return nil, fmt.Errorf("dagpath: no edge id %d leaving node %d (step %d)", id, node, i)
		}
		for _, e := range out[:idx] {
			rank.Add(rank, ix.count[e.To])
		}
		node = out[idx].To
	}
	if node != ix.sink {
		return nil, fmt.Errorf("dagpath: walk ends at node %d, not sink %d", node, ix.sink)
	}
	return rank, nil
}

// Unrank returns the edge ID sequence of the walk from src with the given
// rank. It returns an error unless 0 <= rank < Count(src).
func (ix *Indexer) Unrank(src int, rank *big.Int) ([]int, error) {
	if rank.Sign() < 0 {
		return nil, errors.New("dagpath: negative rank")
	}
	if rank.Cmp(ix.Count(src)) >= 0 {
		return nil, fmt.Errorf("dagpath: rank %s out of range [0, %s)", rank, ix.Count(src))
	}
	k := new(big.Int).Set(rank)
	node := src
	var path []int
walk:
	for {
		if node == ix.sink {
			if k.Sign() == 0 {
				// Rank 0 at the sink is the empty suffix: stop here.
				return path, nil
			}
			k.Sub(k, big.NewInt(1))
		}
		for _, e := range ix.adj[node] {
			c := ix.count[e.To]
			if k.Cmp(c) < 0 {
				path = append(path, e.ID)
				node = e.To
				continue walk
			}
			k.Sub(k, c)
		}
		// Unreachable: k < Count(src) guarantees a fitting edge exists.
		return nil, fmt.Errorf("dagpath: internal error unranking at node %d", node)
	}
}
