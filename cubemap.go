package taskpkg

import (
	"errors"
	"math"
)

// Vec3 is a unit-length direction on the sphere.
type Vec3 struct {
	X, Y, Z float64
}

func (v Vec3) Add(o Vec3) Vec3      { return Vec3{v.X + o.X, v.Y + o.Y, v.Z + o.Z} }
func (v Vec3) Sub(o Vec3) Vec3      { return Vec3{v.X - o.X, v.Y - o.Y, v.Z - o.Z} }
func (v Vec3) Scale(s float64) Vec3 { return Vec3{v.X * s, v.Y * s, v.Z * s} }
func (v Vec3) Dot(o Vec3) float64   { return v.X*o.X + v.Y*o.Y + v.Z*o.Z }

func (v Vec3) Cross(o Vec3) Vec3 {
	return Vec3{
		v.Y*o.Z - v.Z*o.Y,
		v.Z*o.X - v.X*o.Z,
		v.X*o.Y - v.Y*o.X,
	}
}

func (v Vec3) Length() float64 { return math.Sqrt(v.Dot(v)) }

func (v Vec3) Normalize() Vec3 {
	length := v.Length()
	if length == 0 {
		return Vec3{}
	}
	return v.Scale(1 / length)
}

// Face indices with a fixed priority order. Directions landing exactly on a
// seam or corner are owned by the face with the smallest index.
const (
	FacePosX  = 0
	FaceNegX  = 1
	FacePosY  = 2
	FaceNegY  = 3
	FacePosZ  = 4
	FaceNegZ  = 5
	FaceCount = 6
)

// faceFrame is a right-handed orthonormal basis. A canonical cube-map point
// (u, v) with u, v in [-1, 1] maps to direction u*uAxis + v*vAxis + normal.
type faceFrame struct {
	normal Vec3
	uAxis  Vec3
	vAxis  Vec3
}

func (f faceFrame) direction(u, v float64) Vec3 {
	return f.uAxis.Scale(u).Add(f.vAxis.Scale(v)).Add(f.normal).Normalize()
}

// faces lists the six cube faces in the fixed priority order. Every basis is
// a pure rotation matrix (det = +1) so solid angles are preserved under the
// per-face rotations/permutations.
var faces = [FaceCount]faceFrame{
	FacePosX: {normal: Vec3{1, 0, 0}, uAxis: Vec3{0, 0, -1}, vAxis: Vec3{0, 1, 0}},
	FaceNegX: {normal: Vec3{-1, 0, 0}, uAxis: Vec3{0, 0, 1}, vAxis: Vec3{0, 1, 0}},
	FacePosY: {normal: Vec3{0, 1, 0}, uAxis: Vec3{-1, 0, 0}, vAxis: Vec3{0, 0, 1}},
	FaceNegY: {normal: Vec3{0, -1, 0}, uAxis: Vec3{-1, 0, 0}, vAxis: Vec3{0, 0, -1}},
	FacePosZ: {normal: Vec3{0, 0, 1}, uAxis: Vec3{1, 0, 0}, vAxis: Vec3{0, 1, 0}},
	FaceNegZ: {normal: Vec3{0, 0, -1}, uAxis: Vec3{-1, 0, 0}, vAxis: Vec3{0, 1, 0}},
}

// CubeMap holds the per-face luminance grids of a cube environment map.
// Luminance values must be non-negative.
type CubeMap struct {
	n         int
	luminance [FaceCount][]float64
}

// NewCubeMap validates the six grids and returns a cube map. Every face must
// contain n*n non-negative luminance values, laid out row-major with the
// first row at v = +1 and the first column at u = -1.
func NewCubeMap(n int, faceLuminance [FaceCount][]float64) (*CubeMap, error) {
	if n <= 0 {
		return nil, errors.New("taskpkg: cube map resolution must be positive")
	}
	count := n * n
	for face := 0; face < FaceCount; face++ {
		if len(faceLuminance[face]) != count {
			return nil, errors.New("taskpkg: every cube face must contain n*n values")
		}
		for _, luminance := range faceLuminance[face] {
			if math.IsNaN(luminance) || luminance < 0 {
				return nil, errors.New("taskpkg: cube map luminance must be non-negative and finite")
			}
			if math.IsInf(luminance, 0) {
				return nil, errors.New("taskpkg: cube map luminance must be finite")
			}
		}
	}
	copied := [FaceCount][]float64{}
	for face := 0; face < FaceCount; face++ {
		copied[face] = append([]float64(nil), faceLuminance[face]...)
	}
	return &CubeMap{n: n, luminance: copied}, nil
}

// Resolution returns the per-side pixel count of each square face.
func (c *CubeMap) Resolution() int { return c.n }

// Luminance returns the non-negative luminance of pixel (i, j) on a face.
func (c *CubeMap) Luminance(face, i, j int) float64 {
	return c.luminance[face][j*c.n+i]
}

// pixelBounds returns the canonical cube-map (u, v) bounds of pixel (i, j).
// i increases with u, j increases downward (decreasing v).
func (c *CubeMap) pixelBounds(i, j int) (u0, u1, v0, v1 float64) {
	scale := 2 / float64(c.n)
	u0 = -1 + float64(i)*scale
	u1 = u0 + scale
	v1 = 1 - float64(j)*scale
	v0 = v1 - scale
	return u0, u1, v0, v1
}

// SolidAngle returns the exact solid angle subtended by pixel (i, j) of the
// given face, derived by integrating dOmega = du dv / (1 + u^2 + v^2)^(3/2)
// over the pixel's spherical rectangle:
//
//	Omega = F(u1,v1) - F(u0,v1) - F(u1,v0) + F(u0,v0),
//	F(u,v) = atan2(u*v, sqrt(1 + u^2 + v^2)).
func (c *CubeMap) SolidAngle(face, i, j int) float64 {
	u0, u1, v0, v1 := c.pixelBounds(i, j)
	return solidAngleRect(u0, u1, v0, v1)
}

func solidAngleTerm(u, v float64) float64 {
	return math.Atan2(u*v, math.Sqrt(1+u*u+v*v))
}

func solidAngleRect(u0, u1, v0, v1 float64) float64 {
	return solidAngleTerm(u1, v1) - solidAngleTerm(u0, v1) -
		solidAngleTerm(u1, v0) + solidAngleTerm(u0, v0)
}
