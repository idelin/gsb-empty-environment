package taskpkg

import (
	"fmt"
	"math"
)

// Vec2 is a two-dimensional vector.
type Vec2 struct {
	X, Y float64
}

// Vec3 is a three-dimensional vector.
type Vec3 struct {
	X, Y, Z float64
}

// Vec4 is a homogeneous four-dimensional vector.
type Vec4 struct {
	X, Y, Z, W float64
}

// Mat3 is a row-major 3x3 matrix.
type Mat3 [3][3]float64

// Mat4 is a row-major 4x4 matrix.
type Mat4 [4][4]float64

// Identity3 returns the 3x3 identity matrix.
func Identity3() Mat3 {
	return Mat3{
		{1, 0, 0},
		{0, 1, 0},
		{0, 0, 1},
	}
}

// Identity4 returns the 4x4 identity matrix.
func Identity4() Mat4 {
	return Mat4{
		{1, 0, 0, 0},
		{0, 1, 0, 0},
		{0, 0, 1, 0},
		{0, 0, 0, 1},
	}
}

// Translation returns a translation matrix.
func Translation(x, y, z float64) Mat4 {
	m := Identity4()
	m[0][3] = x
	m[1][3] = y
	m[2][3] = z
	return m
}

// Scale returns a non-uniform scale matrix.
func Scale(x, y, z float64) Mat4 {
	return Mat4{
		{x, 0, 0, 0},
		{0, y, 0, 0},
		{0, 0, z, 0},
		{0, 0, 0, 1},
	}
}

// MulVec multiplies a 3x3 matrix by a column vector.
func (m Mat3) MulVec(v Vec3) Vec3 {
	return Vec3{
		X: m[0][0]*v.X + m[0][1]*v.Y + m[0][2]*v.Z,
		Y: m[1][0]*v.X + m[1][1]*v.Y + m[1][2]*v.Z,
		Z: m[2][0]*v.X + m[2][1]*v.Y + m[2][2]*v.Z,
	}
}

// Mul returns m*b.
func (m Mat3) Mul(b Mat3) Mat3 {
	var out Mat3
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			for k := 0; k < 3; k++ {
				out[i][j] += m[i][k] * b[k][j]
			}
		}
	}
	return out
}

// Determinant returns det(m).
func (m Mat3) Determinant() float64 {
	return m[0][0]*(m[1][1]*m[2][2]-m[1][2]*m[2][1]) -
		m[0][1]*(m[1][0]*m[2][2]-m[1][2]*m[2][0]) +
		m[0][2]*(m[1][0]*m[2][1]-m[1][1]*m[2][0])
}

// Inverse returns the inverse of m. It reports false for singular or
// non-finite matrices.
func (m Mat3) Inverse() (Mat3, bool) {
	det := m.Determinant()
	if !isFinite(det) || math.Abs(det) <= 1e-15 {
		return Mat3{}, false
	}
	invDet := 1.0 / det
	var out Mat3
	out[0][0] = (m[1][1]*m[2][2] - m[1][2]*m[2][1]) * invDet
	out[0][1] = (m[0][2]*m[2][1] - m[0][1]*m[2][2]) * invDet
	out[0][2] = (m[0][1]*m[1][2] - m[0][2]*m[1][1]) * invDet
	out[1][0] = (m[1][2]*m[2][0] - m[1][0]*m[2][2]) * invDet
	out[1][1] = (m[0][0]*m[2][2] - m[0][2]*m[2][0]) * invDet
	out[1][2] = (m[0][2]*m[1][0] - m[0][0]*m[1][2]) * invDet
	out[2][0] = (m[1][0]*m[2][1] - m[1][1]*m[2][0]) * invDet
	out[2][1] = (m[0][1]*m[2][0] - m[0][0]*m[2][1]) * invDet
	out[2][2] = (m[0][0]*m[1][1] - m[0][1]*m[1][0]) * invDet
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if !isFinite(out[i][j]) {
				return Mat3{}, false
			}
		}
	}
	return out, true
}

// Transpose returns m^T.
func (m Mat3) Transpose() Mat3 {
	var out Mat3
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			out[i][j] = m[j][i]
		}
	}
	return out
}

// MulVec multiplies a 4x4 matrix by a homogeneous column vector.
func (m Mat4) MulVec(v Vec4) Vec4 {
	return Vec4{
		X: m[0][0]*v.X + m[0][1]*v.Y + m[0][2]*v.Z + m[0][3]*v.W,
		Y: m[1][0]*v.X + m[1][1]*v.Y + m[1][2]*v.Z + m[1][3]*v.W,
		Z: m[2][0]*v.X + m[2][1]*v.Y + m[2][2]*v.Z + m[2][3]*v.W,
		W: m[3][0]*v.X + m[3][1]*v.Y + m[3][2]*v.Z + m[3][3]*v.W,
	}
}

// Mul returns m*b.
func (m Mat4) Mul(b Mat4) Mat4 {
	var out Mat4
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			for k := 0; k < 4; k++ {
				out[i][j] += m[i][k] * b[k][j]
			}
		}
	}
	return out
}

// Inverse returns the inverse of m using Gaussian elimination with partial
// pivoting. It reports false for singular or non-finite matrices.
func (m Mat4) Inverse() (Mat4, bool) {
	a := m
	inv := Identity4()
	for col := 0; col < 4; col++ {
		pivot := col
		pivotAbs := math.Abs(a[col][col])
		for row := col + 1; row < 4; row++ {
			if v := math.Abs(a[row][col]); v > pivotAbs {
				pivot = row
				pivotAbs = v
			}
		}
		if !isFinite(pivotAbs) || pivotAbs <= 1e-15 {
			return Mat4{}, false
		}
		if pivot != col {
			a[col], a[pivot] = a[pivot], a[col]
			inv[col], inv[pivot] = inv[pivot], inv[col]
		}
		d := a[col][col]
		for j := 0; j < 4; j++ {
			a[col][j] /= d
			inv[col][j] /= d
		}
		for row := 0; row < 4; row++ {
			if row == col {
				continue
			}
			f := a[row][col]
			if f == 0 {
				continue
			}
			for j := 0; j < 4; j++ {
				a[row][j] -= f * a[col][j]
				inv[row][j] -= f * inv[col][j]
			}
		}
	}
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			if !isFinite(inv[i][j]) {
				return Mat4{}, false
			}
		}
	}
	return inv, true
}

// Affine3 is a world-space, invertible affine transform. Linear must have
// strictly positive determinant: reflections and negative scales are rejected
// by the reprojector because they reverse the geometric normal orientation.
type Affine3 struct {
	Linear      Mat3
	Translation Vec3
}

// Affine3FromMat4 converts a 4x4 affine matrix to Affine3. It rejects
// non-affine bottom rows and non-finite values.
func Affine3FromMat4(m Mat4) (Affine3, bool) {
	if !m.Finite() {
		return Affine3{}, false
	}
	const eps = 1e-9
	if math.Abs(m[3][0]) > eps || math.Abs(m[3][1]) > eps ||
		math.Abs(m[3][2]) > eps || math.Abs(m[3][3]-1) > eps {
		return Affine3{}, false
	}
	return Affine3{
		Linear: Mat3{
			{m[0][0], m[0][1], m[0][2]},
			{m[1][0], m[1][1], m[1][2]},
			{m[2][0], m[2][1], m[2][2]},
		},
		Translation: Vec3{X: m[0][3], Y: m[1][3], Z: m[2][3]},
	}, true
}

// Matrix returns the equivalent 4x4 affine matrix.
func (a Affine3) Matrix() Mat4 {
	return Mat4{
		{a.Linear[0][0], a.Linear[0][1], a.Linear[0][2], a.Translation.X},
		{a.Linear[1][0], a.Linear[1][1], a.Linear[1][2], a.Translation.Y},
		{a.Linear[2][0], a.Linear[2][1], a.Linear[2][2], a.Translation.Z},
		{0, 0, 0, 1},
	}
}

// TransformPoint applies the affine transform to a world-space point.
func (a Affine3) TransformPoint(v Vec3) Vec3 {
	p := a.Linear.MulVec(v)
	return Vec3{
		X: p.X + a.Translation.X,
		Y: p.Y + a.Translation.Y,
		Z: p.Z + a.Translation.Z,
	}
}

// NormalMatrix returns the inverse-transpose of the linear part.
func (a Affine3) NormalMatrix() (Mat3, bool) {
	inv, ok := a.Linear.Inverse()
	if !ok {
		return Mat3{}, false
	}
	return inv.Transpose(), true
}

// Determinant returns the determinant of the linear part.
func (a Affine3) Determinant() float64 {
	return a.Linear.Determinant()
}

// Finite reports whether all matrix entries are finite.
func (m Mat4) Finite() bool {
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			if !isFinite(m[i][j]) {
				return false
			}
		}
	}
	return true
}

// Finite reports whether all components are finite.
func (v Vec3) Finite() bool {
	return isFinite(v.X) && isFinite(v.Y) && isFinite(v.Z)
}

// Length returns the Euclidean length of v.
func (v Vec3) Length() float64 {
	return math.Sqrt(v.X*v.X + v.Y*v.Y + v.Z*v.Z)
}

// Normalized returns v/|v| and reports false when |v| is too small.
func (v Vec3) Normalized() (Vec3, bool) {
	l := v.Length()
	if !isFinite(l) || l <= 1e-15 {
		return Vec3{}, false
	}
	return Vec3{X: v.X / l, Y: v.Y / l, Z: v.Z / l}, true
}

// Dot returns the dot product of a and b.
func (v Vec3) Dot(b Vec3) float64 {
	return v.X*b.X + v.Y*b.Y + v.Z*b.Z
}

// Add returns a+b.
func (v Vec3) Add(b Vec3) Vec3 {
	return Vec3{X: v.X + b.X, Y: v.Y + b.Y, Z: v.Z + b.Z}
}

// Sub returns a-b.
func (v Vec3) Sub(b Vec3) Vec3 {
	return Vec3{X: v.X - b.X, Y: v.Y - b.Y, Z: v.Z - b.Z}
}

// Scale returns v*s.
func (v Vec3) Scale(s float64) Vec3 {
	return Vec3{X: v.X * s, Y: v.Y * s, Z: v.Z * s}
}

// String formats a Vec3 for debugging.
func (v Vec3) String() string {
	return fmt.Sprintf("(%g, %g, %g)", v.X, v.Y, v.Z)
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
