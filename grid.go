package taskpkg

import (
	"errors"
	"math"
)

// Coeff holds the spatially varying medium coefficients at a point:
// Sigma is the absorption coefficient and Em is the linear RGB emission rate.
type Coeff struct {
	Sigma float64
	Em    RGB
}

// Grid is a structured, possibly non-uniform Cartesian grid.
//
// The grid is defined by strictly increasing node coordinates. Voxel
// coefficient samples live at the grid nodes, and coefficients inside a
// cell (between adjacent nodes) are produced by trilinear interpolation of
// the eight surrounding node values. Non-uniform spacing is handled
// naturally because interpolation uses the parametric cell coordinates
// rather than a uniform cell size.
type Grid struct {
	nx, ny, nz int
	x, y, z    []float64
	sigma      []float64
	em         []RGB

	// Bounding box of the node coordinates.
	xmin, xmax, ymin, ymax, zmin, zmax float64

	// Extremes over all node samples, used for conservative remainder bounds.
	maxSigma float64
	maxEm    RGB
}

// NewGrid builds a grid from node coordinates and node-indexed voxel data.
//
// Coordinate slices have lengths nx, ny, nz; the coefficient slices have
// length nx*ny*nz with node (i,j,k) at index (k*ny+j)*nx+i. Coordinates must
// be strictly increasing and finite; absorption coefficients must be
// non-negative and finite.
func NewGrid(xs, ys, zs []float64, sigma []float64, em []RGB) (*Grid, error) {
	nx, ny, nz := len(xs), len(ys), len(zs)
	if nx < 2 || ny < 2 || nz < 2 {
		return nil, errors.New("grid requires at least two nodes per axis")
	}
	want := nx * ny * nz
	if len(sigma) != want || len(em) != want {
		return nil, errors.New("coefficient slice length does not match grid dimensions")
	}
	for _, s := range [][]float64{xs, ys, zs} {
		for i, v := range s {
			if !isFinite(v) {
				return nil, errors.New("grid coordinate must be finite")
			}
			if i > 0 && v <= s[i-1] {
				return nil, errors.New("grid coordinates must be strictly increasing")
			}
		}
	}
	for i := range sigma {
		if !isFinite(sigma[i]) || sigma[i] < 0 {
			return nil, errors.New("absorption coefficient must be finite and non-negative")
		}
		for c := 0; c < 3; c++ {
			if !isFinite(em[i][c]) {
				return nil, errors.New("emission must be finite")
			}
		}
	}
	g := &Grid{
		nx:    nx,
		ny:    ny,
		nz:    nz,
		x:     append([]float64(nil), xs...),
		y:     append([]float64(nil), ys...),
		z:     append([]float64(nil), zs...),
		sigma: append([]float64(nil), sigma...),
		em:    append([]RGB(nil), em...),
	}
	g.xmin, g.xmax = xs[0], xs[nx-1]
	g.ymin, g.ymax = ys[0], ys[ny-1]
	g.zmin, g.zmax = zs[0], zs[nz-1]
	for _, s := range sigma {
		g.maxSigma = max(g.maxSigma, s)
	}
	for _, e := range em {
		for c := 0; c < 3; c++ {
			g.maxEm[c] = max(g.maxEm[c], math.Abs(e[c]))
		}
	}
	return g, nil
}

// Dims returns the node counts along each axis.
func (g *Grid) Dims() (nx, ny, nz int) { return g.nx, g.ny, g.nz }

// index maps a node index triple onto the flat data slices.
func (g *Grid) index(i, j, k int) int { return (k*g.ny+j)*g.nx + i }

// CoeffAt returns the trilinearly interpolated coefficients at point p.
// Points outside the grid are clamped to the bounding box before lookup.
func (g *Grid) CoeffAt(p Vec3) Coeff {
	i, fx, j, fy, k, fz := g.locate(p)
	return g.interp(i, j, k, fx, fy, fz)
}

// locate returns the low corner index of the cell containing p and the
// parametric coordinates (all in [0,1]) of p within that cell. A point on an
// interior node boundary returns the cell on its low side with a coordinate
// of 1.0, which is equivalent under trilinear interpolation.
func (g *Grid) locate(p Vec3) (i int, fx float64, j int, fy float64, k int, fz float64) {
	i, fx = locateAxis(g.x, p[0])
	j, fy = locateAxis(g.y, p[1])
	k, fz = locateAxis(g.z, p[2])
	return i, fx, j, fy, k, fz
}

// locateAxis locates value v within a strictly increasing node slice.
func locateAxis(nodes []float64, v float64) (idx int, frac float64) {
	n := len(nodes)
	if v <= nodes[0] {
		return 0, 0
	}
	if v >= nodes[n-1] {
		return n - 2, 1
	}
	// First node strictly greater than v.
	hi := upperBound(nodes, v)
	lo := hi - 1
	idx = lo
	frac = (v - nodes[lo]) / (nodes[hi] - nodes[lo])
	return idx, frac
}

// upperBound returns the smallest index i with nodes[i] > v.
func upperBound(nodes []float64, v float64) int {
	lo, hi := 0, len(nodes)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if nodes[mid] <= v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// interp performs trilinear interpolation on the eight corners of cell
// (i,j,k), with (fx,fy,fz) the parametric position inside the cell.
func (g *Grid) interp(i, j, k int, fx, fy, fz float64) Coeff {
	c000 := g.node(i, j, k)
	c100 := g.node(i+1, j, k)
	c010 := g.node(i, j+1, k)
	c110 := g.node(i+1, j+1, k)
	c001 := g.node(i, j, k+1)
	c101 := g.node(i+1, j, k+1)
	c011 := g.node(i, j+1, k+1)
	c111 := g.node(i+1, j+1, k+1)

	c00 := lerpCoeff(c000, c100, fx)
	c10 := lerpCoeff(c010, c110, fx)
	c01 := lerpCoeff(c001, c101, fx)
	c11 := lerpCoeff(c011, c111, fx)

	c0 := lerpCoeff(c00, c10, fy)
	c1 := lerpCoeff(c01, c11, fy)
	return lerpCoeff(c0, c1, fz)
}

func (g *Grid) node(i, j, k int) Coeff {
	idx := g.index(i, j, k)
	return Coeff{Sigma: g.sigma[idx], Em: g.em[idx]}
}

func lerpCoeff(a, b Coeff, t float64) Coeff {
	return Coeff{
		Sigma: a.Sigma + (b.Sigma-a.Sigma)*t,
		Em: RGB{
			a.Em[0] + (b.Em[0]-a.Em[0])*t,
			a.Em[1] + (b.Em[1]-a.Em[1])*t,
			a.Em[2] + (b.Em[2]-a.Em[2])*t,
		},
	}
}
