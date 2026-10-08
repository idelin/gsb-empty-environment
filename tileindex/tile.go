// Package tileindex tracks the set of available map tiles and resolves
// fallback texture intervals from ancestor tiles.
package tileindex

import "fmt"

// TileID identifies a tile in a quadtree pyramid: level Z (0 = root),
// column X and row Y, both in [0, 2^Z).
type TileID struct {
	Z, X, Y int
}

// NewTileID validates z/x/y and returns the tile identifier.
// Negative coordinates and coordinates outside [0, 2^Z) are rejected.
func NewTileID(z, x, y int) (TileID, error) {
	if z < 0 {
		return TileID{}, fmt.Errorf("tileindex: negative level %d", z)
	}
	if x < 0 || y < 0 {
		return TileID{}, fmt.Errorf("tileindex: negative coordinate (x=%d, y=%d)", x, y)
	}
	if z >= 62 {
		return TileID{}, fmt.Errorf("tileindex: level %d too large", z)
	}
	if x >= 1<<uint(z) || y >= 1<<uint(z) {
		return TileID{}, fmt.Errorf("tileindex: coordinate (x=%d, y=%d) out of range for level %d", x, y, z)
	}
	return TileID{Z: z, X: x, Y: y}, nil
}

// Valid reports whether t is a well-formed tile identifier.
func (t TileID) Valid() bool {
	if t.Z < 0 || t.Z >= 62 || t.X < 0 || t.Y < 0 {
		return false
	}
	return t.X < 1<<uint(t.Z) && t.Y < 1<<uint(t.Z)
}

// Parent returns the unique ancestor of t one level up, obtained by
// halving both coordinates. The boolean is false for the root tile.
// Note: the parent is derived purely by coordinate scaling; a missing
// parent is never substituted by a neighbouring tile.
func (t TileID) Parent() (TileID, bool) {
	if t.Z <= 0 {
		return TileID{}, false
	}
	return TileID{Z: t.Z - 1, X: t.X >> 1, Y: t.Y >> 1}, true
}
