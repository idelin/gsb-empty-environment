package laplacian

import (
	"math"
	"testing"
)

func inf() float64 { return math.Inf(1) }

// denseLUSolve solves A*x = b with partial-pivoting Gaussian elimination.
// Used only as a small-matrix reference implementation in tests.
func denseLUSolve(a [][]float64, b []float64) []float64 {
	n := len(a)
	m := make([][]float64, n)
	for i := range a {
		m[i] = append([]float64(nil), a[i]...)
	}
	rhs := append([]float64(nil), b...)
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}

	for col := 0; col < n; col++ {
		pivot := col
		for r := col + 1; r < n; r++ {
			if math.Abs(m[r][col]) > math.Abs(m[pivot][col]) {
				pivot = r
			}
		}
		m[col], m[pivot] = m[pivot], m[col]
		rhs[col], rhs[pivot] = rhs[pivot], rhs[col]
		perm[col], perm[pivot] = perm[pivot], perm[col]

		pv := m[col][col]
		if math.Abs(pv) < 1e-14 {
			panic("dense reference encountered singular matrix")
		}
		for r := col + 1; r < n; r++ {
			factor := m[r][col] / pv
			for c := col; c < n; c++ {
				m[r][c] -= factor * m[col][c]
			}
			rhs[r] -= factor * rhs[col]
		}
	}

	x := make([]float64, n)
	for r := n - 1; r >= 0; r-- {
		sum := rhs[r]
		for c := r + 1; c < n; c++ {
			sum -= m[r][c] * x[c]
		}
		x[r] = sum / m[r][r]
	}
	return x
}

func denseFromCSC(s *cscMatrix, symmetric bool) [][]float64 {
	n := s.n
	m := make([][]float64, n)
	for i := range m {
		m[i] = make([]float64, n)
	}
	for c := 0; c < n; c++ {
		for p := s.cols[c]; p < s.cols[c+1]; p++ {
			r := s.rows[p]
			m[r][c] = s.vals[p]
			if symmetric && r != c {
				m[c][r] = s.vals[p]
			}
		}
	}
	return m
}

func TestLDLMatchesDenseLU(t *testing.T) {
	// Build a small SPD matrix: H = Lfᵀ Lf from a real grid problem.
	mesh, _ := gridMesh(4, 4)
	anchors := []int{0, 3, 12}
	targets := []Vec3{{Z: 1}, {X: 1, Z: 2}, {Y: 1, Z: -1}}
	editor, err := NewEditor(mesh, anchors, targets)
	if err != nil {
		t.Fatalf("NewEditor: %v", err)
	}

	sparseH := editor.buildReducedMatrix()
	nf := len(editor.free)
	denseH := denseFromCSC(sparseH, true)

	// Verify symmetry and positive-definiteness implicitly via LU success.
	for _, rhs := range [][]float64{
		editor.reducedTransposeTimes(editor.restDelta[0]),
		editor.reducedTransposeTimes(editor.restDelta[1]),
		editor.reducedTransposeTimes(editor.restDelta[2]),
	} {
		sparseX := editor.factor.solve(rhs)
		denseX := denseLUSolve(denseH, rhs)
		for i := 0; i < nf; i++ {
			if math.Abs(sparseX[i]-denseX[i]) > 1e-9 {
				t.Fatalf("x[%d] sparse=%g dense=%g", i, sparseX[i], denseX[i])
			}
		}

		// Residual H*x - b must vanish for both.
		for i := 0; i < nf; i++ {
			dot := 0.0
			for j := 0; j < nf; j++ {
				dot += denseH[i][j] * sparseX[j]
			}
			if math.Abs(dot-rhs[i]) > 1e-8 {
				t.Fatalf("residual at %d = %g", i, dot-rhs[i])
			}
		}
	}
}

func TestSparseLaplacianMatchesDenseAssembly(t *testing.T) {
	mesh, _ := gridMesh(5, 3)
	editor, err := NewEditor(mesh, []int{0}, []Vec3{{}})
	if err != nil {
		t.Fatalf("NewEditor: %v", err)
	}

	n := editor.n
	denseL := make([][]float64, n)
	for i := range denseL {
		denseL[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		denseL[i][i] = 1
		degree := len(editor.adjacency[i])
		if degree == 0 {
			continue
		}
		w := 1.0 / float64(degree)
		for _, j := range editor.adjacency[i] {
			denseL[i][j] = -w
		}
	}

	sparseL := denseFromCSC(editor.laplacian, false)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if math.Abs(sparseL[i][j]-denseL[i][j]) > 1e-12 {
				t.Fatalf("L[%d][%d] sparse=%g dense=%g", i, j, sparseL[i][j], denseL[i][j])
			}
		}

		// Uniform weights are non-negative and each row sums to zero.
		rowSum := 0.0
		for _, v := range denseL[i] {
			rowSum += v
		}
		if math.Abs(rowSum) > 1e-12 {
			t.Fatalf("row %d sums to %g", i, rowSum)
		}
		for _, j := range editor.adjacency[i] {
			if denseL[i][j] >= 0 {
				t.Fatalf("weight L[%d][%d] = %g must be negative", i, j, denseL[i][j])
			}
		}
	}
}

func TestFullDeformMatchesDenseReference(t *testing.T) {
	mesh, _ := gridMesh(4, 4)
	anchors := []int{0, 3, 12, 15}
	targets := []Vec3{{Z: 1}, {X: 2, Z: 2}, {Y: 2, Z: -1}, {X: 2, Y: 2, Z: 3}}

	editor, err := NewEditor(mesh, anchors, targets)
	if err != nil {
		t.Fatalf("NewEditor: %v", err)
	}
	sparseResult := editor.Deform()

	// Independent dense reference: assemble full L, partition Lf/Lb and solve
	// the dense normal equations for every axis.
	n := editor.n
	denseL := denseFromCSC(editor.laplacian, false)
	nf := len(editor.free)
	lf := make([][]float64, n)
	for i := range lf {
		lf[i] = make([]float64, nf)
		for j, global := range editor.free {
			lf[i][j] = denseL[i][global]
		}
	}
	h := make([][]float64, nf)
	for i := range h {
		h[i] = make([]float64, nf)
		for j := 0; j < nf; j++ {
			sum := 0.0
			for r := 0; r < n; r++ {
				sum += lf[r][i] * lf[r][j]
			}
			h[i][j] = sum
		}
	}

	delta0 := make([][]float64, 3)
	for axis := 0; axis < 3; axis++ {
		d := make([]float64, n)
		for i, idx := range editor.anchor {
			d[idx] = targets[i].axis(axis)
		}
		delta0[axis] = d
	}

	densePositions := make([]Vec3, n)
	for i, idx := range editor.anchor {
		densePositions[idx] = targets[i]
	}
	for axis := 0; axis < 3; axis++ {
		ld0 := make([]float64, n)
		for r := 0; r < n; r++ {
			for c := 0; c < n; c++ {
				ld0[r] += denseL[r][c] * delta0[axis][c]
			}
		}
		g := make([]float64, n)
		for r := 0; r < n; r++ {
			g[r] = editor.restDelta[axis][r] - ld0[r]
		}
		rhs := make([]float64, nf)
		for j := 0; j < nf; j++ {
			sum := 0.0
			for r := 0; r < n; r++ {
				sum += lf[r][j] * g[r]
			}
			rhs[j] = sum
		}
		dfree := denseLUSolve(h, rhs)
		for j, global := range editor.free {
			delta0[axis][global] = dfree[j]
		}
		for i := 0; i < n; i++ {
			v := densePositions[i]
			switch axis {
			case 0:
				v.X = delta0[axis][i]
			case 1:
				v.Y = delta0[axis][i]
			case 2:
				v.Z = delta0[axis][i]
			}
			densePositions[i] = v
		}
	}

	for i := 0; i < n; i++ {
		if d := distance(sparseResult.Positions[i], densePositions[i]); d > 1e-8 {
			t.Fatalf("vertex %d sparse=%v dense=%v gap=%g", i, sparseResult.Positions[i], densePositions[i], d)
		}
	}
}
