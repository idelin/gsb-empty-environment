package taskpkg

import (
	"errors"
	"math"
)

// Quat is a Hamilton quaternion (w, x, y, z). It rotates column vectors by
// q * v * q^-1.
type Quat struct {
	W float64
	X float64
	Y float64
	Z float64
}

// Quaternion is an alias for Quat.
type Quaternion = Quat

// IdentityQuat returns the identity rotation.
func IdentityQuat() Quat {
	return Quat{W: 1}
}

// IdentityQuaternion returns the identity rotation.
func IdentityQuaternion() Quat {
	return IdentityQuat()
}

// NewQuat constructs a quaternion from its components.
func NewQuat(w, x, y, z float64) Quat {
	return Quat{W: w, X: x, Y: y, Z: z}
}

// NewQuaternion constructs a quaternion from its components.
func NewQuaternion(w, x, y, z float64) Quat {
	return NewQuat(w, x, y, z)
}

// Dot returns the four-dimensional dot product of q and other.
func (q Quat) Dot(other Quat) float64 {
	return q.W*other.W + q.X*other.X + q.Y*other.Y + q.Z*other.Z
}

// Norm returns the Euclidean norm of q.
func (q Quat) Norm() float64 {
	return math.Sqrt(q.Dot(q))
}

// Conjugate returns the conjugate of q.
func (q Quat) Conjugate() Quat {
	return Quat{W: q.W, X: -q.X, Y: -q.Y, Z: -q.Z}
}

// Negate returns -q. It represents the same rotation as q.
func (q Quat) Negate() Quat {
	return Quat{W: -q.W, X: -q.X, Y: -q.Y, Z: -q.Z}
}

// Mul returns the Hamilton product q * other.
func (q Quat) Mul(other Quat) Quat {
	return Quat{
		W: q.W*other.W - q.X*other.X - q.Y*other.Y - q.Z*other.Z,
		X: q.W*other.X + q.X*other.W + q.Y*other.Z - q.Z*other.Y,
		Y: q.W*other.Y - q.X*other.Z + q.Y*other.W + q.Z*other.X,
		Z: q.W*other.Z + q.X*other.Y - q.Y*other.X + q.Z*other.W,
	}
}

// Normalize returns q scaled to unit length.
func (q Quat) Normalize() (Quat, error) {
	if !q.isFinite() {
		return Quat{}, ErrNonFinite
	}
	norm := q.Norm()
	if norm == 0 {
		return Quat{}, ErrInvalidQuaternion
	}
	return Quat{q.W / norm, q.X / norm, q.Y / norm, q.Z / norm}, nil
}

// Rotate rotates v by q. q is normalized internally.
func (q Quat) Rotate(v Vec3) Vec3 {
	normalized, err := q.Normalize()
	if err != nil {
		return Vec3{}
	}
	u := Vec3{normalized.X, normalized.Y, normalized.Z}
	twice := u.Cross(v).Scale(2)
	return v.Add(twice.Scale(normalized.W)).Add(u.Cross(twice))
}

// AxisAngle returns the unit quaternion for a rotation of angle radians around
// axis. The axis is normalized. A zero axis is rejected.
func AxisAngle(axis Vec3, radians float64) (Quat, error) {
	if !axis.isFinite() || math.IsNaN(radians) || math.IsInf(radians, 0) {
		return Quat{}, ErrNonFinite
	}
	length := axis.Length()
	if length == 0 {
		return Quat{}, ErrInvalidQuaternion
	}
	unitAxis := axis.Scale(1 / length)
	half := radians / 2
	sine := math.Sin(half)
	return Quat{
		W: math.Cos(half),
		X: unitAxis.X * sine,
		Y: unitAxis.Y * sine,
		Z: unitAxis.Z * sine,
	}, nil
}

// QuatFromAxisAngle is an alias for AxisAngle.
func QuatFromAxisAngle(axis Vec3, radians float64) (Quat, error) {
	return AxisAngle(axis, radians)
}

// RotationMatrix returns the 3x3 rotation matrix for q.
func (q Quat) RotationMatrix() ([3][3]float64, error) {
	normalized, err := q.Normalize()
	if err != nil {
		return [3][3]float64{}, err
	}
	w, x, y, z := normalized.W, normalized.X, normalized.Y, normalized.Z
	return [3][3]float64{
		{1 - 2*(y*y+z*z), 2 * (x*y - w*z), 2 * (x*z + w*y)},
		{2 * (x*y + w*z), 1 - 2*(x*x+z*z), 2 * (y*z - w*x)},
		{2 * (x*z - w*y), 2 * (y*z + w*x), 1 - 2*(x*x+y*y)},
	}, nil
}

// QuatFromRotationMatrix converts a proper 3x3 rotation matrix to a unit
// quaternion.
func QuatFromRotationMatrix(m [3][3]float64) (Quat, error) {
	for row := 0; row < 3; row++ {
		for column := 0; column < 3; column++ {
			if !finite(m[row][column]) {
				return Quat{}, ErrNonFinite
			}
		}
	}

	trace := m[0][0] + m[1][1] + m[2][2]
	var q Quat
	switch {
	case trace > 0:
		s := math.Sqrt(trace+1) * 2
		q = Quat{
			W: 0.25 * s,
			X: (m[2][1] - m[1][2]) / s,
			Y: (m[0][2] - m[2][0]) / s,
			Z: (m[1][0] - m[0][1]) / s,
		}
	case m[0][0] > m[1][1] && m[0][0] > m[2][2]:
		s := math.Sqrt(1+m[0][0]-m[1][1]-m[2][2]) * 2
		q = Quat{
			W: (m[2][1] - m[1][2]) / s,
			X: 0.25 * s,
			Y: (m[0][1] + m[1][0]) / s,
			Z: (m[0][2] + m[2][0]) / s,
		}
	case m[1][1] > m[2][2]:
		s := math.Sqrt(1+m[1][1]-m[0][0]-m[2][2]) * 2
		q = Quat{
			W: (m[0][2] - m[2][0]) / s,
			X: (m[0][1] + m[1][0]) / s,
			Y: 0.25 * s,
			Z: (m[1][2] + m[2][1]) / s,
		}
	default:
		s := math.Sqrt(1+m[2][2]-m[0][0]-m[1][1]) * 2
		q = Quat{
			W: (m[1][0] - m[0][1]) / s,
			X: (m[0][2] + m[2][0]) / s,
			Y: (m[1][2] + m[2][1]) / s,
			Z: 0.25 * s,
		}
	}
	normalized, err := q.Normalize()
	if err != nil {
		return Quat{}, err
	}
	return canonicalQuat(normalized), nil
}

func (q Quat) isFinite() bool {
	return finite(q.W) && finite(q.X) && finite(q.Y) && finite(q.Z)
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// canonicalQuat chooses one of the two antipodal quaternions q and -q. The
// choice is deterministic so equal rotations produce equal representation.
func canonicalQuat(q Quat) Quat {
	switch {
	case q.W < 0:
		return q.Negate()
	case q.W == 0 && q.X < 0:
		return q.Negate()
	case q.W == 0 && q.X == 0 && q.Y < 0:
		return q.Negate()
	case q.W == 0 && q.X == 0 && q.Y == 0 && q.Z < 0:
		return q.Negate()
	default:
		return q
	}
}

var (
	// ErrNonFinite is returned when a transform, quaternion, weight, or vector
	// contains NaN or infinity.
	ErrNonFinite = errors.New("taskpkg: non-finite input")
	// ErrInvalidQuaternion is returned for a zero or otherwise unusable
	// quaternion.
	ErrInvalidQuaternion = errors.New("taskpkg: invalid quaternion")
)
