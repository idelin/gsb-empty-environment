package taskpkg

import "math"

// Epsilon is the tolerance used for vector degeneracy tests.
const Epsilon = 1e-10

// Vec3 is a 3-component vector in animation host coordinates.
type Vec3 struct {
	X, Y, Z float64
}

// Quat is a Hamilton-product quaternion with a scalar W component.
type Quat struct {
	X, Y, Z, W float64
}

// Vec builds a Vec3 from its components.
func Vec(x, y, z float64) Vec3 { return Vec3{X: x, Y: y, Z: z} }

// Add returns a+b.
func Add(a, b Vec3) Vec3 { return Vec3{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }

// Sub returns a-b.
func Sub(a, b Vec3) Vec3 { return Vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }

// Scale returns a*s.
func Scale(a Vec3, s float64) Vec3 { return Vec3{a.X * s, a.Y * s, a.Z * s} }

// Neg returns -a.
func Neg(a Vec3) Vec3 { return Vec3{-a.X, -a.Y, -a.Z} }

// Dot returns the scalar dot product.
func Dot(a, b Vec3) float64 { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }

// Cross returns the cross product a x b.
func Cross(a, b Vec3) Vec3 {
	return Vec3{
		a.Y*b.Z - a.Z*b.Y,
		a.Z*b.X - a.X*b.Z,
		a.X*b.Y - a.Y*b.X,
	}
}

// Length returns the Euclidean norm.
func Length(a Vec3) float64 { return math.Sqrt(Dot(a, a)) }

// LengthSq returns the squared Euclidean norm.
func LengthSq(a Vec3) float64 { return Dot(a, a) }

// Distance returns the Euclidean distance between two points.
func Distance(a, b Vec3) float64 { return Length(Sub(a, b)) }

// Normalize returns the unit vector. It returns the zero vector for
// degenerate input instead of producing NaNs.
func Normalize(a Vec3) Vec3 {
	n := Length(a)
	if n < Epsilon {
		return Vec3{}
	}
	return Scale(a, 1/n)
}

// Lerp performs linear interpolation between a and b.
func Lerp(a, b Vec3, t float64) Vec3 {
	return Vec3{
		a.X + (b.X-a.X)*t,
		a.Y + (b.Y-a.Y)*t,
		a.Z + (b.Z-a.Z)*t,
	}
}

// Angle returns the unsigned angle in [0, pi] between two vectors.
func Angle(a, b Vec3) float64 {
	cos := Dot(Normalize(a), Normalize(b))
	cos = clampScalar(cos, -1, 1)
	return math.Acos(cos)
}

// PerpTo returns a unit vector perpendicular to a. When a is parallel (or
// anti-parallel) to the fixed reference axis, a fallback world axis breaks
// the degeneracy so pole handling stays deterministic.
func PerpTo(a, ref Vec3) Vec3 {
	n := Normalize(a)
	r := Normalize(ref)
	if math.Abs(Dot(n, r)) > 1-Epsilon {
		r = Normalize(fallbackAxis(ref))
	}
	return Normalize(Cross(n, r))
}

func fallbackAxis(ref Vec3) Vec3 {
	r := Normalize(ref)
	for _, cand := range []Vec3{{X: 1}, {Y: 1}, {Z: 1}} {
		if math.Abs(Dot(r, cand)) < 1-Epsilon {
			return cand
		}
	}
	return Vec3{X: 0, Z: 1}
}

// IdentityQuat returns the unit quaternion.
func IdentityQuat() Quat { return Quat{W: 1} }

// QuatFromAxisAngle builds the quaternion for rotating angle radians around
// the (normalized) axis.
func QuatFromAxisAngle(axis Vec3, angle float64) Quat {
	if Length(axis) < Epsilon || math.Abs(angle) < Epsilon {
		return IdentityQuat()
	}
	n := Normalize(axis)
	h := angle * 0.5
	s := math.Sin(h)
	return Quat{X: n.X * s, Y: n.Y * s, Z: n.Z * s, W: math.Cos(h)}
}

// QuatFromTo returns the shortest-arc quaternion rotating unit vector u onto
// unit vector v. Anti-parallel input rotates pi around a fixed-reference
// perpendicular axis so the result stays stable.
func QuatFromTo(u, v, ref Vec3) Quat {
	nu := Normalize(u)
	nv := Normalize(v)
	d := clampScalar(Dot(nu, nv), -1, 1)
	if d > 1-Epsilon {
		return IdentityQuat()
	}
	if d < -1+Epsilon {
		return QuatFromAxisAngle(PerpTo(nu, ref), math.Pi)
	}
	return QuatFromAxisAngle(Normalize(Cross(nu, nv)), math.Acos(d))
}

// QuatMul returns the Hamilton product a*b (apply b first, then a).
func QuatMul(a, b Quat) Quat {
	return Quat{
		X: a.W*b.X + a.X*b.W + a.Y*b.Z - a.Z*b.Y,
		Y: a.W*b.Y - a.X*b.Z + a.Y*b.W + a.Z*b.X,
		Z: a.W*b.Z + a.X*b.Y - a.Y*b.X + a.Z*b.W,
		W: a.W*b.W - a.X*b.X - a.Y*b.Y - a.Z*b.Z,
	}
}

// QuatInverse returns the conjugate for a unit quaternion.
func QuatInverse(q Quat) Quat {
	return Quat{X: -q.X, Y: -q.Y, Z: -q.Z, W: q.W}
}

// Rotate applies quaternion q to vector a.
func Rotate(q Quat, a Vec3) Vec3 {
	qv := Vec3{q.X, q.Y, q.Z}
	t := Scale(Cross(qv, a), 2)
	return Add(a, Add(Scale(t, q.W), Cross(qv, t)))
}

// RotateVector rotates a direction by q and renormalizes defensively.
func RotateVector(q Quat, a Vec3) Vec3 {
	return Normalize(Rotate(q, a))
}

// QuatAngle returns the unsigned rotation angle between two unit quaternions.
func QuatAngle(a, b Quat) float64 {
	d := math.Abs(a.X*b.X + a.Y*b.Y + a.Z*b.Z + a.W*b.W)
	return 2 * math.Acos(clampScalar(d, 0, 1))
}

func clampScalar(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
