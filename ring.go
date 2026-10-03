package hexgrid

import (
	"errors"
	"fmt"
	"math"
)

// ErrFaceBoundary is returned when a neighborhood traversal would leave
// the region covered by the base cell's hierarchy at this resolution.
var ErrFaceBoundary = errors.New("hexgrid: neighborhood crosses base-cell coverage boundary")

// KRing returns all cells within topological ring distance k of center,
// ordered as an outward spiral: the center, then ring 1, ring 2, ...
// The result contains exactly 1 + 3k(k+1) cells.
//
// The traversal is pure integer arithmetic on face-local IJ lattice
// coordinates (no floating point, no trigonometry): it walks each ring
// by unit neighbor steps and maps IJ back to digits via exact modular
// residue arithmetic.
func KRing(center Cell, k int) ([]Cell, error) {
	if k < 0 {
		return nil, errors.New("hexgrid: negative ring radius")
	}
	if !center.IsValid() {
		return nil, ErrInvalidCell
	}
	res := center.Res()
	base := center.BaseCell()
	cij := center.ij()

	out := make([]Cell, 0, 1+3*k*(k+1))
	out = append(out, center)
	for n := 1; n <= k; n++ {
		// Start at the ring corner n steps along direction 0, then walk
		// 6 sides of n steps each; D[i]+D[i+2] == D[i+1] keeps the walk
		// exactly on the radius-n ring.
		p := IJ{cij.I + n*ijDirections[0].I, cij.J + n*ijDirections[0].J}
		for side := 0; side < 6; side++ {
			step := ijDirections[(side+2)%6]
			for s := 0; s < n; s++ {
				cell, err := cellFromIJ(base, res, p)
				if err != nil {
					return nil, fmt.Errorf("%w (ring %d of %d)", ErrFaceBoundary, n, k)
				}
				out = append(out, cell)
				p.I += step.I
				p.J += step.J
			}
		}
	}
	return out, nil
}

// Neighbors returns the 6 ring-1 neighbors of a cell.
func Neighbors(c Cell) ([]Cell, error) {
	ring, err := KRing(c, 1)
	if err != nil {
		return nil, err
	}
	return ring[1:], nil
}

// RingDistance returns the topological (hex-grid) distance between two
// cells of the same base cell and resolution.
func RingDistance(a, b Cell) (int, error) {
	if a.BaseCell() != b.BaseCell() || a.Res() != b.Res() {
		return 0, errors.New("hexgrid: ring distance requires same base cell and resolution")
	}
	return ijDistance(a.ij(), b.ij()), nil
}

// vertexKey is a quantized 2D vertex used to match shared hexagon
// corners across adjacent cells despite floating-point roundoff.
type vertexKey [2]int64

func quantizeVertex(x, y float64) vertexKey {
	return vertexKey{int64(math.Round(x * 1e9)), int64(math.Round(y * 1e9))}
}

func lessVertex(a, b vertexKey) bool {
	if a[0] != b[0] {
		return a[0] < b[0]
	}
	return a[1] < b[1]
}

// VerifyClosedCoverage verifies that a set of same-resolution cells
// covers a continuous, closed region with no holes:
//
//   - every hexagon edge is shared by at most 2 cells (no overlap);
//   - boundary edges (used once) meet only at vertices of degree 2;
//   - all boundary edges form exactly one closed loop (a hole would
//     create a second disjoint loop).
func VerifyClosedCoverage(cells []Cell) error {
	if len(cells) == 0 {
		return errors.New("hexgrid: empty cell set")
	}
	res := cells[0].Res()
	type edgeKey struct{ a, b vertexKey }
	edgeCount := make(map[edgeKey]int)
	for _, c := range cells {
		if c.Res() != res {
			return fmt.Errorf("hexgrid: mixed resolutions in coverage set (%d vs %d)", c.Res(), res)
		}
		b := cellBoundary2D(c)
		for m := 0; m < 6; m++ {
			v1 := quantizeVertex(b[m][0], b[m][1])
			v2 := quantizeVertex(b[(m+1)%6][0], b[(m+1)%6][1])
			if lessVertex(v2, v1) {
				v1, v2 = v2, v1
			}
			edgeCount[edgeKey{v1, v2}]++
		}
	}

	boundary := make(map[vertexKey][]vertexKey)
	for e, n := range edgeCount {
		if n > 2 {
			return fmt.Errorf("hexgrid: edge %v shared by %d cells (overlap)", e, n)
		}
		if n == 1 {
			boundary[e.a] = append(boundary[e.a], e.b)
			boundary[e.b] = append(boundary[e.b], e.a)
		}
	}
	if len(boundary) == 0 {
		return errors.New("hexgrid: coverage set has no boundary")
	}
	for v, adj := range boundary {
		if len(adj) != 2 {
			return fmt.Errorf("hexgrid: boundary vertex %v has degree %d, want 2", v, len(adj))
		}
	}

	// Walk the boundary loop from an arbitrary start vertex.
	var start vertexKey
	for v := range boundary {
		start = v
		break
	}
	visited := 1 // count the start vertex
	prev, curr := start, boundary[start][0]
	for curr != start {
		next := boundary[curr][0]
		if next == prev {
			next = boundary[curr][1]
		}
		prev, curr = curr, next
		visited++
		if visited > len(boundary) {
			return errors.New("hexgrid: boundary walk did not terminate")
		}
	}
	if visited != len(boundary) {
		return fmt.Errorf("hexgrid: boundary splits into multiple loops (%d of %d vertices reached): holes present",
			visited, len(boundary))
	}
	return nil
}
