package stgrid

import "math"

// CellKey identifies one bucket of the spatiotemporal hash grid: a
// discrete 3D spatial cell inside one time slot.
type CellKey struct {
	X, Y, Z int
	Slot    int
}

// Grid is a spatiotemporal hash grid. Space is partitioned into uniform
// 3D cells of CellSize meters; time is sharded into slots. Entities are
// hashed by the cells their swept capsule overlaps, so neighbor lookup is
// O(1) per entity instead of O(n) pairwise scans.
type Grid struct {
	CellSize float64
	Cells    map[CellKey][]int
}

func NewGrid(cellSize float64) *Grid {
	return &Grid{CellSize: cellSize, Cells: make(map[CellKey][]int)}
}

func (g *Grid) Reset() {
	g.Cells = make(map[CellKey][]int)
}

func (g *Grid) coord(v float64) int {
	return int(math.Floor(v / g.CellSize))
}

// Insert adds entity id to every cell overlapped by the capsule's
// radius-expanded AABB in the given slot. When CellSize is at least the
// capsule's maximum axis extent, a capsule spans at most 2 cells per axis
// (8 total), keeping insertion constant time.
func (g *Grid) Insert(id int, c Capsule, slot int) {
	x0 := g.coord(math.Min(c.P0.X, c.P1.X) - c.Radius)
	x1 := g.coord(math.Max(c.P0.X, c.P1.X) + c.Radius)
	y0 := g.coord(math.Min(c.P0.Y, c.P1.Y) - c.Radius)
	y1 := g.coord(math.Max(c.P0.Y, c.P1.Y) + c.Radius)
	z0 := g.coord(math.Min(c.P0.Z, c.P1.Z) - c.Radius)
	z1 := g.coord(math.Max(c.P0.Z, c.P1.Z) + c.Radius)
	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			for z := z0; z <= z1; z++ {
				k := CellKey{X: x, Y: y, Z: z, Slot: slot}
				g.Cells[k] = append(g.Cells[k], id)
			}
		}
	}
}

// CandidatePairs returns the deduplicated set of entity pairs that share
// at least one spatiotemporal cell. Any two capsules that intersect are
// guaranteed to share a cell, so this broad phase never misses a pair.
func (g *Grid) CandidatePairs() [][2]int {
	seen := make(map[[2]int]struct{})
	var pairs [][2]int
	for _, ids := range g.Cells {
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				a, b := ids[i], ids[j]
				if a > b {
					a, b = b, a
				}
				key := [2]int{a, b}
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				pairs = append(pairs, key)
			}
		}
	}
	return pairs
}
