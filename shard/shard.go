// Package shard implements virtual sharding and locality-aware routing on
// top of the grid cell hierarchy, plus a concurrent scheduling engine that
// minimizes cross-node traffic for topological (k-ring) queries.
package shard

import (
	"errors"
	"fmt"

	"geocell/grid"
)

// NodeID identifies a physical node.
type NodeID string

var (
	ErrNoNodes       = errors.New("shard: no nodes in ring")
	ErrShutdown      = errors.New("shard: engine is shut down")
	ErrCellTooCoarse = errors.New("shard: cell resolution coarser than shard resolution")
)

// Sharder maps cells to virtual shards. A shard is identified by the
// ancestor cell at the shard resolution, so shards are contiguous regions
// of the sphere and topological neighbors overwhelmingly share a shard.
type Sharder struct {
	// Res is the shard (ancestor) resolution.
	Res int
}

// NewSharder returns a Sharder using the given ancestor resolution.
func NewSharder(res int) (Sharder, error) {
	if res < 0 || res > grid.MaxRes {
		return Sharder{}, fmt.Errorf("%w: %d", grid.ErrInvalidResolution, res)
	}
	return Sharder{Res: res}, nil
}

// Of returns the shard (ancestor cell) owning the given cell.
func (s Sharder) Of(c grid.Cell) (grid.Cell, error) {
	if c.Res() < s.Res {
		return 0, fmt.Errorf("%w: cell res %d < shard res %d", ErrCellTooCoarse, c.Res(), s.Res)
	}
	return c.Parent(s.Res)
}

// Key returns the uint64 shard key for a cell.
func (s Sharder) Key(c grid.Cell) (uint64, error) {
	sh, err := s.Of(c)
	return uint64(sh), err
}
