package taskpkg

import (
	"math"
	"math/rand"
	"testing"
)

// gridMesh builds a w by h planar quad grid split into triangles in the z=0
// plane. Faces are counter-clockwise when viewed from +z and the boundary is
// a single loop, so topology is a disk.
func gridMesh(w, h int) *Mesh {
	pos := make([]Vec3, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			pos[y*w+x] = Vec3{X: float64(x), Y: float64(y)}
		}
	}
	vid := func(x, y int) int { return y*w + x }
	var faces [][3]int
	for y := 0; y+1 < h; y++ {
		for x := 0; x+1 < w; x++ {
			a := vid(x, y)
			b := vid(x+1, y)
			c := vid(x, y+1)
			d := vid(x+1, y+1)
			faces = append(faces, [3]int{a, b, d}, [3]int{a, d, c})
		}
	}
	return &Mesh{Positions: pos, Faces: faces}
}

// permuteMesh relabels every vertex through perm and returns a mesh whose
// vertex numbering is unrelated to the original while geometry and winding
// are preserved.
func permuteMesh(m *Mesh, perm []int) *Mesh {
	n := len(m.Positions)
	out := &Mesh{Positions: make([]Vec3, n), Faces: make([][3]int, len(m.Faces))}
	for old, new := range perm {
		out.Positions[new] = m.Positions[old]
	}
	for fi, f := range m.Faces {
		for k := 0; k < 3; k++ {
			out.Faces[fi][k] = perm[f[k]]
		}
	}
	return out
}

func TestGridParameterizationPositiveAreas(t *testing.T) {
	m := gridMesh(5, 4)
	res, err := Parameterize(m)
	if err != nil {
		t.Fatalf("Parameterize returned error: %v", err)
	}
	if len(res.UV) != len(m.Positions) {
		t.Fatalf("UV count = %d, want %d", len(res.UV), len(m.Positions))
	}
	if len(res.Boundary) != 2*(5+4)-4 {
		t.Fatalf("boundary points = %d, want %d", len(res.Boundary), 2*(5+4)-4)
	}
	// Boundary vertices must land on the unit circle and keep loop orientation.
	var prevAngle float64
	for i, bp := range res.Boundary {
		r := math.Hypot(bp.U, bp.V)
		if math.Abs(r-1) > 1e-12 {
			t.Fatalf("boundary vertex %d radius = %.6f, want 1", bp.Vertex, r)
		}
		if i > 0 && bp.Angle <= prevAngle {
			t.Fatalf("boundary angles not strictly increasing at index %d", i)
		}
		if math.Abs(bp.T-float64(i)/float64(len(res.Boundary))) > 1e-12 {
			t.Fatalf("uniform grid boundary T[%d] = %.6f", i, bp.T)
		}
		prevAngle = bp.Angle
	}
	// Interior mean-value condition and strictly positive signed areas.
	top, err := validateTopology(m)
	if err != nil {
		t.Fatalf("validateTopology: %v", err)
	}
	boundary := make(map[int]bool)
	for _, bp := range res.Boundary {
		boundary[bp.Vertex] = true
	}
	for v := range m.Positions {
		if boundary[v] {
			continue
		}
		var meanU, meanV float64
		for _, w := range top.adj[v] {
			meanU += res.UV[w][0]
			meanV += res.UV[w][1]
		}
		deg := float64(len(top.adj[v]))
		if math.Abs(res.UV[v][0]-meanU/deg) > 1e-10 || math.Abs(res.UV[v][1]-meanV/deg) > 1e-10 {
			t.Fatalf("vertex %d is not the mean of its neighbors", v)
		}
	}
	for _, fa := range res.FaceAreas {
		if fa.Area <= 0 {
			t.Fatalf("face %d has non-positive signed area %.3e", fa.Face, fa.Area)
		}
	}
	if res.MaxResidual > 1e-11 {
		t.Fatalf("max residual %.3e exceeds tolerance", res.MaxResidual)
	}
}

func TestSparseMatchesDenseReference(t *testing.T) {
	m := gridMesh(5, 5)
	top, err := validateTopology(m)
	if err != nil {
		t.Fatalf("validateTopology: %v", err)
	}
	n := len(m.Positions)
	uv := make([][2]float64, n)
	_ = mapBoundary(m, top.boundary, uv)

	interior := make([]int, n)
	for i := range interior {
		interior[i] = -1
	}
	bp := mapBoundary(m, top.boundary, uv)
	compact := 0
	for i := range interior {
		isBoundary := false
		for _, p := range bp {
			if p.Vertex == i {
				isBoundary = true
				break
			}
		}
		if !isBoundary {
			interior[i] = compact
			compact++
		}
	}
	if compact == 0 {
		t.Fatal("test mesh has no interior vertices")
	}

	a, rhs := buildLaplacianReduced(top.adj, interior, uv)
	dense := denseFromCSR(a)

	// The reduced matrix must be symmetric before solving.
	for r := 0; r < compact; r++ {
		for c := 0; c < compact; c++ {
			if math.Abs(dense[r][c]-dense[c][r]) > 1e-14 {
				t.Fatalf("reduced matrix not symmetric at (%d,%d)", r, c)
			}
		}
	}

	bx := make([]float64, compact)
	by := make([]float64, compact)
	for i, r := range rhs {
		bx[i] = r[0]
		by[i] = r[1]
	}
	xSparse, _, _, err := a.pcg(bx, solverTol, 20*compact)
	if err != nil {
		t.Fatalf("sparse u solve: %v", err)
	}
	ySparse, _, _, err := a.pcg(by, solverTol, 20*compact)
	if err != nil {
		t.Fatalf("sparse v solve: %v", err)
	}
	xDense, err := denseSolve(cloneDense(dense), bx)
	if err != nil {
		t.Fatalf("dense u solve: %v", err)
	}
	yDense, err := denseSolve(cloneDense(dense), by)
	if err != nil {
		t.Fatalf("dense v solve: %v", err)
	}

	for i := 0; i < compact; i++ {
		if math.Abs(xSparse[i]-xDense[i]) > 1e-10 {
			t.Fatalf("u[%d] sparse %.10f != dense %.10f", i, xSparse[i], xDense[i])
		}
		if math.Abs(ySparse[i]-yDense[i]) > 1e-10 {
			t.Fatalf("v[%d] sparse %.10f != dense %.10f", i, ySparse[i], yDense[i])
		}
	}
}

func cloneDense(a [][]float64) [][]float64 {
	out := make([][]float64, len(a))
	for i := range a {
		out[i] = append([]float64(nil), a[i]...)
	}
	return out
}

func TestVertexOrderIndependence(t *testing.T) {
	base := gridMesh(6, 5)
	baseRes, err := Parameterize(base)
	if err != nil {
		t.Fatalf("base Parameterize: %v", err)
	}

	rng := rand.New(rand.NewSource(20261112))
	for trial := 0; trial < 5; trial++ {
		perm := rng.Perm(len(base.Positions))
		permuted := permuteMesh(base, perm)
		res, err := Parameterize(permuted)
		if err != nil {
			t.Fatalf("trial %d: Parameterize: %v", trial, err)
		}
		inv := make([]int, len(perm))
		for old, new := range perm {
			inv[new] = old
		}

		// Relabeling changes the smallest boundary id, which rotates the whole
		// circle parameterization. Recover that rotation from one shared
		// boundary vertex and then compare polar radius and aligned phase.
		shared := baseRes.Boundary[0].Vertex
		sharedNew := perm[shared]
		shift := math.Atan2(baseRes.UV[shared][1], baseRes.UV[shared][0]) -
			math.Atan2(res.UV[sharedNew][1], res.UV[sharedNew][0])

		for newVertex := range permuted.Positions {
			old := inv[newVertex]
			x := res.UV[newVertex][0]
			y := res.UV[newVertex][1]
			bx := baseRes.UV[old][0]
			by := baseRes.UV[old][1]
			if d := math.Abs(math.Hypot(x, y) - math.Hypot(bx, by)); d > 1e-10 {
				t.Fatalf("trial %d: vertex %d radius changed by %.3e", trial, old, d)
			}
			phaseA := math.Atan2(y, x) + shift
			phaseB := math.Atan2(by, bx)
			if d := angleDiff(phaseA, phaseB); d > 1e-10 {
				t.Fatalf("trial %d: vertex %d phase changed by %.3e after alignment", trial, old, d)
			}
		}
		for _, fa := range res.FaceAreas {
			if fa.Area <= 0 {
				t.Fatalf("trial %d: flipped face %d area %.3e", trial, fa.Face, fa.Area)
			}
		}
		// Signed areas are rotation-invariant and must match the base exactly.
		for fi, fa := range res.FaceAreas {
			if math.Abs(fa.Area-baseRes.FaceAreas[fi].Area) > 1e-12 {
				t.Fatalf("trial %d: face %d area %.10f != base %.10f",
					trial, fi, fa.Area, baseRes.FaceAreas[fi].Area)
			}
		}
	}
}

func angleDiff(a, b float64) float64 {
	return math.Abs(math.Atan2(math.Sin(a-b), math.Cos(a-b)))
}

func TestSolutionIsUnique(t *testing.T) {
	// Two independent solves of the same system must agree to solver precision.
	m := gridMesh(4, 4)
	r1, err := Parameterize(m)
	if err != nil {
		t.Fatalf("first solve: %v", err)
	}
	r2, err := Parameterize(m)
	if err != nil {
		t.Fatalf("second solve: %v", err)
	}
	for v := range m.Positions {
		if math.Abs(r1.UV[v][0]-r2.UV[v][0]) > 1e-14 || math.Abs(r1.UV[v][1]-r2.UV[v][1]) > 1e-14 {
			t.Fatalf("vertex %d differs between solves, solution not deterministic/unique", v)
		}
	}
}

func TestSkinnyTrianglesStayPositive(t *testing.T) {
	// A strip whose middle vertices sit extremely close to an edge produces
	// high-aspect-ratio triangles in 3D, including z jitter.
	m := &Mesh{
		Positions: []Vec3{
			{0, 0, 0},
			{1000, 0, 0.0001},
			{1e-6, 1, 0},
			{999.999, 1, -0.0001},
			{0, 2, 0},
			{1000, 2, 0.0002},
		},
		Faces: [][3]int{
			{0, 1, 3},
			{0, 3, 2},
			{2, 3, 5},
			{2, 5, 4},
		},
	}
	res, err := Parameterize(m)
	if err != nil {
		t.Fatalf("skinny Parameterize: %v", err)
	}
	for _, fa := range res.FaceAreas {
		if fa.Area <= 0 {
			t.Fatalf("skinny face %d has non-positive area %.3e", fa.Face, fa.Area)
		}
	}
	if res.MaxResidual > 1e-9 {
		t.Fatalf("skinny residual %.3e too large", res.MaxResidual)
	}
}

func TestSingleTriangleBoundaryOnly(t *testing.T) {
	m := &Mesh{
		Positions: []Vec3{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}},
		Faces:     [][3]int{{0, 1, 2}},
	}
	res, err := Parameterize(m)
	if err != nil {
		t.Fatalf("single triangle: %v", err)
	}
	if res.Iterations != 0 || res.MaxResidual != 0 {
		t.Fatalf("boundary-only mesh should need no solver iterations, got %d / %.3e",
			res.Iterations, res.MaxResidual)
	}
	if res.FaceAreas[0].Area <= 0 {
		t.Fatalf("single triangle area = %.3e", res.FaceAreas[0].Area)
	}
}

func TestBoundaryArcLengthMapping(t *testing.T) {
	// Rectangle with unequal edge lengths: boundary T values must reflect
	// cumulative 3D edge length, not equal angular spacing.
	m := &Mesh{
		Positions: []Vec3{{0, 0, 0}, {4, 0, 0}, {4, 1, 0}, {0, 1, 0}},
		Faces:     [][3]int{{0, 1, 2}, {0, 2, 3}},
	}
	res, err := Parameterize(m)
	if err != nil {
		t.Fatalf("Parameterize: %v", err)
	}
	perimeter := 10.0
	want := map[int]float64{0: 0, 1: 4.0 / perimeter, 2: 5.0 / perimeter, 3: 9.0 / perimeter}
	for _, bp := range res.Boundary {
		if math.Abs(bp.T-want[bp.Vertex]) > 1e-12 {
			t.Fatalf("vertex %d T = %.6f, want %.6f", bp.Vertex, bp.T, want[bp.Vertex])
		}
	}
}
