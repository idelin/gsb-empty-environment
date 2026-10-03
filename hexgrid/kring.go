package hexgrid

import "errors"

// ErrDistanceOverflow is returned when a topological distance search
// exceeds the safety bound.
var ErrDistanceOverflow = errors.New("hexgrid: distance search overflow")

// GridDisk returns all cells within topological (ring) distance k of
// the cell, including the cell itself. The traversal is pure integer
// lattice arithmetic: offsets are applied in cell space, mapped to the
// face lattice, carried across icosahedron edges by exact integer
// transforms, and canonicalized. No floating point is involved.
//
// Away from pentagons the result has exactly 1+3k(k+1) cells. Near one
// of the 12 pentagon cells the missing sector collapses and fewer
// distinct cells are returned.
func (c Cell) GridDisk(k int) ([]Cell, error) {
	cells, _, err := c.GridDiskDistances(k)
	return cells, err
}

// GridDiskDistances is GridDisk plus the topological distance of each
// returned cell from the center (cells and distances are aligned by
// index).
func (c Cell) GridDiskDistances(k int) ([]Cell, []int, error) {
	if k < 0 {
		return nil, nil, ErrInvalidResolution
	}
	cc, err := decodeCell(c)
	if err != nil {
		return nil, nil, err
	}
	res := cc.res
	n := freqTable[res]
	origin := powM(res, cc.c)
	col1 := powM(res, vec2{1, 0})
	col2 := powM(res, vec2{0, 1})

	seen := make(map[Cell]struct{}, 1+3*k*(k+1))
	cells := make([]Cell, 0, 1+3*k*(k+1))
	dists := make([]int, 0, 1+3*k*(k+1))
	ki := int64(k)
	for di := -ki; di <= ki; di++ {
		lo, hi := -ki, ki
		if -ki-di > lo {
			lo = -ki - di
		}
		if ki-di < hi {
			hi = ki - di
		}
		for dj := lo; dj <= hi; dj++ {
			q := vec2{
				origin.i + di*col1.i + dj*col2.i,
				origin.j + di*col1.j + dj*col2.j,
			}
			w := baryFromIJ(q, n)
			f, w, err := normalize(cc.face, w)
			if err != nil {
				return nil, nil, err
			}
			f, w = canonicalize(f, w)
			cell, err := encodeCoord(f, divMPow(res, ijFromBary(w)), res)
			if err != nil {
				return nil, nil, err
			}
			if _, ok := seen[cell]; ok {
				continue
			}
			seen[cell] = struct{}{}
			cells = append(cells, cell)
			dists = append(dists, int(cubeDist(di, dj)))
		}
	}
	return cells, dists, nil
}

// GridRing returns the cells at exactly topological distance k from the
// cell. k = 0 returns the cell itself.
func (c Cell) GridRing(k int) ([]Cell, error) {
	if k < 0 {
		return nil, ErrInvalidResolution
	}
	if k == 0 {
		return []Cell{c}, nil
	}
	cells, dists, err := c.GridDiskDistances(k)
	if err != nil {
		return nil, err
	}
	ring := make([]Cell, 0, 6*k)
	for i, d := range dists {
		if d == k {
			ring = append(ring, cells[i])
		}
	}
	return ring, nil
}

// GridDistance returns the topological (ring) distance between two
// cells at the same resolution, computed by ring expansion from a.
func GridDistance(a, b Cell) (int, error) {
	if a.Resolution() != b.Resolution() {
		return 0, ErrInvalidResolution
	}
	if a == b {
		return 0, nil
	}
	const maxSearch = 4096
	visited := map[Cell]struct{}{a: {}}
	frontier := []Cell{a}
	for d := 1; d <= maxSearch; d++ {
		var next []Cell
		for _, cell := range frontier {
			disk, err := cell.GridDisk(1)
			if err != nil {
				return 0, err
			}
			for _, nb := range disk {
				if nb == b {
					return d, nil
				}
				if _, ok := visited[nb]; !ok {
					visited[nb] = struct{}{}
					next = append(next, nb)
				}
			}
		}
		frontier = next
	}
	return 0, ErrDistanceOverflow
}
