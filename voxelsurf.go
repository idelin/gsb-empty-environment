// Package voxelsurf computes surface areas of a set of unit voxels given by
// integer coordinates. It reports the total exposed surface area, the outer
// surface area (only faces touching the outside air; faces around sealed
// cavities do not count), the volume of every sealed cavity, and per-face
// classification evidence.
//
// Duplicate input coordinates are counted once. The number of voxels is
// expected to be <= 10000, but the coordinate span may be huge: the
// implementation never allocates the bounding box, not even a
// coordinate-compressed one.
//
// Algorithm overview:
//
//  1. Coordinates of each axis are compressed into "planes": one plane per
//     distinct voxel coordinate, one extra plane for every gap of size >= 1
//     between consecutive voxel coordinates, and two boundary planes.
//     Face-adjacency of empty space in the compressed model is equivalent to
//     the real one.
//  2. Gap planes and boundary planes are slabs extending to infinity, so they
//     always belong to the outside air. Hence every cavity cell sits on a
//     voxel coordinate of all three axes and represents exactly one unit
//     cell, so cavity volume is a plain cell count.
//  3. A cell of a sealed cavity must be "ray-blocked": each of the six
//     axis-aligned rays from the cell hits a solid voxel (otherwise the ray
//     is an empty corridor to infinity and the cell is outside). The flood
//     fill therefore only needs to run over ray-blocked cells, seeded from
//     the <= 6n empty neighbors of solid voxels. If such a flood reaches a
//     cell that is not ray-blocked, the whole component is outside air;
//     if it is exhausted, it is one sealed cavity.
//  4. By the Loomis-Whitney inequality the number of ray-blocked empty cells
//     is at most n^(3/2) (their three orthogonal projections are contained in
//     the projections of the solid set), so the flood fill visits at most
//     ~1e6 cells for n = 10000 regardless of the coordinate span.
//
// Total running time is O(n log n + K log n) where K <= n^(3/2), and memory
// is O(n + K).
package voxelsurf

import (
	"fmt"
	"sort"
	"strings"
)

// Vec3 is an integer coordinate. Ranges may be arbitrarily large.
type Vec3 struct{ X, Y, Z int64 }

// Dir is a face normal direction.
type Dir int

const (
	DirPX Dir = iota // +X
	DirNX            // -X
	DirPY            // +Y
	DirNY            // -Y
	DirPZ            // +Z
	DirNZ            // -Z
)

var dirDelta = [6]Vec3{
	{1, 0, 0}, {-1, 0, 0},
	{0, 1, 0}, {0, -1, 0},
	{0, 0, 1}, {0, 0, -1},
}

// Delta returns the unit step of the direction.
func (d Dir) Delta() Vec3 { return dirDelta[d] }

func (d Dir) String() string {
	return [6]string{"+X", "-X", "+Y", "-Y", "+Z", "-Z"}[d]
}

// FaceClass tells whether an exposed face touches the outside air or a
// sealed cavity.
type FaceClass int

const (
	// FaceOuter touches the outside air.
	FaceOuter FaceClass = iota
	// FaceCavity touches a sealed cavity.
	FaceCavity
)

func (c FaceClass) String() string {
	if c == FaceOuter {
		return "outer"
	}
	return "cavity"
}

// Face is one exposed unit square of the voxel set surface.
type Face struct {
	ID     int       // deterministic face number (evidence)
	Voxel  int       // index into Result.Voxels
	Dir    Dir       // outward normal
	Class  FaceClass // outer air or sealed cavity
	Cavity int       // cavity ID for FaceCavity, -1 for FaceOuter
}

// Cavity is one sealed air pocket.
type Cavity struct {
	ID     int
	Volume int64 // number of unit air cells
	Faces  []int // IDs of the faces enclosing it
}

// Result is the outcome of Analyze / AnalyzeBrute.
type Result struct {
	Voxels            []Vec3 // deduplicated, sorted
	DuplicatesIgnored int
	TotalSurface      int64 // all exposed faces
	OuterSurface      int64 // faces touching outside air
	InnerSurface      int64 // faces touching sealed cavities
	CavityVolume      int64 // sum of all cavity volumes
	Cavities          []Cavity
	Faces             []Face // per-face evidence, ID == slice index
}

// cell is a coordinate in the compressed plane model.
type cell [3]int

// axis compresses one coordinate axis into planes: boundary plane, then per
// distinct voxel coordinate a voxel plane, plus a gap plane wherever two
// consecutive voxel coordinates differ by more than one.
type axis struct {
	coords     []int64 // sorted distinct voxel coordinates
	voxelPlane []int   // voxelPlane[i] = plane index of coords[i]
	gapPlane   []int   // gapPlane[i] = plane between coords[i] and coords[i+1], -1 if adjacent
	loPlane    int     // plane for everything below coords[0]
	hiPlane    int     // plane for everything above coords[last]
}

func buildAxis(coords []int64) axis {
	a := axis{
		coords:     coords,
		voxelPlane: make([]int, len(coords)),
		gapPlane:   make([]int, len(coords)),
	}
	p := 0
	a.loPlane = p
	p++
	for i := range coords {
		a.voxelPlane[i] = p
		p++
		a.gapPlane[i] = -1
		if i+1 < len(coords) && coords[i+1]-coords[i] > 1 {
			a.gapPlane[i] = p
			p++
		}
	}
	a.hiPlane = p
	return a
}

// planeIndex maps a real coordinate to its compressed plane index.
func (a *axis) planeIndex(c int64) int {
	i := sort.Search(len(a.coords), func(i int) bool { return a.coords[i] >= c })
	if i < len(a.coords) && a.coords[i] == c {
		return a.voxelPlane[i]
	}
	if i == 0 {
		return a.loPlane
	}
	if i == len(a.coords) {
		return a.hiPlane
	}
	return a.gapPlane[i-1] // coords[i-1] < c < coords[i], so the gap plane exists
}

func dedup(input []Vec3) []Vec3 {
	set := make(map[Vec3]struct{}, len(input))
	for _, v := range input {
		set[v] = struct{}{}
	}
	vs := make([]Vec3, 0, len(set))
	for v := range set {
		vs = append(vs, v)
	}
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].X != vs[j].X {
			return vs[i].X < vs[j].X
		}
		if vs[i].Y != vs[j].Y {
			return vs[i].Y < vs[j].Y
		}
		return vs[i].Z < vs[j].Z
	})
	return vs
}

var cellOffsets = [6]cell{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}}

// Analyze computes surface areas and cavities of the given voxel set using
// the coordinate-compression flood fill. It never allocates the bounding
// box and is safe for arbitrarily large coordinate spans.
func Analyze(input []Vec3) Result {
	vs := dedup(input)
	res := Result{Voxels: vs, DuplicatesIgnored: len(input) - len(vs)}
	if len(vs) == 0 {
		return res
	}
	solid := make(map[Vec3]struct{}, len(vs))
	for _, v := range vs {
		solid[v] = struct{}{}
	}

	xs := make([]int64, 0, len(vs))
	ys := make([]int64, 0, len(vs))
	zs := make([]int64, 0, len(vs))
	{
		sx := make(map[int64]struct{}, len(vs))
		sy := make(map[int64]struct{}, len(vs))
		sz := make(map[int64]struct{}, len(vs))
		for _, v := range vs {
			if _, ok := sx[v.X]; !ok {
				sx[v.X] = struct{}{}
				xs = append(xs, v.X)
			}
			if _, ok := sy[v.Y]; !ok {
				sy[v.Y] = struct{}{}
				ys = append(ys, v.Y)
			}
			if _, ok := sz[v.Z]; !ok {
				sz[v.Z] = struct{}{}
				zs = append(zs, v.Z)
			}
		}
		sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
		sort.Slice(ys, func(i, j int) bool { return ys[i] < ys[j] })
		sort.Slice(zs, func(i, j int) bool { return zs[i] < zs[j] })
	}
	ax := buildAxis(xs)
	ay := buildAxis(ys)
	az := buildAxis(zs)

	// Compressed solid cells and per-row sorted solid indices, used for
	// O(1) ray-blocked tests (a ray is blocked iff the row has solids on
	// both sides, i.e. min < v && v < max of the sorted row).
	solidC := make(map[cell]struct{}, len(vs))
	compOf := make([]cell, len(vs))
	rowX := make(map[[2]int][]int) // key (y,z) -> solid x planes
	rowY := make(map[[2]int][]int) // key (x,z) -> solid y planes
	rowZ := make(map[[2]int][]int) // key (x,y) -> solid z planes
	for i, v := range vs {
		c := cell{ax.planeIndex(v.X), ay.planeIndex(v.Y), az.planeIndex(v.Z)}
		compOf[i] = c
		solidC[c] = struct{}{}
		rowX[[2]int{c[1], c[2]}] = append(rowX[[2]int{c[1], c[2]}], c[0])
		rowY[[2]int{c[0], c[2]}] = append(rowY[[2]int{c[0], c[2]}], c[1])
		rowZ[[2]int{c[0], c[1]}] = append(rowZ[[2]int{c[0], c[1]}], c[2])
	}
	for _, r := range rowX {
		sort.Ints(r)
	}
	for _, r := range rowY {
		sort.Ints(r)
	}
	for _, r := range rowZ {
		sort.Ints(r)
	}

	blocked := func(row []int, v int) bool {
		return len(row) > 0 && row[0] < v && v < row[len(row)-1]
	}
	rbCache := make(map[cell]bool)
	rayBlocked := func(c cell) bool {
		if b, ok := rbCache[c]; ok {
			return b
		}
		b := blocked(rowX[[2]int{c[1], c[2]}], c[0]) &&
			blocked(rowY[[2]int{c[0], c[2]}], c[1]) &&
			blocked(rowZ[[2]int{c[0], c[1]}], c[2])
		rbCache[c] = b
		return b
	}

	// Flood fill over ray-blocked cells, seeded from empty neighbors of
	// solid voxels. A component that reaches a non-ray-blocked cell is
	// outside air; an exhausted component is a sealed cavity.
	cavityOf := make(map[cell]int)
	knownOutside := make(map[cell]struct{})
	var cavities []Cavity
	for _, sc := range compOf { // deterministic: sorted voxel order
		for _, off := range cellOffsets {
			seed := cell{sc[0] + off[0], sc[1] + off[1], sc[2] + off[2]}
			if _, ok := solidC[seed]; ok {
				continue
			}
			if _, ok := cavityOf[seed]; ok {
				continue
			}
			if _, ok := knownOutside[seed]; ok {
				continue
			}
			if !rayBlocked(seed) {
				continue // free ray to infinity: outside air
			}
			seen := map[cell]struct{}{seed: {}}
			stack := []cell{seed}
			escaped := false
			for len(stack) > 0 {
				cur := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				for _, o := range cellOffsets {
					d := cell{cur[0] + o[0], cur[1] + o[1], cur[2] + o[2]}
					if _, ok := solidC[d]; ok {
						continue
					}
					if _, ok := knownOutside[d]; ok {
						escaped = true
						continue
					}
					if _, ok := cavityOf[d]; ok {
						continue // unreachable: cavity cells are fully enclosed
					}
					if _, ok := seen[d]; ok {
						continue
					}
					if !rayBlocked(d) {
						escaped = true // d has a free ray to infinity
						continue
					}
					seen[d] = struct{}{}
					stack = append(stack, d)
				}
			}
			if escaped {
				for c := range seen {
					knownOutside[c] = struct{}{}
				}
			} else {
				id := len(cavities)
				for c := range seen {
					cavityOf[c] = id
				}
				cavities = append(cavities, Cavity{ID: id, Volume: int64(len(seen))})
			}
		}
	}

	// Enumerate exposed faces and classify each one.
	faces := make([]Face, 0, len(vs)*2)
	var outer, inner int64
	for i, v := range vs {
		cc := compOf[i]
		for di, delta := range dirDelta {
			nb := Vec3{v.X + delta.X, v.Y + delta.Y, v.Z + delta.Z}
			if _, ok := solid[nb]; ok {
				continue // not exposed
			}
			// The compressed neighbor of an exposed face is never solid:
			// if the adjacent plane held a voxel it would be nb itself.
			nc := cell{cc[0] + cellOffsets[di][0], cc[1] + cellOffsets[di][1], cc[2] + cellOffsets[di][2]}
			f := Face{ID: len(faces), Voxel: i, Dir: Dir(di), Cavity: -1}
			if id, ok := cavityOf[nc]; ok {
				f.Class = FaceCavity
				f.Cavity = id
				cavities[id].Faces = append(cavities[id].Faces, f.ID)
				inner++
			} else {
				outer++
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

// Summary returns a short human-readable report of areas and cavities.
func (r *Result) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "voxels: %d unique (%d duplicates ignored)\n", len(r.Voxels), r.DuplicatesIgnored)
	fmt.Fprintf(&b, "total surface: %d\n", r.TotalSurface)
	fmt.Fprintf(&b, "outer surface: %d\n", r.OuterSurface)
	fmt.Fprintf(&b, "inner (cavity) surface: %d\n", r.InnerSurface)
	fmt.Fprintf(&b, "cavities: %d (total volume %d)\n", len(r.Cavities), r.CavityVolume)
	for _, c := range r.Cavities {
		fmt.Fprintf(&b, "  cavity #%d: volume %d, %d enclosing faces\n", c.ID, c.Volume, len(c.Faces))
	}
	return b.String()
}

// Evidence returns the full per-face classification table (face numbering
// evidence), one line per exposed face.
func (r *Result) Evidence() string {
	var b strings.Builder
	for _, f := range r.Faces {
		v := r.Voxels[f.Voxel]
		if f.Class == FaceOuter {
			fmt.Fprintf(&b, "face #%d voxel (%d,%d,%d) dir %s outer\n", f.ID, v.X, v.Y, v.Z, f.Dir)
		} else {
			fmt.Fprintf(&b, "face #%d voxel (%d,%d,%d) dir %s cavity #%d\n", f.ID, v.X, v.Y, v.Z, f.Dir, f.Cavity)
		}
	}
	return b.String()
}
