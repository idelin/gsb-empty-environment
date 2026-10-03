package sdf

import (
	"math"
	"runtime"
	"sync"
)

// Grid2 is a rasterized (sampled) 2D signed distance field over the
// axis-aligned rectangle [Lo, Hi]. Values are stored row-major with
// Nx*Ny samples. A Grid2 is itself an SDF2 via bilinear interpolation,
// so it can be ray-marched or re-sampled like an analytic field.
type Grid2 struct {
	Nx, Ny int
	Lo, Hi Vec2
	Values []float64
}

// Sample2 rasterizes sdf into an nx*ny grid covering [lo, hi].
// Sampling is parallelized across the available CPU cores, which makes
// it cheap to re-rasterize dynamic (animated) fields every frame.
func Sample2(s SDF2, nx, ny int, lo, hi Vec2) *Grid2 {
	g := &Grid2{Nx: nx, Ny: ny, Lo: lo, Hi: hi, Values: make([]float64, nx*ny)}
	workers := runtime.NumCPU()
	if workers > ny {
		workers = ny
	}
	rowsPerWorker := (ny + workers - 1) / workers
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		j0 := w * rowsPerWorker
		j1 := j0 + rowsPerWorker
		if j1 > ny {
			j1 = ny
		}
		if j0 >= j1 {
			break
		}
		wg.Add(1)
		go func(j0, j1 int) {
			defer wg.Done()
			for j := j0; j < j1; j++ {
				y := lo.Y + float64(j)*(hi.Y-lo.Y)/float64(ny-1)
				row := j * nx
				for i := 0; i < nx; i++ {
					x := lo.X + float64(i)*(hi.X-lo.X)/float64(nx-1)
					g.Values[row+i] = s.Distance(Vec2{x, y})
				}
			}
		}(j0, j1)
	}
	wg.Wait()
	return g
}

// At returns the sampled value at grid node (i, j).
func (g *Grid2) At(i, j int) float64 { return g.Values[j*g.Nx+i] }

// Position returns the world-space position of grid node (i, j).
func (g *Grid2) Position(i, j int) Vec2 {
	return Vec2{
		g.Lo.X + float64(i)*(g.Hi.X-g.Lo.X)/float64(g.Nx-1),
		g.Lo.Y + float64(j)*(g.Hi.Y-g.Lo.Y)/float64(g.Ny-1),
	}
}

// Cell returns the world-space size of one grid cell.
func (g *Grid2) Cell() Vec2 {
	return Vec2{
		(g.Hi.X - g.Lo.X) / float64(g.Nx-1),
		(g.Hi.Y - g.Lo.Y) / float64(g.Ny-1),
	}
}

// Value returns the bilinearly interpolated field value at p.
// p is clamped to the grid bounds.
func (g *Grid2) Value(p Vec2) float64 {
	fx := (p.X - g.Lo.X) / (g.Hi.X - g.Lo.X) * float64(g.Nx-1)
	fy := (p.Y - g.Lo.Y) / (g.Hi.Y - g.Lo.Y) * float64(g.Ny-1)
	fx = math.Max(0, math.Min(float64(g.Nx-1), fx))
	fy = math.Max(0, math.Min(float64(g.Ny-1), fy))
	i0 := int(math.Min(float64(g.Nx-2), math.Floor(fx)))
	j0 := int(math.Min(float64(g.Ny-2), math.Floor(fy)))
	tx := fx - float64(i0)
	ty := fy - float64(j0)
	v00 := g.At(i0, j0)
	v10 := g.At(i0+1, j0)
	v01 := g.At(i0, j0+1)
	v11 := g.At(i0+1, j0+1)
	return (v00*(1-tx)+v10*tx)*(1-ty) + (v01*(1-tx)+v11*tx)*ty
}

// Distance implements SDF2, sampling the rasterized grid.
func (g *Grid2) Distance(p Vec2) float64 { return g.Value(p) }
