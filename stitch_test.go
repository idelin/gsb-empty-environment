package taskpkg

import (
	"math"
	"reflect"
	"testing"
)

// boxCorner maps vertex IDs to unit-cube corners.
func boxCorner(v int) Vec3 {
	return [...]Vec3{
		{0, 0, 0}, {1, 0, 0}, {1, 1, 0}, {0, 1, 0},
		{0, 0, 1}, {1, 0, 1}, {1, 1, 1}, {0, 1, 1},
	}[v]
}

// boxPatches returns the six outward-oriented faces of the unit cube.
func boxPatches(levels []int) []Patch {
	return []Patch{
		{ID: 0, Corners: [4]int{0, 3, 2, 1}, Level: levels[0]}, // bottom
		{ID: 1, Corners: [4]int{4, 5, 6, 7}, Level: levels[1]}, // top
		{ID: 2, Corners: [4]int{0, 1, 5, 4}, Level: levels[2]}, // front
		{ID: 3, Corners: [4]int{3, 7, 6, 2}, Level: levels[3]}, // back
		{ID: 4, Corners: [4]int{0, 4, 7, 3}, Level: levels[4]}, // left
		{ID: 5, Corners: [4]int{1, 2, 6, 5}, Level: levels[5]}, // right
	}
}

func stitch(t *testing.T, topo *Topology, cornerPos func(int) Vec3) ([]PatchMesh, *Mesh) {
	t.Helper()
	pms := NewStitcher(topo, cornerPos, nil).Stitch()
	mesh, err := Merge(pms)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	return pms, mesh
}

// countSharedPoints counts point IDs produced by more than one patch.
func countSharedPoints(pms []PatchMesh) int {
	seen := map[PointID]int{}
	for _, pm := range pms {
		for id := range pm.Points {
			seen[id]++
		}
	}
	shared := 0
	for _, n := range seen {
		if n > 1 {
			shared++
		}
	}
	return shared
}

func TestTransitionTriangles(t *testing.T) {
	// Two adjacent quads, levels 0 and 1: the coarse side must insert a
	// midpoint on the shared edge and stitch with transition triangles.
	topo := mustTopology(t, []Patch{
		{ID: 0, Corners: [4]int{0, 1, 2, 3}, Level: 0},
		{ID: 1, Corners: [4]int{2, 1, 4, 5}, Level: 1},
	})
	flat := func(v int) Vec3 {
		return Vec3{float64(v % 3), float64(v / 3), 0}
	}
	pms, mesh := stitch(t, topo, flat)
	if err := mesh.Validate(false); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	tris, internal, boundary := mesh.Stats()
	if tris != 3+8 {
		t.Errorf("triangles = %d, want 11", tris)
	}
	if internal != 12 || boundary != 9 {
		t.Errorf("internal/boundary = %d/%d, want 12/9", internal, boundary)
	}
	// The shared-edge midpoint must exist with rational parameter 1/2.
	mid := PointID{Kind: EdgePoint, Edge: NewEdgeID(1, 2), Num: 1, Den: 2}
	pos, ok := mesh.Points[mid]
	if !ok {
		t.Fatalf("midpoint %+v not generated", mid)
	}
	want := flat(1).Add(flat(2)).Scale(0.5)
	if pos != want {
		t.Errorf("midpoint position = %v, want %v", pos, want)
	}
	if countSharedPoints(pms) != 3 {
		t.Errorf("shared points = %d, want 3 (two corners + midpoint)", countSharedPoints(pms))
	}
}

func TestBoxClosedMesh(t *testing.T) {
	topo := mustTopology(t, boxPatches([]int{0, 2, 1, 3, 0, 2}))
	pms, mesh := stitch(t, topo, boxCorner)
	if topo.MaxAdjacentLevelDiff() > 1 {
		t.Fatalf("level constraint violated after Stitch")
	}
	if err := mesh.Validate(true); err != nil {
		t.Fatalf("Validate(closed): %v", err)
	}
	tris, internal, boundary := mesh.Stats()
	if tris == 0 || internal == 0 || boundary != 0 {
		t.Fatalf("stats: tris=%d internal=%d boundary=%d", tris, internal, boundary)
	}
	if countSharedPoints(pms) == 0 {
		t.Fatal("no shared points across patches")
	}
	// Every triangle normal must point away from the box center.
	center := Vec3{0.5, 0.5, 0.5}
	for _, tri := range mesh.Triangles {
		n := mesh.Normal(tri)
		if n.Dot(mesh.Centroid(tri).Sub(center)) <= 0 {
			t.Fatalf("triangle %v normal points inward", tri)
		}
	}
}

// torus builds a U-by-V grid of outward-oriented quad patches on a torus.
func torus(U, V int, levels []int) ([]Patch, func(int) Vec3) {
	const R, r = 2.0, 0.75
	vid := func(i, j int) int { return (i%U)*V + j%V }
	patches := make([]Patch, 0, U*V)
	for i := 0; i < U; i++ {
		for j := 0; j < V; j++ {
			patches = append(patches, Patch{
				ID: i*V + j,
				Corners: [4]int{
					vid(i, j), vid(i+1, j), vid(i+1, j+1), vid(i, j+1),
				},
				Level: levels[i*V+j],
			})
		}
	}
	pos := func(v int) Vec3 {
		i, j := v/V, v%V
		u := 2 * math.Pi * float64(i) / float64(U)
		w := 2 * math.Pi * float64(j) / float64(V)
		return Vec3{
			(R + r*math.Cos(w)) * math.Cos(u),
			(R + r*math.Cos(w)) * math.Sin(u),
			r * math.Sin(w),
		}
	}
	return patches, pos
}

func TestTorusClosedMesh(t *testing.T) {
	const U, V = 8, 4
	levels := make([]int, U*V)
	for k := range levels {
		levels[k] = (k*7 + k/3) % 4 // deterministic pseudo-random levels 0..3
	}
	patches, pos := torus(U, V, levels)
	topo := mustTopology(t, patches)
	pms, mesh := stitch(t, topo, pos)
	if topo.MaxAdjacentLevelDiff() > 1 {
		t.Fatalf("level constraint violated after Stitch")
	}
	if err := mesh.Validate(true); err != nil {
		t.Fatalf("Validate(closed): %v", err)
	}
	_, internal, boundary := mesh.Stats()
	if internal == 0 || boundary != 0 {
		t.Fatalf("internal=%d boundary=%d", internal, boundary)
	}
	if countSharedPoints(pms) == 0 {
		t.Fatal("no shared points across patches")
	}
	// Normals must point away from the tube center circle.
	const R = 2.0
	for _, tri := range mesh.Triangles {
		c := mesh.Centroid(tri)
		u := math.Atan2(c[1], c[0])
		tube := Vec3{R * math.Cos(u), R * math.Sin(u), 0}
		if mesh.Normal(tri).Dot(c.Sub(tube)) <= 0 {
			t.Fatalf("triangle %v normal points inward", tri)
		}
	}
}

func TestStitchDeterministic(t *testing.T) {
	const U, V = 6, 3
	levels := make([]int, U*V)
	for k := range levels {
		levels[k] = (k*5 + 1) % 4
	}
	run := func() ([]PatchMesh, *Mesh) {
		patches, pos := torus(U, V, levels)
		topo := mustTopology(t, patches)
		pms := NewStitcher(topo, pos, nil).Stitch()
		mesh, err := Merge(pms)
		if err != nil {
			t.Fatalf("Merge: %v", err)
		}
		return pms, mesh
	}
	pms1, mesh1 := run()
	pms2, mesh2 := run()
	if !reflect.DeepEqual(pms1, pms2) {
		t.Fatal("Stitch is not deterministic across runs")
	}
	if !reflect.DeepEqual(mesh1.Triangles, mesh2.Triangles) ||
		!reflect.DeepEqual(mesh1.Points, mesh2.Points) {
		t.Fatal("merged mesh is not deterministic across runs")
	}
}

func TestMergeDetectsMismatch(t *testing.T) {
	id := PointID{Kind: CornerPoint, Vert: 7}
	bad := []PatchMesh{
		{PatchID: 0, Points: map[PointID]Vec3{id: {0, 0, 0}}},
		{PatchID: 1, Points: map[PointID]Vec3{id: {0, 0, 1}}},
	}
	if _, err := Merge(bad); err == nil {
		t.Fatal("Merge accepted mismatched shared positions")
	}
}

func TestSingleCellFullySubdivided(t *testing.T) {
	// A level-0 quad surrounded by four level-1 quads: its only cell has
	// midpoints on all four edges and must be stitched around a cell
	// center point.
	flat := func(v int) Vec3 {
		return Vec3{float64(v % 4), float64(v / 4), 0}
	}
	topo := mustTopology(t, []Patch{
		{ID: 0, Corners: [4]int{0, 1, 2, 3}, Level: 0},
		{ID: 1, Corners: [4]int{1, 0, 4, 5}, Level: 1},
		{ID: 2, Corners: [4]int{2, 1, 6, 7}, Level: 1},
		{ID: 3, Corners: [4]int{3, 2, 8, 9}, Level: 1},
		{ID: 4, Corners: [4]int{0, 3, 10, 11}, Level: 1},
	})
	pms, mesh := stitch(t, topo, flat)
	if err := mesh.Validate(false); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	var centerTris int
	foundCenter := false
	for _, pm := range pms {
		if pm.PatchID != 0 {
			continue
		}
		centerTris = len(pm.Triangles)
		for id := range pm.Points {
			if id.Kind == CenterPoint {
				foundCenter = true
			}
		}
	}
	if !foundCenter {
		t.Fatal("no cell center point generated")
	}
	if centerTris != 8 {
		t.Errorf("center patch triangles = %d, want 8", centerTris)
	}
}
