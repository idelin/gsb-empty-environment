package sdf

// Grid2 is a rasterized 2D signed distance field over a rectangular
// region [Min, Max]. Distances are stored row-major: Data[y*Nx+x].
type Grid2 struct {
	Nx, Ny   int
	Min, Max Vec2
	Data     []float64
}

// Rasterize2 samples sdf on an nx*ny uniform grid covering [min, max].
// Grid nodes sit on cell corners; node (i,j) maps to world position
// Min + (i/(nx-1), j/(ny-1)) * (Max-Min).
func Rasterize2(sdf SDF2, nx, ny int, min, max Vec2) *Grid2 {
	g := &Grid2{Nx: nx, Ny: ny, Min: min, Max: max, Data: make([]float64, nx*ny)}
	for j := 0; j < ny; j++ {
		y := min.Y + float64(j)/float64(ny-1)*(max.Y-min.Y)
		for i := 0; i < nx; i++ {
			x := min.X + float64(i)/float64(nx-1)*(max.X-min.X)
			g.Data[j*nx+i] = sdf(Vec2{x, y})
		}
	}
	return g
}

// At returns the raw sampled distance at grid node (i, j).
func (g *Grid2) At(i, j int) float64 { return g.Data[j*g.Nx+i] }

// Sample bilinearly interpolates the field at world position p.
// Positions outside the grid are clamped to the boundary.
func (g *Grid2) Sample(p Vec2) float64 {
	fx := (p.X - g.Min.X) / (g.Max.X - g.Min.X) * float64(g.Nx-1)
	fy := (p.Y - g.Min.Y) / (g.Max.Y - g.Min.Y) * float64(g.Ny-1)
	fx = minf(maxf(fx, 0), float64(g.Nx-1))
	fy = minf(maxf(fy, 0), float64(g.Ny-1))
	i0 := int(fx)
	j0 := int(fy)
	if i0 >= g.Nx-1 {
		i0 = g.Nx - 2
	}
	if j0 >= g.Ny-1 {
		j0 = g.Ny - 2
	}
	tx := fx - float64(i0)
	ty := fy - float64(j0)
	c00 := g.At(i0, j0)
	c10 := g.At(i0+1, j0)
	c01 := g.At(i0, j0+1)
	c11 := g.At(i0+1, j0+1)
	return (c00*(1-tx)+c10*tx)*(1-ty) + (c01*(1-tx)+c11*tx)*ty
}

// Gradient returns the field gradient at p via central differences,
// which for a signed distance field approximates the surface normal.
func (g *Grid2) Gradient(p Vec2) Vec2 {
	hx := (g.Max.X - g.Min.X) / float64(g.Nx-1)
	hy := (g.Max.Y - g.Min.Y) / float64(g.Ny-1)
	dx := g.Sample(Vec2{p.X + hx, p.Y}) - g.Sample(Vec2{p.X - hx, p.Y})
	dy := g.Sample(Vec2{p.X, p.Y + hy}) - g.Sample(Vec2{p.X, p.Y - hy})
	return Vec2{dx / (2 * hx), dy / (2 * hy)}
}

// Grid3 is a rasterized 3D signed distance field over an axis-aligned
// box [Min, Max]. Distances are stored with x fastest:
// Data[(z*Ny+y)*Nx+x].
type Grid3 struct {
	Nx, Ny, Nz int
	Min, Max   Vec3
	Data       []float64
}

// Rasterize3 samples sdf on an nx*ny*nz uniform grid covering [min, max].
func Rasterize3(sdf SDF3, nx, ny, nz int, min, max Vec3) *Grid3 {
	g := &Grid3{
		Nx: nx, Ny: ny, Nz: nz,
		Min: min, Max: max,
		Data: make([]float64, nx*ny*nz),
	}
	for k := 0; k < nz; k++ {
		z := min.Z + float64(k)/float64(nz-1)*(max.Z-min.Z)
		for j := 0; j < ny; j++ {
			y := min.Y + float64(j)/float64(ny-1)*(max.Y-min.Y)
			for i := 0; i < nx; i++ {
				x := min.X + float64(i)/float64(nx-1)*(max.X-min.X)
				g.Data[(k*ny+j)*nx+i] = sdf(Vec3{x, y, z})
			}
		}
	}
	return g
}

// At returns the raw sampled distance at grid node (i, j, k).
func (g *Grid3) At(i, j, k int) float64 { return g.Data[(k*g.Ny+j)*g.Nx+i] }

// Sample trilinearly interpolates the field at world position p.
// Positions outside the grid are clamped to the boundary.
func (g *Grid3) Sample(p Vec3) float64 {
	fx := (p.X - g.Min.X) / (g.Max.X - g.Min.X) * float64(g.Nx-1)
	fy := (p.Y - g.Min.Y) / (g.Max.Y - g.Min.Y) * float64(g.Ny-1)
	fz := (p.Z - g.Min.Z) / (g.Max.Z - g.Min.Z) * float64(g.Nz-1)
	fx = minf(maxf(fx, 0), float64(g.Nx-1))
	fy = minf(maxf(fy, 0), float64(g.Ny-1))
	fz = minf(maxf(fz, 0), float64(g.Nz-1))
	i0, j0, k0 := int(fx), int(fy), int(fz)
	if i0 >= g.Nx-1 {
		i0 = g.Nx - 2
	}
	if j0 >= g.Ny-1 {
		j0 = g.Ny - 2
	}
	if k0 >= g.Nz-1 {
		k0 = g.Nz - 2
	}
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

// Gradient returns the field gradient at p via central differences,
// which for a signed distance field approximates the surface normal.
func (g *Grid3) Gradient(p Vec3) Vec3 {
	hx := (g.Max.X - g.Min.X) / float64(g.Nx-1)
	hy := (g.Max.Y - g.Min.Y) / float64(g.Ny-1)
	hz := (g.Max.Z - g.Min.Z) / float64(g.Nz-1)
	dx := g.Sample(Vec3{p.X + hx, p.Y, p.Z}) - g.Sample(Vec3{p.X - hx, p.Y, p.Z})
	dy := g.Sample(Vec3{p.X, p.Y + hy, p.Z}) - g.Sample(Vec3{p.X, p.Y - hy, p.Z})
	dz := g.Sample(Vec3{p.X, p.Y, p.Z + hz}) - g.Sample(Vec3{p.X, p.Y, p.Z - hz})
	return Vec3{dx / (2 * hx), dy / (2 * hy), dz / (2 * hz)}
}
