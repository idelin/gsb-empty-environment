// Package tileindex tracks the set of available map tiles and resolves
// requests for missing tiles to the nearest available ancestor, reporting
// the exact rational sub-rectangle of the ancestor texture to crop.
package tileindex

import (
	"math/big"
	"sync/atomic"
)

// Key identifies a single tile in a quadtree pyramid: level Z, column X,
// row Y. At level z the valid coordinate range is [0, 2^z).
type Key struct {
	Z, X, Y int
}

// Valid reports whether k is a well-formed tile coordinate.
func (k Key) Valid() bool {
	if k.Z < 0 || k.X < 0 || k.Y < 0 {
		return false
	}
	n := int64(1) << uint(k.Z)
	return int64(k.X) < n && int64(k.Y) < n
}

// Parent returns the unique ancestor of k one level up. The caller must
// ensure k.Z > 0. Only the true ancestor (x/2, y/2) is used; neighbouring
// tiles are never substituted.
func (k Key) Parent() Key {
	return Key{Z: k.Z - 1, X: k.X / 2, Y: k.Y / 2}
}

// Region is an exact sub-rectangle of a tile's texture in UV space, with
// each edge in [0, 1]. Coordinates are rational so no precision is lost at
// any zoom depth.
type Region struct {
	U0, V0, U1, V1 *big.Rat
}

// FullRegion returns the region covering an entire tile.
func FullRegion() Region {
	return Region{big.NewRat(0, 1), big.NewRat(0, 1), big.NewRat(1, 1), big.NewRat(1, 1)}
}

// subRegion computes the exact UV region that tile (z, x, y) occupies
// within its ancestor k levels up. The scaling formula is
//
//	u0 = (x mod 2^k) / 2^k,  u1 = (x mod 2^k + 1) / 2^k
//
// and likewise for v with y.
func subRegion(z, x, y, k int) Region {
	if k == 0 {
		return FullRegion()
	}
	scale := int64(1) << uint(k)
	mask := scale - 1
	rx := int64(x) & mask
	ry := int64(y) & mask
	return Region{
		U0: big.NewRat(rx, scale),
		V0: big.NewRat(ry, scale),
		U1: big.NewRat(rx+1, scale),
		V1: big.NewRat(ry+1, scale),
	}
}

// Snapshot is an immutable view of the available tile set. Snapshots
// obtained before an Update remain valid and unchanged afterwards.
type Snapshot struct {
	tiles map[Key]struct{}
}

// Has reports whether the exact tile k is present in this snapshot.
func (s *Snapshot) Has(k Key) bool {
	_, ok := s.tiles[k]
	return ok
}

// Find resolves a request for tile (z, x, y). If the tile itself is
// available it is returned with the full region. Otherwise the search
// ascends level by level through true ancestors only, returning the
// nearest available ancestor together with the exact rational region of
// its texture that covers the requested tile. If no ancestor (including
// the root) is available, ok is false. Invalid coordinates (negative, or
// out of range for the level) are rejected with ok == false.
func (s *Snapshot) Find(z, x, y int) (tile Key, region Region, ok bool) {
	k := Key{Z: z, X: x, Y: y}
	if !k.Valid() {
		return Key{}, Region{}, false
	}
	for {
		if s.Has(k) {
			return k, subRegion(z, x, y, z-k.Z), true
		}
		if k.Z == 0 {
			return Key{}, Region{}, false
		}
		k = k.Parent()
	}
}

// Index is a tile availability set supporting atomic updates: readers hold
// Snapshots and never observe a partially applied update.
type Index struct {
	current atomic.Value // stores *Snapshot
}

// NewIndex returns an Index containing the given tiles.
func NewIndex(available []Key) *Index {
	idx := &Index{}
	idx.Update(available)
	return idx
}

// Update atomically replaces the available tile set. Snapshots acquired
// before the call keep referring to the old set.
func (idx *Index) Update(available []Key) {
	tiles := make(map[Key]struct{}, len(available))
	for _, k := range available {
		tiles[k] = struct{}{}
	}
	idx.current.Store(&Snapshot{tiles: tiles})
}

// Snapshot returns the current immutable view of the available set.
func (idx *Index) Snapshot() *Snapshot {
	return idx.current.Load().(*Snapshot)
}
