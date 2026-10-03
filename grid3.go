package sdf

import (
	"math"
	"runtime"
	"sync"
)

// Grid3 is a rasterized (sampled) 3D signed distance field over the
// axis-aligned box [Lo, Hi]. Values are stored with x fastest. A Grid3
// is itself an SDF3 via trilinear interpolation, so it can be
// ray-marched or re-sampled like an analytic field.
type Grid3 struct {
	Nx, Ny, Nz int
	Lo, Hi     Vec3
	Values     []float64
}

// Sample3 rasterizes sdf into an nx*ny*nz grid covering [lo, hi].
// Sampling is parallelized across the available CPU cores, which makes
// it cheap to re-rasterize dynamic (animated) fields every frame.
func Sample3(s SDF3, nx, ny, nz int, lo, hi Vec3) *Grid3 {
	g := &Grid3{
		Nx: nx, Ny: ny, Nz: nz,
		Lo: lo, Hi: hi,
		Values: make([]float64, nx*ny*nz),
	}
	workers := runtime.NumCPU()
	if workers > nz {
		workers = nz
	}
	slabsPerWorker := (nz + workers - 1) / workers
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		k0 := w * slabsPerWorker
		k1 := k0 + slabsPerWorker
		if k1 > nz {
			k1 = nz
		}
		if k0 >= k1 {
			break
		}
		wg.Add(1)
		go func(k0, k1 int) {
			defer wg.Done()
			for k := k0; k < k1; k++ {
				z := lo.Z + float64(k)*(hi.Z-lo.Z)/float64(nz-1)
				for j := 0; j < ny; j++ {
					y := lo.Y + float64(j)*(hi.Y-lo.Y)/float64(ny-1)
					base := (k*ny + j) * nx
					for i := 0; i < nx; i++ {
						x := lo.X + float64(i)*(hi.X-lo.X)/float64(nx-1)
						g.Values[base+i] = s.Distance(Vec3{x, y, z})
					}
				}
			}
		}(k0, k1)
	}
	wg.Wait()
	return g
}

// At returns the sampled value at grid node (i, j, k).
func (g *Grid3) At(i, j, k int) float64 { return g.Values[(k*g.Ny+j)*g.Nx+i] }

// Position returns the world-space position of grid node (i, j, k).
func (g *Grid3) Position(i, j, k int) Vec3 {
	return Vec3{
		g.Lo.X + float64(i)*(g.Hi.X-g.Lo.X)/float64(g.Nx-1),
		g.Lo.Y + float64(j)*(g.Hi.Y-g.Lo.Y)/float64(g.Ny-1),
		g.Lo.Z + float64(k)*(g.Hi.Z-g.Lo.Z)/float64(g.Nz-1),
	}
}

// Cell returns the world-space size of one grid cell.
func (g *Grid3) Cell() Vec3 {
	return Vec3{
		(g.Hi.X - g.Lo.X) / float64(g.Nx-1),
		(g.Hi.Y - g.Lo.Y) / float64(g.Ny-1),
		(g.Hi.Z - g.Lo.Z) / float64(g.Nz-1),
	}
}

// Value returns the trilinearly interpolated field value at p.
// p is clamped to the grid bounds.
func (g *Grid3) Value(p Vec3) float64 {
	fx := (p.X - g.Lo.X) / (g.Hi.X - g.Lo.X) * float64(g.Nx-1)
	fy := (p.Y - g.Lo.Y) / (g.Hi.Y - g.Lo.Y) * float64(g.Ny-1)
	fz := (p.Z - g.Lo.Z) / (g.Hi.Z - g.Lo.Z) * float64(g.Nz-1)
	fx = math.Max(0, math.Min(float64(g.Nx-1), fx))
	fy = math.Max(0, math.Min(float64(g.Ny-1), fy))
	fz = math.Max(0, math.Min(float64(g.Nz-1), fz))
	i0 := int(math.Min(float64(g.Nx-2), math.Floor(fx)))
	j0 := int(math.Min(float64(g.Ny-2), math.Floor(fy)))
	k0 := int(math.Min(float64(g.Nz-2), math.Floor(fz)))
	tx := fx - float64(i0)
	ty := fy - float64(j0)
	tz := fz - float64(k0)
	c000 := g.At(i0, j0, k0)
	c100 := g.At(i0+1, j0, k0)
	c010 := g.At(i0, j0+1, k0)
	c110 := g.At(i0+1, j0+1, k0)
	c001 := g.At(i0, j0, k0+1)
	c101 := g.At(i0+1, j0, k0+1)
	c011 := g.At(i0, j0+1, k0+1)
	c111 := g.At(i0+1, j0+1, k0+1)
	c00 := c000*(1-tx) + c100*tx
	c10 := c010*(1-tx) + c110*tx
	c01 := c001*(1-tx) + c101*tx
	c11 := c011*(1-tx) + c111*tx
	c0 := c00*(1-ty) + c10*ty
	c1 := c01*(1-ty) + c11*ty
	return c0*(1-tz) + c1*tz
}

// Distance implements SDF3, sampling the rasterized grid.
func (g *Grid3) Distance(p Vec3) float64 { return g.Value(p) }
