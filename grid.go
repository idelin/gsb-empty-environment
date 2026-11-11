package hpastar

import "math"

// Cell is a grid coordinate.
type Cell struct {
	X, Y int
}

var allDirs = [8][2]int{
	{1, 0}, {-1, 0}, {0, 1}, {0, -1},
	{1, 1}, {1, -1}, {-1, 1}, {-1, -1},
}

// Grid is a blocked/walkable tile map.
type Grid struct {
	W, H    int
	blocked []bool
}

// NewGrid creates an all-walkable W x H grid.
func NewGrid(w, h int) *Grid {
	return &Grid{W: w, H: h, blocked: make([]bool, w*h)}
}

// ID returns the linear cell id.
func (g *Grid) ID(x, y int) int { return y*g.W + x }

// CellOf converts a linear id back to a coordinate.
func (g *Grid) CellOf(id int) Cell {
	return Cell{X: id % g.W, Y: id / g.W}
}

// InBounds reports whether the coordinate is inside the map.
func (g *Grid) InBounds(x, y int) bool {
	return x >= 0 && y >= 0 && x < g.W && y < g.H
}

// Blocked reports whether the coordinate is blocked.
func (g *Grid) Blocked(x, y int) bool {
	return g.blocked[g.ID(x, y)]
}

// Walkable reports whether the cell id is walkable.
func (g *Grid) Walkable(id int) bool {
	return !g.blocked[id]
}

// neighbors appends every legal 8-directional move from id.
// Diagonal moves that cut a blocked corner are rejected.
func (g *Grid) neighbors(id int, out []int) []int {
	c := g.CellOf(id)
	for _, d := range allDirs {
		nx, ny := c.X+d[0], c.Y+d[1]
		if !g.InBounds(nx, ny) || g.blocked[g.ID(nx, ny)] {
			continue
		}
		if d[0] != 0 && d[1] != 0 {
			if g.blocked[g.ID(c.X+d[0], c.Y)] || g.blocked[g.ID(c.X, c.Y+d[1])] {
				continue
			}
		}
		out = append(out, g.ID(nx, ny))
	}
	return out
}

// moveCost returns the cost of one legal step between adjacent cells.
func moveCost(a, b Cell) float64 {
	if a.X != b.X && a.Y != b.Y {
		return math.Sqrt2
	}
	return 1
}

// octile is the consistent admissible heuristic for 8-directional movement.
func octile(a, b Cell) float64 {
	dx := a.X - b.X
	if dx < 0 {
		dx = -dx
	}
	dy := a.Y - b.Y
	if dy < 0 {
		dy = -dy
	}
	lo, hi := dx, dy
	if lo > hi {
		lo, hi = hi, lo
	}
	return float64(hi-lo) + math.Sqrt2*float64(lo)
}
