package laplacian

import (
	"math"
	"sort"
)

// Editor holds the topology-dependent factorization of a Laplacian
// deformation problem. Create one with NewEditor, then call Deform repeatedly;
// SetTargets changes anchor goals while reusing the factorization.
type Editor struct {
	mesh      *Mesh
	n         int
	adjacency [][]int
	anchor    []int // global vertex indices, deduplicated and sorted
	target    []Vec3
	free      []int // global vertex indices, sorted
	localOf   []int // global -> free index, -1 for anchors
	laplacian *cscMatrix
	factor    *ldlFactorization
	restDelta [][]float64 // rest-pose Laplacian coordinates, per axis
	component []int       // component id per vertex
	solved    bool        // whether Deform has run since construction/retarget
}

// Deform is a one-shot convenience wrapper. Prefer NewEditor when several
// target updates are needed, so the LDLᵀ factorization is computed only once.
func Deform(mesh *Mesh, anchors []int, targets []Vec3) (*Result, error) {
	editor, err := NewEditor(mesh, anchors, targets)
	if err != nil {
		return nil, err
	}
	return editor.Deform(), nil
}

// NewEditor validates the mesh and anchors, builds the uniform-weight
// Laplacian matrix and factorizes the reduced normal-equation matrix. All
// connected components must contain at least one anchor.
func NewEditor(mesh *Mesh, anchors []int, targets []Vec3) (*Editor, error) {
	if mesh == nil || len(mesh.Vertices) == 0 {
		return nil, ErrInvalidMesh
	}
	if len(anchors) != len(targets) {
		return nil, ErrAnchorOutOfRange
	}
	n := len(mesh.Vertices)
	for _, v := range mesh.Vertices {
		if !isFinite(v.X) || !isFinite(v.Y) || !isFinite(v.Z) {
			return nil, ErrInvalidMesh
		}
	}

	adjacency, err := buildAdjacency(mesh)
	if err != nil {
		return nil, err
	}

	if len(anchors) == 0 {
		return nil, ErrNoAnchors
	}

	// Deduplicate anchors, keeping the first target seen for each vertex.
	anchorSet := make(map[int]struct{}, len(anchors))
	anchorList := make([]int, 0, len(anchors))
	targetByVertex := make(map[int]Vec3, len(anchors))
	for i, idx := range anchors {
		if idx < 0 || idx >= n {
			return nil, ErrAnchorOutOfRange
		}
		t := targets[i]
		if !isFinite(t.X) || !isFinite(t.Y) || !isFinite(t.Z) {
			return nil, ErrInvalidMesh
		}
		if _, seen := anchorSet[idx]; !seen {
			anchorSet[idx] = struct{}{}
			anchorList = append(anchorList, idx)
			targetByVertex[idx] = t
		}
	}
	sort.Ints(anchorList)

	component, compCount := connectedComponents(adjacency)
	anchored := make([]bool, compCount)
	for _, idx := range anchorList {
		anchored[component[idx]] = true
	}
	for c := 0; c < compCount; c++ {
		if !anchored[c] {
			return nil, ErrComponentWithoutAnchor
		}
	}

	free := make([]int, 0, n-len(anchorList))
	localOf := make([]int, n)
	for i := range localOf {
		localOf[i] = -1
	}
	isAnchor := make(map[int]bool, len(anchorList))
	for _, idx := range anchorList {
		isAnchor[idx] = true
	}
	for i := 0; i < n; i++ {
		if !isAnchor[i] {
			localOf[i] = len(free)
			free = append(free, i)
		}
	}

	targetList := make([]Vec3, len(anchorList))
	for i, idx := range anchorList {
		targetList[i] = targetByVertex[idx]
	}

	editor := &Editor{
		mesh:      mesh,
		n:         n,
		adjacency: adjacency,
		anchor:    anchorList,
		target:    targetList,
		free:      free,
		localOf:   localOf,
		component: component,
	}

	editor.laplacian = editor.buildLaplacian()
	editor.restDelta = editor.restLaplacianCoordinates()

	if len(free) > 0 {
		reduced := editor.buildReducedMatrix()
		factor, err := factorLDL(reduced)
		if err != nil {
			return nil, err
		}
		editor.factor = factor
	}

	return editor, nil
}

// SetTargets replaces anchor target positions. Anchor indices and mesh
// topology are unchanged, so the existing factorization is reused on the next
// Deform call. The slice must contain one entry per NewEditor anchor, in the
// order returned by Anchors.
func (e *Editor) SetTargets(targets []Vec3) error {
	if len(targets) != len(e.anchor) {
		return ErrAnchorOutOfRange
	}
	for _, t := range targets {
		if !isFinite(t.X) || !isFinite(t.Y) || !isFinite(t.Z) {
			return ErrInvalidMesh
		}
	}
	copied := make([]Vec3, len(targets))
	copy(copied, targets)
	e.target = copied
	return nil
}

// Anchors returns the deduplicated anchor vertex indices in ascending order.
func (e *Editor) Anchors() []int {
	out := make([]int, len(e.anchor))
	copy(out, e.anchor)
	return out
}

// Components returns the component identifier of every vertex and the number
// of connected components.
func (e *Editor) Components() (ids []int, count int) {
	out := make([]int, len(e.component))
	copy(out, e.component)
	return out, e.component[len(e.component)-1] + 1
}

// Deform solves for positions satisfying the anchor targets while minimizing
// the Laplacian-coordinate residual.
func (e *Editor) Deform() *Result {
	positions := make([]Vec3, e.n)

	for i, idx := range e.anchor {
		positions[idx] = e.target[i]
	}

	reused := e.solved

	if len(e.free) > 0 {
		// Constant solution δ₀: anchors at targets, free vertices at origin.
		delta0 := make([][]float64, 3)
		for axis := 0; axis < 3; axis++ {
			d := make([]float64, e.n)
			for i, idx := range e.anchor {
				d[idx] = e.target[i].axis(axis)
			}
			delta0[axis] = d
		}

		for axis := 0; axis < 3; axis++ {
			ld0 := e.laplacian.matVec(delta0[axis])
			// Normal equations H xf = Lfᵀ (δ_rest - L δ₀).
			g := vectorSub(e.restDelta[axis], ld0)
			rhs := e.reducedTransposeTimes(g)

			dfree := e.factor.solve(rhs)
			for local, global := range e.free {
				delta0[axis][global] = dfree[local]
			}
		}

		for i := 0; i < e.n; i++ {
			positions[i] = Vec3{delta0[0][i], delta0[1][i], delta0[2][i]}
		}
	}

	e.solved = true
	return e.evaluate(positions, reused)
}

// evaluate assembles result statistics, including hard-constraint violation
// and per-vertex Laplacian residual norms.
func (e *Editor) evaluate(positions []Vec3, reused bool) *Result {
	perVertex := make([]float64, e.n)
	var sumSquared, maxResidual float64

	for axis := 0; axis < 3; axis++ {
		coord := make([]float64, e.n)
		for i, v := range positions {
			coord[i] = v.axis(axis)
		}
		current := e.laplacian.matVec(coord)
		for i := 0; i < e.n; i++ {
			d := current[i] - e.restDelta[axis][i]
			perVertex[i] += d * d
		}
	}

	for i := 0; i < e.n; i++ {
		perVertex[i] = math.Sqrt(perVertex[i])
		sumSquared += perVertex[i] * perVertex[i]
		if perVertex[i] > maxResidual {
			maxResidual = perVertex[i]
		}
	}

	anchorMax := 0.0
	for i, idx := range e.anchor {
		if d := distance(positions[idx], e.target[i]); d > anchorMax {
			anchorMax = d
		}
	}

	return &Result{
		Positions:           positions,
		AnchorMax:           anchorMax,
		VertexResidual:      perVertex,
		RMSResidual:         math.Sqrt(sumSquared / float64(e.n)),
		MaxResidual:         maxResidual,
		ReusedFactorization: reused,
	}
}

// buildLaplacian constructs L with L[i][i] = 1 and L[i][j] = -1/degree(i)
// for each neighbor j. The non-negative uniform weights sum to one per row.
func (e *Editor) buildLaplacian() *cscMatrix {
	entries := make([]cooEntry, 0, e.n+6*len(e.mesh.Faces))
	for i := 0; i < e.n; i++ {
		entries = append(entries, cooEntry{i, i, 1})
		degree := len(e.adjacency[i])
		if degree == 0 {
			continue
		}
		w := 1.0 / float64(degree)
		for _, j := range e.adjacency[i] {
			entries = append(entries, cooEntry{i, j, -w})
		}
	}
	return buildCSC(entries, e.n, buildFull)
}

// restLaplacianCoordinates computes δ = L * x for each coordinate axis of the
// rest pose.
func (e *Editor) restLaplacianCoordinates() [][]float64 {
	delta := make([][]float64, 3)
	for axis := 0; axis < 3; axis++ {
		coord := make([]float64, e.n)
		for i, v := range e.mesh.Vertices {
			coord[i] = v.axis(axis)
		}
		delta[axis] = e.laplacian.matVec(coord)
	}
	return delta
}

// buildReducedMatrix assembles H = Lfᵀ Lf (lower triangle) where Lf contains
// the free-vertex columns of the Laplacian.
func (e *Editor) buildReducedMatrix() *cscMatrix {
	nf := len(e.free)
	entries := make([]cooEntry, 0)

	// H = Lfᵀ Lf. For each row r of Lf, add the outer product vᵀv of its
	// free-column entries v. Row r of L has a unit diagonal and -1/degree(r)
	// on each neighbor, so the entries are assembled directly from adjacency.
	for r := 0; r < e.n; r++ {
		type rowVal struct {
			row int
			val float64
		}
		col := make([]rowVal, 0)
		if lr := e.localOf[r]; lr >= 0 {
			col = append(col, rowVal{lr, 1})
		}
		degree := len(e.adjacency[r])
		if degree > 0 {
			w := 1.0 / float64(degree)
			for _, nb := range e.adjacency[r] {
				if lnb := e.localOf[nb]; lnb >= 0 {
					col = append(col, rowVal{lnb, -w})
				}
			}
		}
		for _, a := range col {
			for _, b := range col {
				entries = append(entries, cooEntry{a.row, b.row, a.val * b.val})
			}
		}
	}

	return buildCSC(entries, nf, buildLowerDiscard)
}

// reducedTransposeTimes computes Lfᵀ x.
func (e *Editor) reducedTransposeTimes(x []float64) []float64 {
	nf := len(e.free)
	out := make([]float64, nf)
	for lc, globalCol := range e.free {
		sum := 0.0
		// Lf keeps all n rows; only anchor columns are removed, so every row
		// (including anchor rows) contributes to this dot product.
		for p := e.laplacian.cols[globalCol]; p < e.laplacian.cols[globalCol+1]; p++ {
			row := e.laplacian.rows[p]
			sum += e.laplacian.vals[p] * x[row]
		}
		out[lc] = sum
	}
	return out
}

// buildAdjacency derives the symmetric neighbor list from triangle faces.
func buildAdjacency(mesh *Mesh) ([][]int, error) {
	n := len(mesh.Vertices)
	neighbors := make(map[int]map[int]struct{}, n)
	for i := 0; i < n; i++ {
		neighbors[i] = make(map[int]struct{})
	}

	addEdge := func(a, b int) error {
		if a == b {
			return ErrInvalidMesh
		}
		neighbors[a][b] = struct{}{}
		neighbors[b][a] = struct{}{}
		return nil
	}

	for _, face := range mesh.Faces {
		for _, idx := range face {
			if idx < 0 || idx >= n {
				return nil, ErrInvalidMesh
			}
		}
		if err := addEdge(face[0], face[1]); err != nil {
			return nil, err
		}
		if err := addEdge(face[1], face[2]); err != nil {
			return nil, err
		}
		if err := addEdge(face[2], face[0]); err != nil {
			return nil, err
		}
	}

	adjacency := make([][]int, n)
	for i := 0; i < n; i++ {
		list := make([]int, 0, len(neighbors[i]))
		for j := range neighbors[i] {
			list = append(list, j)
		}
		sort.Ints(list)
		adjacency[i] = list
	}
	return adjacency, nil
}

// connectedComponents labels vertices via BFS over the adjacency graph.
func connectedComponents(adjacency [][]int) (ids []int, count int) {
	n := len(adjacency)
	ids = make([]int, n)
	for i := range ids {
		ids[i] = -1
	}

	queue := make([]int, 0, n)
	for start := 0; start < n; start++ {
		if ids[start] != -1 {
			continue
		}
		ids[start] = count
		queue = append(queue, start)
		for len(queue) > 0 {
			v := queue[0]
			queue = queue[1:]
			for _, next := range adjacency[v] {
				if ids[next] == -1 {
					ids[next] = count
					queue = append(queue, next)
				}
			}
		}
		count++
	}
	return ids, count
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func vectorSub(a, b []float64) []float64 {
	out := make([]float64, len(a))
	for i := range a {
		out[i] = a[i] - b[i]
	}
	return out
}

func distance(a, b Vec3) float64 {
	dx := a.X - b.X
	dy := a.Y - b.Y
	dz := a.Z - b.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

func (v Vec3) axis(i int) float64 {
	switch i {
	case 0:
		return v.X
	case 1:
		return v.Y
	default:
		return v.Z
	}
}
