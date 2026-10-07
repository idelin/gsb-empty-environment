// Package voxelsurf computes surface areas of voxel sets given as integer
// lattice coordinates.
//
// Two areas are reported:
//   - TotalArea: every exposed unit face of the solid, including faces that
//     border enclosed air cavities.
//   - ExternalArea: only faces that touch air connected to the outside world;
//     faces bordering sealed cavities are excluded.
//
// The input may contain duplicate coordinates (counted once) and may span a
// huge coordinate range while holding few voxels (<= 10000). The implementation
// never allocates the full bounding box: it flood-fills outside air on a
// coordinate-compressed grid instead.
package voxelsurf

import (
	"fmt"
	"math"
	"sort"
)

// Point is an integer lattice coordinate of one voxel.
type Point struct{ X, Y, Z int64 }

// Face identifies one unit square face shared by two adjacent lattice cells.
// It is a deterministic, coordinate-based "face number" usable as evidence.
//
// The face is perpendicular to Axis (0=x, 1=y, 2=z), lies on the plane
// Plane along that axis, and U, V are the remaining two coordinates of the
// face (for Axis 0: U=y, V=z; for Axis 1: U=x, V=z; for Axis 2: U=x, V=y).
type Face struct {
	Axis  int
	Plane int64
	U, V  int64
}

// String renders the face as a compact, human-readable face number.
func (f Face) String() string {
	return fmt.Sprintf("face|%c|plane=%d|u=%d|v=%d", "XYZ"[f.Axis], f.Plane, f.U, f.V)
}

// Cavity is one connected enclosed air pocket together with the evidence of
// the solid faces that seal it.
type Cavity struct {
	Volume int    // number of enclosed unit lattice cells
	Faces  []Face // solid faces bordering this cavity, sorted
}

// Analysis is the result of examining a voxel set.
type Analysis struct {
	Voxels        int      // unique voxel count (duplicates removed)
	TotalArea     int      // all exposed faces, including cavity faces
	ExternalArea  int      // exposed faces touching outside air only
	CavityArea    int      // exposed faces bordering sealed cavities (TotalArea - ExternalArea)
	CavityVolume  int      // total number of enclosed air cells
	ExternalFaces []Face   // sorted evidence for ExternalArea
	Cavities      []Cavity // per-cavity volume and face evidence
}

// String renders a one-line summary.
func (a Analysis) String() string {
	return fmt.Sprintf("voxels=%d total=%d external=%d cavityArea=%d cavityVolume=%d cavities=%d",
		a.Voxels, a.TotalArea, a.ExternalArea, a.CavityArea, a.CavityVolume, len(a.Cavities))
}

var dirs = [6]Point{
	{1, 0, 0}, {-1, 0, 0},
	{0, 1, 0}, {0, -1, 0},
	{0, 0, 1}, {0, 0, -1},
}

// faceBetween returns the Face shared by solid voxel p and its neighbor in
// direction d (index into dirs).
func faceBetween(p Point, d int) Face {
	q := Point{p.X + dirs[d].X, p.Y + dirs[d].Y, p.Z + dirs[d].Z}
	switch d / 2 {
	case 0:
		return Face{Axis: 0, Plane: max64(p.X, q.X), U: p.Y, V: p.Z}
	case 1:
		return Face{Axis: 1, Plane: max64(p.Y, q.Y), U: p.X, V: p.Z}
	default:
		return Face{Axis: 2, Plane: max64(p.Z, q.Z), U: p.X, V: p.Y}
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func lessFace(a, b Face) bool {
	if a.Axis != b.Axis {
		return a.Axis < b.Axis
	}
	if a.Plane != b.Plane {
		return a.Plane < b.Plane
	}
	if a.U != b.U {
		return a.U < b.U
	}
	return a.V < b.V
}

func sortFaces(fs []Face) { sort.Slice(fs, func(i, j int) bool { return lessFace(fs[i], fs[j]) }) }

func sortCavities(cs []Cavity) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Volume != cs[j].Volume {
			return cs[i].Volume < cs[j].Volume
		}
		// First face is unique across cavities, so this is a total order.
		return lessFace(cs[i].Faces[0], cs[j].Faces[0])
	})
}

func dedupePoints(voxels []Point) []Point {
	seen := make(map[Point]struct{}, len(voxels))
	out := make([]Point, 0, len(voxels))
	for _, p := range voxels {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].X != out[j].X {
			return out[i].X < out[j].X
		}
		if out[i].Y != out[j].Y {
			return out[i].Y < out[j].Y
		}
		return out[i].Z < out[j].Z
	})
	return out
}

// buildAnalysis assembles the deterministic result from raw counts and faces.
func buildAnalysis(n, total int, extFaces []Face, cavVol []int, cavFaces [][]Face) Analysis {
	sortFaces(extFaces)
	cavities := make([]Cavity, len(cavVol))
	cavArea, cavVolSum := 0, 0
	for i := range cavities {
		sortFaces(cavFaces[i])
		cavities[i] = Cavity{Volume: cavVol[i], Faces: cavFaces[i]}
		cavArea += len(cavFaces[i])
		cavVolSum += cavVol[i]
	}
	sortCavities(cavities)
	return Analysis{
		Voxels:        n,
		TotalArea:     total,
		ExternalArea:  len(extFaces),
		CavityArea:    cavArea,
		CavityVolume:  cavVolSum,
		ExternalFaces: extFaces,
		Cavities:      cavities,
	}
}

// axis holds the coordinate compression for one dimension: the sorted unique
// voxel coordinates and a lookup from coordinate to compressed index.
type axis struct {
	coords []int64
	index  map[int64]int
}

func newAxis(values []int64) axis {
	coords := append([]int64(nil), values...)
	sort.Slice(coords, func(i, j int) bool { return coords[i] < coords[j] })
	uniq := coords[:0]
	for _, v := range coords {
		if len(uniq) == 0 || uniq[len(uniq)-1] != v {
			uniq = append(uniq, v)
		}
	}
	idx := make(map[int64]int, len(uniq))
	for i, v := range uniq {
		idx[v] = i
	}
	return axis{coords: uniq, index: idx}
}

// Analyze examines the voxel set with the coordinate-compression flood fill.
// It handles arbitrarily large coordinate spans without allocating the
// bounding box.
func Analyze(voxels []Point) Analysis {
	set := dedupePoints(voxels)
	n := len(set)
	if n == 0 {
		return Analysis{}
	}

	xs := make([]int64, n)
	ys := make([]int64, n)
	zs := make([]int64, n)
	solidOrig := make(map[Point]struct{}, n)
	for i, p := range set {
		xs[i], ys[i], zs[i] = p.X, p.Y, p.Z
		solidOrig[p] = struct{}{}
	}
	ax := [3]axis{newAxis(xs), newAxis(ys), newAxis(zs)}

	solid := make(map[[3]int]struct{}, n)
	for _, p := range set {
		solid[[3]int{ax[0].index[p.X], ax[1].index[p.Y], ax[2].index[p.Z]}] = struct{}{}
	}

	// Isoperimetric cap: any sealed cavity C satisfies
	// |boundary(C)| >= 6*|C|^(2/3) (Loomis-Whitney) and |boundary(C)| <= 6n,
	// hence |C| <= n^(3/2). A flood exceeding this limit cannot be a cavity.
	limit := int(float64(n)*math.Sqrt(float64(n))) + 1

	// state of a compressed all-coordinate free cell:
	// absent = unknown, -1 = outside air, -2 = being flooded, >=1 = cavity id.
	state := make(map[[3]int]int)

	flood := func(start [3]int) (cells [][3]int, outside bool) {
		queue := [][3]int{start}
		state[start] = -2
		for len(queue) > 0 {
			c := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			cells = append(cells, c)
			if len(cells) > limit {
				// Too large to be a sealed cavity. Drain the queue so no
				// provisional -2 marks are left behind in state.
				cells = append(cells, queue...)
				return cells, true
			}
			for a := 0; a < 3; a++ {
				for _, s := range [2]int{-1, 1} {
					ni := c[a] + s
					if ni < 0 || ni >= len(ax[a].coords) {
						outside = true // padding beyond the extreme coordinate
						continue
					}
					d := ax[a].coords[ni] - ax[a].coords[c[a]]
					if d != 1 && d != -1 {
						outside = true // gap slab: part of the outside skeleton
						continue
					}
					nc := c
					nc[a] = ni
					if _, ok := solid[nc]; ok {
						continue
					}
					switch state[nc] {
					case -2:
						// already in this flood
					case -1:
						outside = true
					case 0:
						state[nc] = -2
						queue = append(queue, nc)
					}
				}
			}
		}
		return cells, outside
	}

	total := 0
	var extFaces []Face
	var cavVol []int
	var cavFaces [][]Face

	for _, p := range set {
		for d := 0; d < 6; d++ {
			q := Point{p.X + dirs[d].X, p.Y + dirs[d].Y, p.Z + dirs[d].Z}
			if _, ok := solidOrig[q]; ok {
				continue
			}
			total++
			f := faceBetween(p, d)

			ix, okx := ax[0].index[q.X]
			iy, oky := ax[1].index[q.Y]
			iz, okz := ax[2].index[q.Z]
			if !okx || !oky || !okz {
				// The neighbor cell lies in a fully empty plane; such gap
				// slabs all interconnect into the outside air skeleton.
				extFaces = append(extFaces, f)
				continue
			}
			c := [3]int{ix, iy, iz}
			st, known := state[c]
			if !known {
				cells, out := flood(c)
				if out {
					for _, cc := range cells {
						state[cc] = -1
					}
					st = -1
				} else {
					id := len(cavVol) + 1
					for _, cc := range cells {
						state[cc] = id
					}
					cavVol = append(cavVol, len(cells))
					cavFaces = append(cavFaces, nil)
					st = id
				}
			}
			if st == -1 {
				extFaces = append(extFaces, f)
			} else {
				cavFaces[st-1] = append(cavFaces[st-1], f)
			}
		}
	}

	return buildAnalysis(n, total, extFaces, cavVol, cavFaces)
}
