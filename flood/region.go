package flood

// Cell is an integer grid coordinate.
type Cell struct{ X, Y int }

// Point is a 2D coordinate in grid space (cell units).
type Point struct{ X, Y float64 }

// Region is one 8-connected flooded area together with its boundary
// polygon (ordered vertices in grid coordinates, closed: last == first
// for regions larger than one cell).
type Region struct {
	ID       int
	Cells    []Cell
	Boundary []Point
}

// Area returns the number of flooded cells in the region.
func (r Region) Area() int { return len(r.Cells) }

// RegionsAt extracts the 8-connected flooded regions (water depth >=
// BlockDepth) of the state after tick t, each with a boundary polygon
// traced via Moore-neighbour tracing.
func (s *Simulator) RegionsAt(t int) []Region {
	return ExtractRegions(s.DepthAt(t), s.Cfg.BlockDepth)
}

// ExtractRegions labels connected components of cells with value >=
// threshold and traces each component's boundary polygon.
func ExtractRegions(depth *Grid, threshold float64) []Region {
	w, h := depth.W, depth.H
	flooded := make([]bool, w*h)
	for i, v := range depth.Cells {
		flooded[i] = v >= threshold
	}
	labels := make([]int, w*h)
	for i := range labels {
		labels[i] = -1
	}
	var regions []Region
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			if !flooded[i] || labels[i] >= 0 {
				continue
			}
			id := len(regions)
			cells := floodFill(flooded, labels, w, h, x, y, id)
			regions = append(regions, Region{
				ID:       id,
				Cells:    cells,
				Boundary: traceBoundary(flooded, w, h, x, y),
			})
		}
	}
	return regions
}

// floodFill BFS-labels the 8-connected component containing (sx, sy).
func floodFill(flooded []bool, labels []int, w, h, sx, sy, id int) []Cell {
	var cells []Cell
	queue := []Cell{{sx, sy}}
	labels[sy*w+sx] = id
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		cells = append(cells, c)
		for k := 0; k < 8; k++ {
			nx, ny := c.X+nbDX[k], c.Y+nbDY[k]
			if nx < 0 || nx >= w || ny < 0 || ny >= h {
				continue
			}
			j := ny*w + nx
			if flooded[j] && labels[j] < 0 {
				labels[j] = id
				queue = append(queue, Cell{nx, ny})
			}
		}
	}
	return cells
}

// traceBoundary performs Moore-neighbour tracing (with Jacob's stopping
// criterion plus a visited-state guard for degenerate thin shapes) over a
// boolean mask, starting from cell (sx, sy) which is entered from the west.
// It returns the ordered boundary vertices in grid coordinates.
func traceBoundary(mask []bool, w, h, sx, sy int) []Point {
	fg := func(x, y int) bool {
		return x >= 0 && x < w && y >= 0 && y < h && mask[y*w+x]
	}
	// Clockwise offsets starting at west: W, NW, N, NE, E, SE, S, SW.
	dx := [8]int{-1, -1, 0, 1, 1, 1, 0, -1}
	dy := [8]int{0, -1, -1, -1, 0, 1, 1, 1}

	boundary := []Point{{X: float64(sx), Y: float64(sy)}}
	px, py := sx, sy
	btx, bty := sx-1, sy // entered from the west
	type state struct{ px, py, bx, by int }
	seen := map[state]bool{{px, py, btx, bty}: true}
	maxIter := 8*w*h + 16

	for iter := 0; iter < maxIter; iter++ {
		// Index of the backtrack direction relative to p.
		bi := 0
		for k := 0; k < 8; k++ {
			if px+dx[k] == btx && py+dy[k] == bty {
				bi = k
				break
			}
		}
		// Scan clockwise from the neighbour after the backtrack pixel.
		found := false
		var cx, cy, nbx, nby int
		prevX, prevY := btx, bty
		for i := 1; i <= 8; i++ {
			k := (bi + i) % 8
			nx, ny := px+dx[k], py+dy[k]
			if fg(nx, ny) {
				cx, cy = nx, ny
				nbx, nby = prevX, prevY // last background pixel examined
				found = true
				break
			}
			prevX, prevY = nx, ny
		}
		if !found {
			break // isolated single cell
		}
		px, py, btx, bty = cx, cy, nbx, nby
		if px == sx && py == sy && btx == sx-1 && bty == sy {
			break // Jacob's criterion: re-entered start from the west
		}
		boundary = append(boundary, Point{X: float64(px), Y: float64(py)})
		st := state{px, py, btx, bty}
		if seen[st] {
			break // degenerate (1-cell-wide) shape: state cycle
		}
		seen[st] = true
	}
	// Close the ring when possible.
	if n := len(boundary); n > 1 && (boundary[0] != boundary[n-1]) {
		boundary = append(boundary, boundary[0])
	}
	return boundary
}
