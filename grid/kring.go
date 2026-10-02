package grid

import "fmt"

// KRingSize returns the number of cells in a k-ring: 1 + 3*k*(k+1).
func KRingSize(k int) int { return 1 + 3*k*(k+1) }

// KRing returns all cells within hex-grid distance k of center, including
// center itself. The traversal is purely topological: the center cell is
// decoded to its integer lattice coordinate, cube-coordinate ring offsets
// are added, and each neighbor is re-encoded to a cell index. No floating
// point or trigonometric computation is performed.
//
// It returns ErrFaceCrossing if the neighborhood would leave the res-0
// coverage of the center's icosahedron face.
func KRing(center Cell, k int) ([]Cell, error) {
	if k < 0 {
		return nil, fmt.Errorf("grid: negative ring radius %d", k)
	}
	res := center.Res()
	face := center.Face()
	cij := center.IJ()
	out := make([]Cell, 0, KRingSize(k))
	for di := -k; di <= k; di++ {
		lo := -k
		if -di-k > lo {
			lo = -di - k
		}
		hi := k
		if -di+k < hi {
			hi = -di + k
		}
		for dj := lo; dj <= hi; dj++ {
			cell, err := cellFromIJ(face, res, cij.Add(IJ{di, dj}))
			if err != nil {
				return nil, err
			}
			out = append(out, cell)
		}
	}
	return out, nil
}

// Neighbors returns the six ring-1 neighbors of the cell.
func Neighbors(c Cell) ([6]Cell, error) {
	var out [6]Cell
	ij := c.IJ()
	for d := 1; d <= 6; d++ {
		n, err := cellFromIJ(c.Face(), c.Res(), ij.Add(digitOffsets[d]))
		if err != nil {
			return out, err
		}
		out[d-1] = n
	}
	return out, nil
}

// ValidateFaceLocal verifies that every cell's center is geographically
// owned by its own face (i.e. the cells form a geometrically consistent
// patch of the icosahedron unfolding). This is a geometric guard that is
// separate from the integer-only traversal itself.
func ValidateFaceLocal(cells []Cell) error {
	for _, c := range cells {
		ll := CellToLatLng(c)
		if f := faceOf(latLngToVec3(ll)); f != c.Face() {
			return fmt.Errorf("%w: cell %s center is owned by face %d, not %d",
				ErrFaceCrossing, c, f, c.Face())
		}
	}
	return nil
}
