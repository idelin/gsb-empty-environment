package hexgrid

import (
	"math"
	"sort"
)

// axialDirs are the 6 neighbor offsets of a pointy-top axial hex grid.
var axialDirs = [6][2]int{
	{1, 0}, {1, -1}, {0, -1}, {-1, 0}, {-1, 1}, {0, 1},
}

// revScanRadius is the axial box radius used by the reverse-closure
// scan when symmetrizing cross-face adjacency.
const revScanRadius = 5

// neighborCell resolves the axial neighbor (i, j) of a position on
// face f at resolution res to a canonical cell. Inside the grid this
// is pure integer arithmetic. When the position leaves the face grid,
// the face is crossed by unprojecting the position to the sphere and
// re-quantizing it on the owning face; this uses vector algebra only
// and involves no floating-point trigonometry.
func neighborCell(f, res, i, j int) Cell {
	n := 1 << uint(res)
	if i >= 0 && i < n && j >= 0 && j < n {
		return newCell(f, res, i, j)
	}
	return cellAtSpherePoint(cellCenterSphere(f, res, i, j), res)
}

// Neighbors returns the topological neighbors of the cell at its own
// resolution, deduplicated and sorted. The cell itself is never
// included.
//
// The relation is symmetric by construction: in addition to the 6
// forward axial neighbors (with trig-free spherical reprojection
// across face boundaries), cells near a face seam gain the reverse
// closure — any cell on an adjacent face whose own forward neighbor
// computation lands on c is included, so b in Neighbors(a) always
// implies a in Neighbors(b).
func Neighbors(c Cell) []Cell {
	f, res := c.Face(), c.Resolution()
	i, j := c.IJ()
	seen := make(map[Cell]struct{}, 12)
	out := make([]Cell, 0, 12)
	add := func(nc Cell) {
		if nc == c {
			return
		}
		if _, dup := seen[nc]; dup {
			return
		}
		seen[nc] = struct{}{}
		out = append(out, nc)
	}
	for _, d := range axialDirs {
		add(neighborCell(f, res, i+d[0], j+d[1]))
	}
	// Reverse closure is only needed near a face seam; interior
	// cells take the pure-integer fast path.
	if seamDistance(f, res, i, j) < 7*hexSize(f, res) {
		reverseNeighbors(c, add)
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

// reverseNeighbors adds every cell a on any other face such that
// c is among a's forward axial neighbors (i.e. a's off-grid neighbor
// position re-projects to c). This makes the neighbor relation
// symmetric across face seams. Off-grid positions near grid corners
// can quantize to faces that are not icosahedron-adjacent, so every
// face is considered; faces whose projection falls far outside the
// scan box are rejected cheaply.
func reverseNeighbors(c Cell, add func(Cell)) {
	f, res := c.Face(), c.Resolution()
	i, j := c.IJ()
	p := cellCenterSphere(f, res, i, j)
	n := 1 << uint(res)
	for g := 0; g < numFaces; g++ {
		if g == f {
			continue
		}
		px, py := projectSphereToFace(g, p)
		q, r := planeToAxial(px, py, hexSize(g, res))
		ci, cj := int(math.Round(q))+n/2, int(math.Round(r))+n/2
		if ci < -revScanRadius-1 || ci > n+revScanRadius+1 ||
			cj < -revScanRadius-1 || cj > n+revScanRadius+1 {
			continue
		}
		for ai := ci - revScanRadius; ai <= ci+revScanRadius; ai++ {
			if ai < 0 || ai >= n {
				continue
			}
			for aj := cj - revScanRadius; aj <= cj+revScanRadius; aj++ {
				if aj < 0 || aj >= n {
					continue
				}
				for _, d := range axialDirs {
					ni, nj := ai+d[0], aj+d[1]
					if ni >= 0 && ni < n && nj >= 0 && nj < n {
						continue // in-grid: not a crossing link
					}
					if cellAtSpherePoint(cellCenterSphere(g, res, ni, nj), res) == c {
						add(newCell(g, res, ai, aj))
						break
					}
				}
			}
		}
	}
}

// KRing returns every cell within topological distance k of c
// (including c itself), deduplicated and sorted. For a cell whose
// neighborhood does not touch a face seam the result has exactly
// 1 + 3*k*(k+1) cells and tiles a contiguous, closed hexagonal area
// with no holes.
//
// The traversal is a breadth-first walk on the symmetric neighbor
// graph; same-face steps are pure integer axial arithmetic and only
// face crossings involve (trig-free) spherical reprojection.
func KRing(c Cell, k int) []Cell {
	dist := KRingDistances(c, k)
	out := make([]Cell, 0, len(dist))
	for cell := range dist {
		out = append(out, cell)
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

// KRingDistances is KRing with the topological distance of every
// returned cell attached. The origin has distance 0.
func KRingDistances(c Cell, k int) map[Cell]int {
	if k < 0 {
		k = 0
	}
	dist := map[Cell]int{c: 0}
	frontier := []Cell{c}
	for step := 0; step < k; step++ {
		var next []Cell
		for _, cur := range frontier {
			for _, nc := range Neighbors(cur) {
				if _, seen := dist[nc]; seen {
					continue
				}
				dist[nc] = dist[cur] + 1
				next = append(next, nc)
			}
		}
		frontier = next
	}
	return dist
}
