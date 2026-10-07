package voxelsurf

import "fmt"

// maxBruteCells bounds the padded bounding box that AnalyzeBrute may
// allocate. It is a reference model for validation on small coordinate
// spans, not for production use.
const maxBruteCells = 1 << 28

// AnalyzeBrute is the independent cell-by-cell reference model: it
// materializes the whole (padded) bounding box, flood-fills the outside air
// cell by cell, and classifies every face directly. Use it to cross-check
// Analyze on inputs whose bounding box is small; it panics when the padded
// bounding box exceeds maxBruteCells cells.
func AnalyzeBrute(input []Vec3) Result {
	vs := dedup(input)
	res := Result{Voxels: vs, DuplicatesIgnored: len(input) - len(vs)}
	if len(vs) == 0 {
		return res
	}

	minX, minY, minZ := vs[0].X, vs[0].Y, vs[0].Z
	maxX, maxY, maxZ := minX, minY, minZ
	for _, v := range vs[1:] {
		minX = min(minX, v.X)
		minY = min(minY, v.Y)
		minZ = min(minZ, v.Z)
		maxX = max(maxX, v.X)
		maxY = max(maxY, v.Y)
		maxZ = max(maxZ, v.Z)
	}
	// Padded box: one layer of air around the voxel bounding box.
	dx := maxX - minX + 3
	dy := maxY - minY + 3
	dz := maxZ - minZ + 3
	if dx*dy*dz > maxBruteCells {
		panic(fmt.Sprintf("voxelsurf: AnalyzeBrute bounding box %dx%dx%d too large", dx, dy, dz))
	}
	index := func(x, y, z int64) int64 {
		return (x-minX+1)*dy*dz + (y-minY+1)*dz + (z - minZ + 1)
	}
	cells := dx * dy * dz
	solid := make([]bool, cells)
	for _, v := range vs {
		solid[index(v.X, v.Y, v.Z)] = true
	}

	outside := make([]bool, cells)
	queue := [][3]int64{{minX - 1, minY - 1, minZ - 1}}
	outside[0] = true
	for len(queue) > 0 {
		c := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, d := range dirDelta {
			nx, ny, nz := c[0]+d.X, c[1]+d.Y, c[2]+d.Z
			if nx < minX-1 || nx > maxX+1 || ny < minY-1 || ny > maxY+1 || nz < minZ-1 || nz > maxZ+1 {
				continue
			}
			i := index(nx, ny, nz)
			if solid[i] || outside[i] {
				continue
			}
			outside[i] = true
			queue = append(queue, [3]int64{nx, ny, nz})
		}
	}

	// Every empty cell not reached by the outside flood belongs to a
	// sealed cavity; group them into connected components.
	cellCavity := make([]int, cells)
	for i := range cellCavity {
		cellCavity[i] = -1
	}
	var cavities []Cavity
	for x := minX - 1; x <= maxX+1; x++ {
		for y := minY - 1; y <= maxY+1; y++ {
			for z := minZ - 1; z <= maxZ+1; z++ {
				i := index(x, y, z)
				if solid[i] || outside[i] || cellCavity[i] >= 0 {
					continue
				}
				id := len(cavities)
				var volume int64
				stack := [][3]int64{{x, y, z}}
				cellCavity[i] = id
				for len(stack) > 0 {
					c := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					volume++
					for _, d := range dirDelta {
						nx, ny, nz := c[0]+d.X, c[1]+d.Y, c[2]+d.Z
						if nx < minX-1 || nx > maxX+1 || ny < minY-1 || ny > maxY+1 || nz < minZ-1 || nz > maxZ+1 {
							continue
						}
						j := index(nx, ny, nz)
						if solid[j] || outside[j] || cellCavity[j] >= 0 {
							continue
						}
						cellCavity[j] = id
						stack = append(stack, [3]int64{nx, ny, nz})
					}
				}
				cavities = append(cavities, Cavity{ID: id, Volume: volume})
			}
		}
	}

	faces := make([]Face, 0, len(vs)*2)
	var outer, inner int64
	for i, v := range vs {
		for di, d := range dirDelta {
			nx, ny, nz := v.X+d.X, v.Y+d.Y, v.Z+d.Z
			j := index(nx, ny, nz)
			if solid[j] {
				continue
			}
			f := Face{ID: len(faces), Voxel: i, Dir: Dir(di), Cavity: -1}
			if outside[j] {
				outer++
			} else {
				id := cellCavity[j]
				f.Class = FaceCavity
				f.Cavity = id
				cavities[id].Faces = append(cavities[id].Faces, f.ID)
				inner++
			}
			faces = append(faces, f)
		}
	}

	res.Faces = faces
	res.TotalSurface = int64(len(faces))
	res.OuterSurface = outer
	res.InnerSurface = inner
	res.Cavities = cavities
	for _, c := range cavities {
		res.CavityVolume += c.Volume
	}
	return res
}
