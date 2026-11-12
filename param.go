package taskpkg

import (
	"fmt"
	"math"
)

// BoundaryPoint records where one boundary vertex lands on the unit circle.
// T is the normalized arc-length position in [0,1) and Angle is the
// corresponding polar angle in radians, measured counter-clockwise from +U.
type BoundaryPoint struct {
	Vertex int
	U, V   float64
	Angle  float64
	T      float64
}

// FaceArea is the signed UV area of one face. A positive value means the face
// keeps its input orientation in texture space; a non-positive value is either
// flipped (negative) or degenerate (zero).
type FaceArea struct {
	Face  int
	Area  float64
	Verts [3]int
}

// FlipEvidence describes the first flipped or degenerate face found after
// solving. It is returned wrapped inside *FlipError.
type FlipEvidence struct {
	Face        int
	Verts       [3]int
	SignedArea  float64
	MinPositive float64 // smallest positive signed area across all faces
}

// FlipError reports that the harmonic embedding is not strictly orientation
// preserving. Use errors.As to extract the *FlipError and inspect Evidence.
type FlipError struct {
	Evidence FlipEvidence
}

func (e *FlipError) Error() string {
	return fmt.Sprintf("%s: face %d (vertices %v) has signed UV area %.3e",
		ErrFlip, e.Evidence.Face, e.Evidence.Verts, e.Evidence.SignedArea)
}

// Is enables errors.Is(err, ErrFlip).
func (e *FlipError) Is(target error) bool { return target == ErrFlip }

// Result is the output of a successful harmonic parameterization.
type Result struct {
	// UV holds the texture coordinate of every input vertex, indexed by vertex id.
	UV [][2]float64
	// Boundary lists boundary vertices in loop order with their unit-circle images.
	Boundary []BoundaryPoint
	// FaceAreas lists signed UV areas for every face in face order.
	FaceAreas []FaceArea
	// MaxResidual is the infinity norm of A x - b over the interior rows.
	MaxResidual float64
	// Iterations is the number of sparse matrix-vector products used by the solver.
	Iterations int
}

const (
	// solverTol is the relative residual tolerance for the PCG solver.
	solverTol = 1e-12
	// flipRelTol declares a face flipped when its area is below this fraction
	// of the largest absolute face area, protecting against zero-area slivers.
	flipRelTol = 1e-12
)

// Parameterize computes a harmonic (Tutte) parameterization of m.
//
// The unique boundary loop is mapped onto the unit circle by cumulative edge
// length; every interior vertex is forced to the arithmetic mean of its
// neighbors, which gives the symmetric positive definite linear system
// deg(i)*u_i - sum_j u_j = boundary contribution.
//
// On success every face has strictly positive signed UV area. A flipped or
// degenerate face is reported with *FlipError rather than silently accepted.
func Parameterize(m *Mesh) (*Result, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: nil mesh", ErrInvalidMesh)
	}
	top, err := validateTopology(m)
	if err != nil {
		return nil, err
	}

	n := len(m.Positions)
	uv := make([][2]float64, n)
	boundary := mapBoundary(m, top.boundary, uv)

	// interior[v] is the compact row index for an interior vertex, or -1 for
	// a boundary vertex.
	isBoundary := make(map[int]bool, len(boundary))
	for _, bp := range boundary {
		isBoundary[bp.Vertex] = true
	}
	interior := make([]int, n)
	for i := range interior {
		interior[i] = -1
	}
	compact := 0
	for vi := range m.Positions {
		if !isBoundary[vi] {
			interior[vi] = compact
			compact++
		}
	}

	res := &Result{UV: uv, Boundary: boundary, Iterations: 0}

	if compact > 0 {
		a, rhsDense := buildLaplacianReduced(top.adj, interior, uv)

		bx := make([]float64, compact)
		by := make([]float64, compact)
		for i, r := range rhsDense {
			bx[i] = r[0]
			by[i] = r[1]
		}

		maxIter := 20 * compact
		ux, itX, _, errX := a.pcg(bx, solverTol, maxIter)
		uy, itY, _, errY := a.pcg(by, solverTol, maxIter)
		if errX != nil {
			return nil, errX
		}
		if errY != nil {
			return nil, errY
		}
		res.Iterations = itX + itY

		for vi, r := range interior {
			if r >= 0 {
				uv[vi] = [2]float64{ux[r], uy[r]}
			}
		}

		// Recompute the residual explicitly instead of trusting the solver's
		// internal norm so the reported value reflects the full system.
		checkX := sub(a.mul(ux), bx)
		checkY := sub(a.mul(uy), by)
		res.MaxResidual = math.Max(infNorm(checkX), infNorm(checkY))
	}

	areas := make([]FaceArea, len(m.Faces))
	minPositive := math.Inf(1)
	var flip *FlipError
	maxAbs := 0.0
	for fi, face := range m.Faces {
		area := signedArea(uv[face[0]], uv[face[1]], uv[face[2]])
		areas[fi] = FaceArea{Face: fi, Area: area, Verts: face}
		if a := math.Abs(area); a > maxAbs {
			maxAbs = a
		}
		if area > 0 && area < minPositive {
			minPositive = area
		}
	}
	for _, fa := range areas {
		if fa.Area <= maxAbs*flipRelTol {
			flip = &FlipError{Evidence: FlipEvidence{
				Face:        fa.Face,
				Verts:       fa.Verts,
				SignedArea:  fa.Area,
				MinPositive: minPositive,
			}}
			break
		}
	}
	res.FaceAreas = areas
	if flip != nil {
		return nil, flip
	}
	return res, nil
}

// mapBoundary places boundary vertices on the unit circle counter-clockwise,
// spacing them by the cumulative 3D length of boundary edges.
func mapBoundary(m *Mesh, loop []int, uv [][2]float64) []BoundaryPoint {
	lengths := make([]float64, len(loop))
	total := 0.0
	for i := range loop {
		a := m.Positions[loop[i]]
		b := m.Positions[loop[(i+1)%len(loop)]]
		d := distance(a, b)
		lengths[i] = d
		total += d
	}

	out := make([]BoundaryPoint, len(loop))
	cum := 0.0
	for i, vi := range loop {
		t := 0.0
		if total > 0 {
			t = cum / total
		}
		angle := 2 * math.Pi * t
		u := math.Cos(angle)
		v := math.Sin(angle)
		uv[vi] = [2]float64{u, v}
		out[i] = BoundaryPoint{Vertex: vi, U: u, V: v, Angle: angle, T: t}
		cum += lengths[i]
	}
	return out
}

func signedArea(p0, p1, p2 [2]float64) float64 {
	return 0.5 * ((p1[0]-p0[0])*(p2[1]-p0[1]) - (p2[0]-p0[0])*(p1[1]-p0[1]))
}

func distance(a, b Vec3) float64 {
	dx := b.X - a.X
	dy := b.Y - a.Y
	dz := b.Z - a.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

func sub(a, b []float64) []float64 {
	c := make([]float64, len(a))
	for i := range a {
		c[i] = a[i] - b[i]
	}
	return c
}
