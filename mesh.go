package taskpkg

import (
	"errors"
	"fmt"
	"sort"
)

// Vec3 is a three-dimensional vertex position.
type Vec3 struct {
	X, Y, Z float64
}

// Mesh is an oriented triangle mesh described by positions and triangular
// faces. Each face stores three vertex indices in counter-clockwise order
// when seen from the front side of the surface.
type Mesh struct {
	Positions []Vec3
	Faces     [][3]int
}

// Sentinel errors returned by Parameterize. Use errors.Is to classify failures
// and errors.As to inspect *FlipError evidence.
var (
	// ErrInvalidMesh covers malformed input such as bad indices or empty input.
	ErrInvalidMesh = errors.New("harmonic: invalid mesh data")
	// ErrNonManifold means the input is not a consistently oriented 2-manifold.
	ErrNonManifold = errors.New("harmonic: mesh is not an oriented 2-manifold")
	// ErrBoundary means the mesh does not have exactly one boundary loop.
	ErrBoundary = errors.New("harmonic: mesh must have exactly one boundary loop")
	// ErrNotDisk means the connected manifold mesh has the wrong Euler
	// characteristic for disk topology (for example it has genus).
	ErrNotDisk = errors.New("harmonic: mesh topology is not a disk")
	// ErrSolver means the sparse linear system did not reach the tolerance.
	ErrSolver = errors.New("harmonic: sparse linear solver failed to converge")
	// ErrFlip means the parameterization produced a flipped or degenerate face.
	ErrFlip = errors.New("harmonic: parameterization contains a flipped or degenerate face")
)

type topology struct {
	adj      [][]int
	edges    int
	boundary []int // ordered boundary vertex loop, deterministic start
}

// validateTopology verifies that m is a connected, consistently oriented
// triangle 2-manifold with exactly one boundary loop and disk topology
// (Euler characteristic V-E+F == 1).
func validateTopology(m *Mesh) (*topology, error) {
	n := len(m.Positions)
	if n < 3 {
		return nil, fmt.Errorf("%w: need at least 3 vertex positions, got %d", ErrInvalidMesh, n)
	}
	if len(m.Faces) == 0 {
		return nil, fmt.Errorf("%w: mesh has no faces", ErrInvalidMesh)
	}

	directed := make(map[[2]int]int)
	undirected := make(map[[2]int]bool)
	for fi, face := range m.Faces {
		for k := 0; k < 3; k++ {
			a := face[k]
			b := face[(k+1)%3]
			if a < 0 || a >= n || b < 0 || b >= n {
				return nil, fmt.Errorf("%w: face %d references vertex out of range [0,%d)", ErrInvalidMesh, fi, n)
			}
			if a == b {
				return nil, fmt.Errorf("%w: face %d is degenerate (repeated vertex %d)", ErrInvalidMesh, fi, a)
			}
		}
		for k := 0; k < 3; k++ {
			a := face[k]
			b := face[(k+1)%3]
			if directed[[2]int{a, b}] > 0 {
				return nil, fmt.Errorf("%w: directed edge (%d,%d) is used by multiple faces with the same winding", ErrNonManifold, a, b)
			}
			directed[[2]int{a, b}] = fi + 1
			u, v := a, b
			if u > v {
				u, v = v, u
			}
			undirected[[2]int{u, v}] = true
		}
	}

	adj := make([][]int, n)
	edges := 0
	for e := range undirected {
		u, v := e[0], e[1]
		cu := directed[[2]int{u, v}]
		cv := directed[[2]int{v, u}]
		incidence := b2i(cu) + b2i(cv)
		switch incidence {
		case 1:
			// boundary edge
		case 2:
			if cu == 0 || cv == 0 {
				return nil, fmt.Errorf("%w: edge (%d,%d) is shared with inconsistent winding", ErrNonManifold, u, v)
			}
		default:
			return nil, fmt.Errorf("%w: edge (%d,%d) is incident to %d faces", ErrNonManifold, u, v, incidence)
		}
		adj[u] = append(adj[u], v)
		adj[v] = append(adj[v], u)
		edges++
	}
	for i := range adj {
		if len(adj[i]) == 0 {
			return nil, fmt.Errorf("%w: vertex %d is not referenced by any face", ErrInvalidMesh, i)
		}
		sort.Ints(adj[i])
	}

	seen := make([]bool, n)
	queue := []int{0}
	seen[0] = true
	reached := 0
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		reached++
		for _, w := range adj[v] {
			if !seen[w] {
				seen[w] = true
				queue = append(queue, w)
			}
		}
	}
	if reached != n {
		return nil, fmt.Errorf("%w: mesh is disconnected", ErrNonManifold)
	}

	next := make(map[int]int)
	for e := range undirected {
		u, v := e[0], e[1]
		switch {
		case directed[[2]int{u, v}] > 0 && directed[[2]int{v, u}] == 0:
			next[u] = v
		case directed[[2]int{v, u}] > 0 && directed[[2]int{u, v}] == 0:
			next[v] = u
		}
	}
	if len(next) < 3 {
		return nil, fmt.Errorf("%w: found %d boundary edges, expected a loop of at least 3", ErrBoundary, len(next))
	}

	loops := 0
	var boundary []int
	used := make(map[int]bool)
	for len(used) < len(next) {
		start := pickUnusedStart(next, used)
		loops++
		cur := start
		for {
			used[cur] = true
			if loops == 1 {
				boundary = append(boundary, cur)
			}
			nxt, ok := next[cur]
			if !ok {
				return nil, fmt.Errorf("%w: boundary walk is broken at vertex %d", ErrBoundary, cur)
			}
			cur = nxt
			if cur == start {
				break
			}
		}
	}
	if loops != 1 {
		return nil, fmt.Errorf("%w: found %d boundary loops, exactly one is required", ErrBoundary, loops)
	}

	// Connected orientable surface: chi = 2 - 2*genus - boundaryLoops.
	// With one boundary loop, chi == 1 selects genus 0, i.e. a topological disk.
	chi := n - edges + len(m.Faces)
	if chi != 1 {
		return nil, fmt.Errorf("%w: Euler characteristic V-E+F = %d, expected 1", ErrNotDisk, chi)
	}

	return &topology{adj: adj, edges: edges, boundary: boundary}, nil
}

func pickUnusedStart(next map[int]int, used map[int]bool) int {
	best := -1
	for v := range next {
		if used[v] {
			continue
		}
		if best == -1 || v < best {
			best = v
		}
	}
	return best
}

func b2i(v int) int {
	if v > 0 {
		return 1
	}
	return 0
}
