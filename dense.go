package voxelsurf

// AnalyzeDense is the naive reference model: it allocates the whole padded
// bounding box and flood-fills outside air cell by cell. It is only suitable
// for small coordinate spans and exists to cross-check Analyze.
func AnalyzeDense(voxels []Point) Analysis {
	set := dedupePoints(voxels)
	n := len(set)
	if n == 0 {
		return Analysis{}
	}

	minX, minY, minZ := set[0].X, set[0].Y, set[0].Z
	maxX, maxY, maxZ := minX, minY, minZ
	for _, p := range set[1:] {
		minX, maxX = min64(minX, p.X), max64(maxX, p.X)
		minY, maxY = min64(minY, p.Y), max64(maxY, p.Y)
		minZ, maxZ = min64(minZ, p.Z), max64(maxZ, p.Z)
	}
	// One layer of padding guarantees the frame is outside air.
	x0, y0, z0 := minX-1, minY-1, minZ-1
	dx := int(maxX-minX) + 3
	dy := int(maxY-minY) + 3
	dz := int(maxZ-minZ) + 3
	size := dx * dy * dz

	at := func(x, y, z int) int { return (x*dy+y)*dz + z }

	solid := make([]bool, size)
	for _, p := range set {
		solid[at(int(p.X-x0), int(p.Y-y0), int(p.Z-z0))] = true
	}

	// Flood fill outside air from a padding corner.
	outside := make([]bool, size)
	queue := make([]int, 0, size)
	queue = append(queue, 0)
	outside[0] = true
	for len(queue) > 0 {
		i := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		x, rem := i/(dy*dz), i%(dy*dz)
		y, z := rem/dz, rem%dz
		for _, d := range dirs {
			nx, ny, nz := x+int(d.X), y+int(d.Y), z+int(d.Z)
			if nx < 0 || nx >= dx || ny < 0 || ny >= dy || nz < 0 || nz >= dz {
				continue
			}
			ni := at(nx, ny, nz)
			if solid[ni] || outside[ni] {
				continue
			}
			outside[ni] = true
			queue = append(queue, ni)
		}
	}

	// Cluster the remaining free cells into cavities.
	cavOf := make([]int, size)
	var cavVol []int
	for i := 0; i < size; i++ {
		if solid[i] || outside[i] || cavOf[i] != 0 {
			continue
		}
		id := len(cavVol) + 1
		vol := 0
		cq := []int{i}
		cavOf[i] = id
		for len(cq) > 0 {
			c := cq[len(cq)-1]
			cq = cq[:len(cq)-1]
			vol++
			x, rem := c/(dy*dz), c%(dy*dz)
			y, z := rem/dz, rem%dz
			for _, d := range dirs {
				nx, ny, nz := x+int(d.X), y+int(d.Y), z+int(d.Z)
				if nx < 0 || nx >= dx || ny < 0 || ny >= dy || nz < 0 || nz >= dz {
					continue
				}
				ni := at(nx, ny, nz)
				if solid[ni] || outside[ni] || cavOf[ni] != 0 {
					continue
				}
				cavOf[ni] = id
				cq = append(cq, ni)
			}
		}
		cavVol = append(cavVol, vol)
	}

	// Classify every exposed solid face.
	total := 0
	var extFaces []Face
	cavFaces := make([][]Face, len(cavVol))
	for _, p := range set {
		for d := 0; d < 6; d++ {
			q := Point{p.X + dirs[d].X, p.Y + dirs[d].Y, p.Z + dirs[d].Z}
			qi := at(int(q.X-x0), int(q.Y-y0), int(q.Z-z0))
			if solid[qi] {
				continue
			}
			total++
			f := faceBetween(p, d)
			if outside[qi] {
				extFaces = append(extFaces, f)
			} else {
				cavFaces[cavOf[qi]-1] = append(cavFaces[cavOf[qi]-1], f)
			}
		}
	}

	return buildAnalysis(n, total, extFaces, cavVol, cavFaces)
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
