package shadow

import "math"

// Vec3 is a world-space vector. All quantities in the package share the same
// world-space units.
type Vec3 struct {
	X, Y, Z float64
}

func (v Vec3) add(o Vec3) Vec3      { return Vec3{v.X + o.X, v.Y + o.Y, v.Z + o.Z} }
func (v Vec3) sub(o Vec3) Vec3      { return Vec3{v.X - o.X, v.Y - o.Y, v.Z - o.Z} }
func (v Vec3) scale(s float64) Vec3 { return Vec3{v.X * s, v.Y * s, v.Z * s} }
func (v Vec3) dot(o Vec3) float64   { return v.X*o.X + v.Y*o.Y + v.Z*o.Z }
func (v Vec3) cross(o Vec3) Vec3 {
	return Vec3{
		v.Y*o.Z - v.Z*o.Y,
		v.Z*o.X - v.X*o.Z,
		v.X*o.Y - v.Y*o.X,
	}
}
func (v Vec3) lengthSquared() float64 { return v.dot(v) }
func (v Vec3) length() float64        { return math.Sqrt(v.lengthSquared()) }

// Mat4 is a row-major 4x4 matrix: m[r][c]. Multiplication follows standard
// column-vector conventions so that MVP*p works as expected.
type Mat4 [4][4]float64

func identity() Mat4 {
	var m Mat4
	m[0][0], m[1][1], m[2][2], m[3][3] = 1, 1, 1, 1
	return m
}

// mul returns a*b.
func mul(a, b Mat4) Mat4 {
	var out Mat4
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			s := 0.0
			for k := 0; k < 4; k++ {
				s += a[r][k] * b[k][c]
			}
			out[r][c] = s
		}
	}
	return out
}

// invert computes a general inverse via cofactors.
func invert(a Mat4) (Mat4, error) {
	const n = 4
	var inv Mat4
	for col := 0; col < n; col++ {
		for row := 0; row < n; row++ {
			// Minor with row "row" and column "col" removed.
			minor := make([][]float64, 0, n-1)
			for r := 0; r < n; r++ {
				if r == row {
					continue
				}
				line := make([]float64, 0, n-1)
				for c := 0; c < n; c++ {
					if c == col {
						continue
					}
					line = append(line, a[r][c])
				}
				minor = append(minor, line)
			}
			cof := det3(minor)
			if (row+col)%2 == 1 {
				cof = -cof
			}
			// Transpose is folded in here: cofactor of (row,col) lands at
			// inv[col][row].
			inv[col][row] = cof
		}
	}
	det := 0.0
	for c := 0; c < n; c++ {
		det += a[0][c] * inv[c][0]
	}
	if det == 0 || math.IsNaN(det) || math.IsInf(det, 0) {
		return Mat4{}, ErrSingularMatrix
	}
	invDet := 1 / det
	for r := 0; r < 4; r++ {
		for c := 0; c < n; c++ {
			inv[r][c] *= invDet
		}
	}
	return inv, nil
}

func det3(m [][]float64) float64 {
	return m[0][0]*(m[1][1]*m[2][2]-m[1][2]*m[2][1]) -
		m[0][1]*(m[1][0]*m[2][2]-m[1][2]*m[2][0]) +
		m[0][2]*(m[1][0]*m[2][1]-m[1][1]*m[2][0])
}

// transformPoint applies m to a point with w = 1, dividing by the resulting w.
func transformPoint(m Mat4, p Vec3) Vec3 {
	x := m[0][0]*p.X + m[0][1]*p.Y + m[0][2]*p.Z + m[0][3]
	y := m[1][0]*p.X + m[1][1]*p.Y + m[1][2]*p.Z + m[1][3]
	z := m[2][0]*p.X + m[2][1]*p.Y + m[2][2]*p.Z + m[2][3]
	w := m[3][0]*p.X + m[3][1]*p.Y + m[3][2]*p.Z + m[3][3]
	return Vec3{x / w, y / w, z / w}
}

// LookAt builds a right-handed view matrix looking from eye along forward
// (the camera -Z axis) with up as an orientation hint.
func LookAt(eye, forward, up Vec3) Mat4 {
	f := forward.scale(1 / forward.length())
	// Right-handed camera: the -Z axis points along forward, so the z basis
	// vector is -forward; a point in front then yields negative view z.
	z := f.scale(-1)
	x := up.cross(z)
	xl := x.length()
	if xl == 0 {
		// Parallel up/forward: pick an arbitrary stable perpendicular.
		x = Vec3{Z: 1}.cross(z)
		xl = x.length()
	}
	x = x.scale(1 / xl)
	y := z.cross(x)
	y = y.scale(1 / y.length())
	var r Mat4
	r[0][0], r[0][1], r[0][2] = x.X, x.Y, x.Z
	r[1][0], r[1][1], r[1][2] = y.X, y.Y, y.Z
	r[2][0], r[2][1], r[2][2] = z.X, z.Y, z.Z
	r[3][3] = 1
	var t Mat4
	t[0][0], t[1][1], t[2][2], t[3][3] = 1, 1, 1, 1
	t[0][3] = -eye.X
	t[1][3] = -eye.Y
	t[2][3] = -eye.Z
	return mul(r, t)
}

// Perspective builds a standard perspective projection with NDC z in [0,1].
func Perspective(fovY, aspect, near, far float64) Mat4 {
	f := 1 / math.Tan(fovY/2)
	var m Mat4
	m[0][0] = f / aspect
	m[1][1] = f
	m[2][2] = far / (near - far)
	m[2][3] = -(far * near) / (far - near)
	m[3][2] = -1
	return m
}

// PerspectiveReverseZ builds a reverse-Z perspective projection: NDC z=1 at
// the near plane and NDC z=0 at the far plane.
func PerspectiveReverseZ(fovY, aspect, near, far float64) Mat4 {
	f := 1 / math.Tan(fovY/2)
	var m Mat4
	m[0][0] = f / aspect
	m[1][1] = f
	m[2][2] = near / (far - near)
	m[2][3] = (far * near) / (far - near)
	m[3][2] = -1
	return m
}

// ortho builds an off-center orthographic projection with NDC z in [0,1],
// where -near maps to 0 and -far maps to 1 (near, far are positive distances).
func ortho(left, right, bottom, top, near, far float64) Mat4 {
	var m Mat4
	m[0][0] = 2 / (right - left)
	m[0][3] = -(right + left) / (right - left)
	m[1][1] = 2 / (top - bottom)
	m[1][3] = -(top + bottom) / (top - bottom)
	m[2][2] = -1 / (far - near)
	m[2][3] = -near / (far - near)
	m[3][3] = 1
	return m
}

// orthoReverseZ builds the reverse-Z counterpart of ortho: -near maps to 1
// and -far maps to 0.
func orthoReverseZ(left, right, bottom, top, near, far float64) Mat4 {
	var m Mat4
	m[0][0] = 2 / (right - left)
	m[0][3] = -(right + left) / (right - left)
	m[1][1] = 2 / (top - bottom)
	m[1][3] = -(top + bottom) / (top - bottom)
	m[2][2] = 1 / (far - near)
	m[2][3] = far / (far - near)
	m[3][3] = 1
	return m
}
