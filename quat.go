package animblend

import "math"

// Quat is a unit quaternion (X, Y, Z imaginary parts; W real part) describing
// a rotation in local bone space.
type Quat struct {
	X, Y, Z, W float64
}

// IdentityQuat returns the identity rotation.
func IdentityQuat() Quat {
	return Quat{W: 1}
}

// QuatFromAxisAngle builds the rotation of angle radians around the (unit)
// axis (ax, ay, az).
func QuatFromAxisAngle(ax, ay, az, angle float64) Quat {
	half := angle * 0.5
	s := math.Sin(half)
	return Quat{X: ax * s, Y: ay * s, Z: az * s, W: math.Cos(half)}
}

// Dot returns the inner product of two quaternions.
func QuatDot(a, b Quat) float64 {
	return a.X*b.X + a.Y*b.Y + a.Z*b.Z + a.W*b.W
}

// Norm returns the Euclidean length of the quaternion.
func (q Quat) Norm() float64 {
	return math.Sqrt(q.X*q.X + q.Y*q.Y + q.Z*q.Z + q.W*q.W)
}

// Normalize returns a unit quaternion, or the identity for a near-zero one.
func Normalize(q Quat) Quat {
	n := q.Norm()
	if n < 1e-12 {
		return IdentityQuat()
	}
	inv := 1 / n
	return Quat{X: q.X * inv, Y: q.Y * inv, Z: q.Z * inv, W: q.W * inv}
}

// Mul returns the Hamilton product a*b, i.e. the rotation b applied first and
// then a.
func Mul(a, b Quat) Quat {
	return Quat{
		X: a.W*b.X + a.X*b.W + a.Y*b.Z - a.Z*b.Y,
		Y: a.W*b.Y - a.X*b.Z + a.Y*b.W + a.Z*b.X,
		Z: a.W*b.Z + a.X*b.Y - a.Y*b.X + a.Z*b.W,
		W: a.W*b.W - a.X*b.X - a.Y*b.Y - a.Z*b.Z,
	}
}

// Conjugate returns the quaternion conjugate.
func (q Quat) Conjugate() Quat {
	return Quat{X: -q.X, Y: -q.Y, Z: -q.Z, W: q.W}
}

// Inverse returns the inverse rotation; for a unit quaternion this equals the
// conjugate.
func Inverse(q Quat) Quat {
	n2 := q.X*q.X + q.Y*q.Y + q.Z*q.Z + q.W*q.W
	if n2 < 1e-24 {
		return IdentityQuat()
	}
	inv := 1 / n2
	return Quat{X: -q.X * inv, Y: -q.Y * inv, Z: -q.Z * inv, W: q.W * inv}
}

// Negate flips all components. q and -q describe the same rotation.
func Negate(q Quat) Quat {
	return Quat{X: -q.X, Y: -q.Y, Z: -q.Z, W: -q.W}
}

// Rotate rotates vector v by quaternion q (assumed unit).
func Rotate(q Quat, v Vec3) Vec3 {
	// q * v * q^-1 using the expanded form.
	uv := cross3(Vec3{q.X, q.Y, q.Z}, v)
	uuv := cross3(Vec3{q.X, q.Y, q.Z}, uv)
	uv = uv.scale(2 * q.W)
	uuv = uuv.scale(2)
	return Vec3{
		X: v.X + uv.X + uuv.X,
		Y: v.Y + uv.Y + uuv.Y,
		Z: v.Z + uv.Z + uuv.Z,
	}
}

// Slerp interpolates between two unit quaternions along the shortest great
// arc. When the inputs are (near) antipodal an orthogonal fallback axis is
// used instead of dividing by sin(pi), so the result stays finite and never
// flips onto the long way around.
//
// t is clamped to [0, 1]. The result is normalized before returning.
func Slerp(a, b Quat, t float64) Quat {
	if t <= 0 {
		return Normalize(a)
	}
	if t >= 1 {
		return Normalize(b)
	}

	a = Normalize(a)
	b = Normalize(b)

	cosAngle := QuatDot(a, b)
	// Pick the representative of b on the same hemisphere as a so the
	// interpolation always follows the shortest path.
	if cosAngle < 0 {
		b = Negate(b)
		cosAngle = -cosAngle
	}

	// Close together: linear interpolation + normalization is numerically
	// cheaper and avoids sin(0) division.
	if cosAngle > 1-1e-6 {
		return Normalize(Quat{
			X: a.X + t*(b.X-a.X),
			Y: a.Y + t*(b.Y-a.Y),
			Z: a.Z + t*(b.Z-a.Z),
			W: a.W + t*(b.W-a.W),
		})
	}

	angle := math.Acos(clamp(cosAngle, -1, 1))
	sinAngle := math.Sin(angle)

	// Exactly (or virtually) antipodal: build an orthogonal quaternion h and
	// rotate half the angle toward it, keeping a finite, hemisphere-consistent
	// result instead of dividing by sin(pi) ~ 0.
	if math.Abs(sinAngle) < 1e-10 {
		h := orthogonalQuat(a)
		half := t * angle
		s := math.Sin(half)
		c := math.Cos(half)
		return Normalize(Quat{
			X: c*a.X + s*h.X,
			Y: c*a.Y + s*h.Y,
			Z: c*a.Z + s*h.Z,
			W: c*a.W + s*h.W,
		})
	}

	wa := math.Sin((1-t)*angle) / sinAngle
	wb := math.Sin(t*angle) / sinAngle
	return Normalize(Quat{
		X: wa*a.X + wb*b.X,
		Y: wa*a.Y + wb*b.Y,
		Z: wa*a.Z + wb*b.Z,
		W: wa*a.W + wb*b.W,
	})
}

// orthogonalQuat returns a unit quaternion orthogonal to q, used as a
// numerically stable fallback for antipodal slerp.
func orthogonalQuat(q Quat) Quat {
	var axis Vec3
	if math.Abs(q.X) > 0.5 || math.Abs(q.Y) > 0.5 {
		axis = Vec3{Z: 1}
	} else {
		axis = Vec3{X: 1}
	}
	v := Vec3{q.X, q.Y, q.Z}
	orth := cross3(v, axis)
	n := orth.length()
	orth = orth.scale(1 / n)
	return Quat{X: orth.X, Y: orth.Y, Z: orth.Z, W: 0}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
