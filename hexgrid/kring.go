package hexgrid

import "sort"

// axialDirs are the 6 unit steps of the axial hex lattice. Neighbor
// traversal is pure integer arithmetic on these directions; floating-point
// projection only happens when a step crosses an icosahedron face edge.
var axialDirs = [6][2]int{{1, 0}, {1, -1}, {0, -1}, {-1, 0}, {-1, 1}, {0, 1}}

// Neighbors returns the cells adjacent to c (6, or 5 next to an icosahedron
// vertex pentagon defect), in deterministic direction order.
func (c Cell) Neighbors() []Cell {
	faceIdx := c.Face()
	res := c.Resolution()
	q := c.Q()
	r := c.R()
	out := make([]Cell, 0, 6)
	seen := make(map[Cell]struct{}, 7)
	seen[c] = struct{}{}
	for _, d := range axialDirs {
		n := resolveCell(faceIdx, res, q+d[0], r+d[1])
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// bfsLayers computes exact topological (graph) distances from origin up to
// k rings. Because the disk is grown ring by ring through actual adjacency,
// coverage is contiguous and hole-free by construction, including across
// face edges and around pentagon defects.
func bfsLayers(origin Cell, k int) (dist map[Cell]int, order []Cell) {
	dist = map[Cell]int{origin: 0}
	order = []Cell{origin}
	frontier := []Cell{origin}
	for d := 1; d <= k; d++ {
		var next []Cell
		for _, cur := range frontier {
			for _, n := range cur.Neighbors() {
				if _, ok := dist[n]; ok {
					continue
				}
				dist[n] = d
				order = append(order, n)
				next = append(next, n)
			}
		}
		frontier = next
	}
	return dist, order
}

// KRingDisk returns the origin followed by all cells within topological
// distance k (the closed k-ring disk). Interior disks contain exactly
// 1 + 3*k*(k+1) cells.
func KRingDisk(origin Cell, k int) []Cell {
	if k < 0 {
		return nil
	}
	_, order := bfsLayers(origin, k)
	return order
}

// KRing returns the cells at topological distance exactly k from the
// origin, sorted for determinism. Interior rings contain exactly 6*k cells.
func KRing(origin Cell, k int) []Cell {
	if k < 0 {
		return nil
	}
	dist, _ := bfsLayers(origin, k)
	out := make([]Cell, 0, 6*k)
	for c, d := range dist {
		if d == k {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// GridDistance returns the topological distance between two cells, or -1
// if b is not reached within the search cap.
func GridDistance(a, b Cell) int {
	if a == b {
		return 0
	}
	dist := map[Cell]int{a: 0}
	frontier := []Cell{a}
	for d := 1; d <= 64; d++ {
		var next []Cell
		for _, cur := range frontier {
			for _, n := range cur.Neighbors() {
				if n == b {
					return d
				}
				if _, ok := dist[n]; !ok {
					dist[n] = d
					next = append(next, n)
				}
			}
		}
		frontier = next
	}
	return -1
}
