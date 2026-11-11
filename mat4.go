package animblend

// Mat4 is a 4x4 transform stored in column-major order: element at
// (row, col) is M[col*4+row]. Vectors are column vectors, so a combined
// local transform reads M = T * R * S.
type Mat4 [16]float64

// IdentityMat4 returns the identity matrix.
func IdentityMat4() Mat4 {
	return Mat4{
		1, 0, 0, 0,
		0, 1, 0, 0,
		0, 0, 1, 0,
		0, 0, 0, 1,
	}
}

// at returns the element at (row, col).
func (m Mat4) at(row, col int) float64 {
	return m[col*4+row]
}

// Mul4 returns the matrix product a*b (b applied first).
func Mul4(a, b Mat4) Mat4 {
	var out Mat4
	for col := 0; col < 4; col++ {
		for row := 0; row < 4; row++ {
			sum := 0.0
			for k := 0; k < 4; k++ {
				sum += a.at(row, k) * b.at(k, col)
			}
			out[col*4+row] = sum
		}
	}
	return out
}

// TransformPoint applies m to a point (implicit w = 1).
func TransformPoint(m Mat4, p Vec3) Vec3 {
	return Vec3{
		X: m.at(0, 0)*p.X + m.at(0, 1)*p.Y + m.at(0, 2)*p.Z + m.at(0, 3),
		Y: m.at(1, 0)*p.X + m.at(1, 1)*p.Y + m.at(1, 2)*p.Z + m.at(1, 3),
		Z: m.at(2, 0)*p.X + m.at(2, 1)*p.Y + m.at(2, 2)*p.Z + m.at(2, 3),
	}
}

// Mat4FromQuat builds a pure rotation matrix from a (unit) quaternion.
func Mat4FromQuat(q Quat) Mat4 {
	x, y, z, w := q.X, q.Y, q.Z, q.W
	xx, yy, zz := x*x, y*y, z*z
	xy, xz, yz := x*y, x*z, y*z
	wx, wy, wz := w*x, w*y, w*z

	return Mat4{
		// column 0
		1 - 2*(yy+zz), 2 * (xy + wz), 2 * (xz - wy), 0,
		// column 1
		2 * (xy - wz), 1 - 2*(xx+zz), 2 * (yz + wx), 0,
		// column 2
		2 * (xz + wy), 2 * (yz - wx), 1 - 2*(xx+yy), 0,
		// column 3
		0, 0, 0, 1,
	}
}

// ComposeTRS builds the local matrix T(translation) * R(rotation) * S(scale).
func ComposeTRS(translation Vec3, rotation Quat, scale Vec3) Mat4 {
	r := Mat4FromQuat(rotation)
	for col := 0; col < 3; col++ {
		for row := 0; row < 3; row++ {
			r[col*4+row] *= scale.component(col)
		}
	}
	r[12] = translation.X
	r[13] = translation.Y
	r[14] = translation.Z
	return r
}

func (v Vec3) component(i int) float64 {
	switch i {
	case 0:
		return v.X
	case 1:
		return v.Y
	default:
		return v.Z
	}
}
