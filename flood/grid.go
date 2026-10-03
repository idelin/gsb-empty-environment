// Package flood implements a spatiotemporal 2D grid simulation kernel for
// dynamic flood spreading, connected-region extraction and road-blockage
// analysis over a digital elevation model (DEM).
package flood

// Grid is a row-major 2D raster of float64 values.
type Grid struct {
	W, H  int
	Cells []float64
}

// NewGrid returns a zero-initialised w by h grid.
func NewGrid(w, h int) *Grid {
	return &Grid{W: w, H: h, Cells: make([]float64, w*h)}
}

// InBounds reports whether (x, y) lies inside the grid.
func (g *Grid) InBounds(x, y int) bool {
	return x >= 0 && x < g.W && y >= 0 && y < g.H
}

// At returns the value at cell (x, y). Out-of-bounds reads return 0.
func (g *Grid) At(x, y int) float64 {
	if !g.InBounds(x, y) {
		return 0
	}
	return g.Cells[y*g.W+x]
}

// Set assigns v to cell (x, y). Out-of-bounds writes are ignored.
func (g *Grid) Set(x, y int, v float64) {
	if g.InBounds(x, y) {
		g.Cells[y*g.W+x] = v
	}
}

// Clone returns a deep copy of the grid.
func (g *Grid) Clone() *Grid {
	c := &Grid{W: g.W, H: g.H, Cells: make([]float64, len(g.Cells))}
	copy(c.Cells, g.Cells)
	return c
}

// Sum returns the total of all cell values.
func (g *Grid) Sum() float64 {
	var s float64
	for _, v := range g.Cells {
		s += v
	}
	return s
}
