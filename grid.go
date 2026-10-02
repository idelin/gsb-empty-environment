package hexgrid

// GridDisk returns all cells within k rings of the given cell, including the
// cell itself. For a hexagonal region the result has exactly
// 1 + 3*k*(k+1) cells. The traversal is purely topological: it walks the
// integer ijk lattice and never calls a trigonometric function.
func GridDisk(center Cell, k int) []Cell {
	n := 1 + 3*k*(k+1)
	buf := make([]Cell, n)
	m := GridDiskInto(center, k, buf)
	return buf[:m]
}

// GridDiskInto is the zero-allocation variant of GridDisk: results are
// written into buf (which must have room for 1+3*k*(k+1) cells) and the
// number of cells written is returned. When the disk does not cross an
// icosahedron face boundary the entire walk runs on the integer fast path;
// otherwise boundary cells are re-projected onto the neighboring face using
// precomputed rotation bases (still no trigonometry) and duplicates are
// removed with an in-place scan of buf.
func GridDiskInto(center Cell, k int, buf []Cell) int {
	if k < 0 {
		return 0
	}
	if k == 0 {
		buf[0] = center
		return 1
	}
	face, res, origin := decodeCell(center)
	fast := ijkDistance(origin, ijk{})+k <= safeRadius[res]

	n := 0
	buf[n] = center
	n++

	pos := origin
	for ring := 1; ring <= k; ring++ {
		// Step out to the current ring along the +I axis.
		pos = ijkNeighbor(pos, 4)
		for d := 0; d < 6; d++ {
			for s := 0; s < ring; s++ {
				pos = normalizeIJK(ijkAdd(pos, ringWalkDirs[d]))
				if fast {
					buf[n] = encodeIJK(face, res, pos)
					n++
				} else {
					f2, coord := localize(face, res, pos)
					c := encodeIJK(f2, res, coord)
					if !containsCell(buf[:n], c) {
						buf[n] = c
						n++
					}
				}
			}
		}
	}
	return n
}

// GridRing returns only the cells exactly k rings away from center
// (6*k cells for hexagonal regions, or the center itself for k == 0).
func GridRing(center Cell, k int) []Cell {
	if k == 0 {
		return []Cell{center}
	}
	buf := make([]Cell, 6*k)
	n := GridRingInto(center, k, buf)
	return buf[:n]
}

// GridRingInto is the zero-allocation variant of GridRing.
func GridRingInto(center Cell, k int, buf []Cell) int {
	if k <= 0 {
		buf[0] = center
		return 1
	}
	face, res, origin := decodeCell(center)
	fast := ijkDistance(origin, ijk{})+k <= safeRadius[res]
	pos := origin
	for i := 0; i < k; i++ {
		pos = ijkNeighbor(pos, 4)
	}
	n := 0
	for d := 0; d < 6; d++ {
		for s := 0; s < k; s++ {
			pos = normalizeIJK(ijkAdd(pos, ringWalkDirs[d]))
			if fast {
				buf[n] = encodeIJK(face, res, pos)
				n++
			} else {
				f2, coord := localize(face, res, pos)
				c := encodeIJK(f2, res, coord)
				if !containsCell(buf[:n], c) {
					buf[n] = c
					n++
				}
			}
		}
	}
	return n
}

// localize maps an ijk coordinate to the face that owns its center under
// the Voronoi rule (nearest face center wins), re-quantizing on that face
// when ownership changes. It uses only precomputed basis vectors and dot
// products: ijk -> 2D plane point -> 3D ray -> best face -> 2D -> ijk.
// No trigonometric functions are called.
//
// Near face seams the two adjacent grids can disagree about ownership (each
// face's quantization claims the cell for the other face). The map
// T(face, ijk) = requantize(center on bestFace(center)) is therefore
// iterated to a fixed point or a cycle; cycles are broken deterministically
// by choosing the member with the smallest face index. This makes localize
// idempotent, so encode(LatLng(cell)) == cell holds everywhere.
func localize(face, res int, coord ijk) (int, ijk) {
	var vf [17]int
	var vc [17]ijk
	visited := 0
	for iter := 0; iter < 16; iter++ {
		for i := 0; i < visited; i++ {
			if vf[i] == face && vc[i] == coord {
				// Cycle detected: entries i..visited-1 form the cycle.
				best := i
				for j := i + 1; j < visited; j++ {
					if vf[j] < vf[best] {
						best = j
					}
				}
				return vf[best], vc[best]
			}
		}
		vf[visited] = face
		vc[visited] = coord
		visited++
		x, y := ijkToHex2d(coord)
		x *= scaleByRes[res]
		y *= scaleByRes[res]
		g := faces[face]
		dir := g.n.add(g.u.scale(x)).add(g.v.scale(y))
		f2 := bestFace(dir)
		if f2 == face {
			return face, coord
		}
		g2 := faces[f2]
		t := 1 / dir.dot(g2.n)
		x2 := t * dir.dot(g2.u) / scaleByRes[res]
		y2 := t * dir.dot(g2.v) / scaleByRes[res]
		face, coord = f2, hex2dToIJK(x2, y2)
	}
	return face, coord
}

func containsCell(haystack []Cell, needle Cell) bool {
	for _, c := range haystack {
		if c == needle {
			return true
		}
	}
	return false
}
