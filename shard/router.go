package shard

import (
	"sort"

	"geocell/grid"
)

// Batch groups cells that share one virtual shard; a batch is the unit of
// cross-node communication.
type Batch struct {
	Shard grid.Cell
	Cells []grid.Cell
}

// Router maps cells to physical nodes via virtual shards and a consistent
// hash ring. Batching by shard is what makes topological queries cheap:
// a k-ring touches only a handful of shards, so only a handful of
// cross-node messages are needed instead of one per cell.
type Router struct {
	sharder Sharder
	ring    *Ring
}

// NewRouter builds a Router from a sharder and a hash ring.
func NewRouter(sharder Sharder, ring *Ring) *Router {
	return &Router{sharder: sharder, ring: ring}
}

// Sharder returns the router's sharder.
func (rt *Router) Sharder() Sharder { return rt.sharder }

// Ring returns the router's hash ring.
func (rt *Router) Ring() *Ring { return rt.ring }

// Route returns the node owning the given cell.
func (rt *Router) Route(c grid.Cell) (NodeID, error) {
	key, err := rt.sharder.Key(c)
	if err != nil {
		return "", err
	}
	n, ok := rt.ring.Owner(key)
	if !ok {
		return "", ErrNoNodes
	}
	return n, nil
}

// RouteBatch groups cells by shard and then by owning node. Cells within
// one batch are guaranteed to share a shard and therefore a node.
func (rt *Router) RouteBatch(cells []grid.Cell) (map[NodeID][]Batch, error) {
	byShard := make(map[uint64][]grid.Cell)
	var shardOf = make(map[uint64]grid.Cell)
	for _, c := range cells {
		sh, err := rt.sharder.Of(c)
		if err != nil {
			return nil, err
		}
		key := uint64(sh)
		byShard[key] = append(byShard[key], c)
		shardOf[key] = sh
	}
	out := make(map[NodeID][]Batch)
	for key, group := range byShard {
		n, ok := rt.ring.Owner(key)
		if !ok {
			return nil, ErrNoNodes
		}
		out[n] = append(out[n], Batch{Shard: shardOf[key], Cells: group})
	}
	// deterministic ordering for reproducibility
	for n := range out {
		bs := out[n]
		sort.Slice(bs, func(a, b int) bool { return bs[a].Shard < bs[b].Shard })
		out[n] = bs
	}
	return out, nil
}
