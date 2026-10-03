package hexgrid

// KRing returns all grid cells within topological distance k of the
// origin cell, including the origin itself (a filled hexagonal disk of
// 1 + 3k(k+1) cells when no icosahedron face boundary is crossed).
//
// The traversal is purely topological: it performs a breadth-first walk
// over integer cube coordinates using the 6 unit direction vectors, with
// no floating-point or trigonometric computation per step. Projection
// math is only involved when a neighbor crosses an icosahedron face
// boundary, where the cell is re-encoded through the spherical path.
func KRing(origin Cell, k int) []Cell {
	if k < 0 || !origin.IsValid() {
		return nil
	}
	face, res, center := cellToCube(origin)

	seen := map[cubeCoord]struct{}{center: {}}
	cells := []Cell{origin}
	frontier := []cubeCoord{center}

	for ring := 0; ring < k; ring++ {
		var next []cubeCoord
		for _, f := range frontier {
			for _, dir := range cubeDirections {
				n := f.add(dir)
				if _, ok := seen[n]; ok {
					continue
				}
				seen[n] = struct{}{}
				next = append(next, n)
				cells = append(cells, cubeToCell(face, res, n))
			}
		}
		frontier = next
	}
	return cells
}

// GridDistance returns the topological hex-grid distance between two
// cells. The second return value is false when the cells are not
// comparable (different resolutions, or different icosahedron faces
// where a purely topological distance is not defined by this package).
func GridDistance(a, b Cell) (int, bool) {
	if a.Resolution() != b.Resolution() || a.BaseCell() != b.BaseCell() {
		return 0, false
	}
	_, _, ca := cellToCube(a)
	_, _, cb := cellToCube(b)
	return cubeDistance(ca, cb), true
}
