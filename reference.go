package edtgrid

// ReferenceEDT is the all-pairs reference implementation. It keeps no
// incremental index of any kind: every pixel independently scans every
// other pixel and keeps the best opposite-valued one (squared distance,
// ties broken by row then column). Cost is O(N^2) for N = rows*cols, which
// makes it ideal as a ground truth for the faster engine on small grids.
func ReferenceEDT(rows, cols int, vals []uint8) []Cell {
	cells := make([]Cell, rows*cols)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			i := r*cols + c
			cells[i] = Cell{Value: vals[i], Dist: NoTargetDist}
			best := int64(-1)
			var bestSrc Point
			found := false
			for r2 := 0; r2 < rows; r2++ {
				for c2 := 0; c2 < cols; c2++ {
					j := r2*cols + c2
					if vals[j] == vals[i] {
						continue
					}
					d := sqDist(r, c, Point{Row: r2, Col: c2})
					cand := Point{Row: r2, Col: c2}
					if !found || d < best || (d == best && lessPoint(cand, bestSrc)) {
						found = true
						best = d
						bestSrc = cand
					}
				}
			}
			if found {
				cells[i].HasTarget = true
				cells[i].Dist = best
				cells[i].Source = bestSrc
			}
		}
	}
	return cells
}
