package taskpkg

import (
	"errors"
	"math"
)

// DualQuat is a dual quaternion real + epsilon*dual. A unit dual quaternion
// with real·dual == 0 represents a rigid transform.
type DualQuat struct {
	Real Quat
	Dual Quat
}

// DualQuaternion is an alias for DualQuat.
type DualQuaternion = DualQuat

// IdentityDualQuat returns the identity rigid transform.
func IdentityDualQuat() DualQuat {
	return DualQuat{Real: IdentityQuat()}
}

// IdentityDualQuaternion returns the identity rigid transform.
func IdentityDualQuaternion() DualQuat {
	return IdentityDualQuat()
}

// NewDualQuaternion constructs a raw dual quaternion. It does not normalize or
// validate its components; use FromRotationTranslation or Normalize for a
// rigid transform.
func NewDualQuaternion(real, dual Quat) DualQuat {
	return DualQuat{Real: real, Dual: dual}
}

// FromRotationTranslation builds the rigid dual quaternion for the given
// rotation and translation.
func FromRotationTranslation(rotation Quat, translation Vec3) (DualQuat, error) {
	if !translation.isFinite() {
		return DualQuat{}, ErrNonFinite
	}
	normalized, err := rotation.Normalize()
	if err != nil {
		return DualQuat{}, err
	}
	normalized = canonicalQuat(normalized)
	translationQuat := Quat{X: translation.X, Y: translation.Y, Z: translation.Z}
	return DualQuat{
		Real: normalized,
		Dual: translationQuat.Mul(normalized).ScaleQuat(0.5),
	}, nil
}

// FromRigidTransform is an alias for FromRotationTranslation.
func FromRigidTransform(rotation Quat, translation Vec3) (DualQuat, error) {
	return FromRotationTranslation(rotation, translation)
}

// NewRigidTransform is an alias for FromRotationTranslation.
func NewRigidTransform(rotation Quat, translation Vec3) (DualQuat, error) {
	return FromRotationTranslation(rotation, translation)
}

// FromTranslation returns a rigid transform with identity rotation.
func FromTranslation(translation Vec3) DualQuat {
	return DualQuat{
		Real: IdentityQuat(),
		Dual: Quat{X: translation.X / 2, Y: translation.Y / 2, Z: translation.Z / 2},
	}
}

// ScaleQuat multiplies every component by s.
func (q Quat) ScaleQuat(s float64) Quat {
	return Quat{W: q.W * s, X: q.X * s, Y: q.Y * s, Z: q.Z * s}
}

// Negate returns -dq. It represents the same rigid transform as dq.
func (dq DualQuat) Negate() DualQuat {
	return DualQuat{Real: dq.Real.Negate(), Dual: dq.Dual.Negate()}
}

// Normalize returns an equivalent unit dual quaternion whose real part has
// unit length and whose dual part is orthogonal to the real part.
func (dq DualQuat) Normalize() (DualQuat, error) {
	if !dq.Real.isFinite() || !dq.Dual.isFinite() {
		return DualQuat{}, ErrNonFinite
	}
	realNorm := dq.Real.Norm()
	if realNorm == 0 {
		return DualQuat{}, ErrDegenerateDualQuaternion
	}
	real := dq.Real.ScaleQuat(1 / realNorm)
	dual := dq.Dual.ScaleQuat(1 / realNorm)
	projection := real.Dot(dual)
	dual = Quat{
		W: dual.W - projection*real.W,
		X: dual.X - projection*real.X,
		Y: dual.Y - projection*real.Y,
		Z: dual.Z - projection*real.Z,
	}
	if !real.isFinite() || !dual.isFinite() {
		return DualQuat{}, ErrNonFinite
	}
	canonicalReal := canonicalQuat(real)
	if canonicalReal != real {
		dual = dual.Negate()
	}
	return DualQuat{Real: canonicalReal, Dual: dual}, nil
}

// Normalized is a convenience form of Normalize.
func NormalizeDualQuaternion(dq DualQuat) (DualQuat, error) {
	return dq.Normalize()
}

// Rotation returns the rotation represented by dq.
func (dq DualQuat) Rotation() (Quat, error) {
	normalized, err := dq.Normalize()
	if err != nil {
		return Quat{}, err
	}
	return normalized.Real, nil
}

// Translation returns the translation represented by dq.
func (dq DualQuat) Translation() (Vec3, error) {
	normalized, err := dq.Normalize()
	if err != nil {
		return Vec3{}, err
	}
	translation := normalized.Dual.Mul(normalized.Real.Conjugate()).ScaleQuat(2)
	return Vec3{X: translation.X, Y: translation.Y, Z: translation.Z}, nil
}

// Compose returns dq * other. When applied to a column vector, other is
// applied first and dq second.
func (dq DualQuat) Compose(other DualQuat) DualQuat {
	return DualQuat{
		Real: dq.Real.Mul(other.Real),
		Dual: dq.Dual.Mul(other.Real).AddQuat(dq.Real.Mul(other.Dual)),
	}
}

// AddQuat returns q + other.
func (q Quat) AddQuat(other Quat) Quat {
	return Quat{W: q.W + other.W, X: q.X + other.X, Y: q.Y + other.Y, Z: q.Z + other.Z}
}

// Inverse returns the inverse rigid transform.
func (dq DualQuat) Inverse() (DualQuat, error) {
	normalized, err := dq.Normalize()
	if err != nil {
		return DualQuat{}, err
	}
	conjugateReal := normalized.Real.Conjugate()
	conjugateDual := normalized.Dual.Conjugate()
	return DualQuat{
		Real: conjugateReal,
		Dual: conjugateReal.Mul(conjugateDual).Mul(conjugateReal).Negate(),
	}, nil
}

// TransformPoint applies the rigid transform to p.
func (dq DualQuat) TransformPoint(p Vec3) Vec3 {
	normalized, err := dq.Normalize()
	if err != nil {
		return Vec3{}
	}
	rotation := normalized.Real.Rotate(p)
	translation := normalized.Dual.Mul(normalized.Real.Conjugate()).ScaleQuat(2)
	return rotation.Add(Vec3{X: translation.X, Y: translation.Y, Z: translation.Z})
}

// ApplyPoint is an alias for TransformPoint.
func (dq DualQuat) ApplyPoint(p Vec3) Vec3 {
	return dq.TransformPoint(p)
}

// TransformPosition is an alias for TransformPoint.
func (dq DualQuat) TransformPosition(p Vec3) Vec3 {
	return dq.TransformPoint(p)
}

// TransformVector applies only the rotational part of dq to v.
func (dq DualQuat) TransformVector(v Vec3) Vec3 {
	normalized, err := dq.Normalize()
	if err != nil {
		return Vec3{}
	}
	return normalized.Real.Rotate(v)
}

// ApplyVector is an alias for TransformVector.
func (dq DualQuat) ApplyVector(v Vec3) Vec3 {
	return dq.TransformVector(v)
}

// RotateVector is an alias for TransformVector.
func (dq DualQuat) RotateVector(v Vec3) Vec3 {
	return dq.TransformVector(v)
}

// TransformNormal rotates and normalizes n. Translation is deliberately not
// applied to normals.
func (dq DualQuat) TransformNormal(n Vec3) Vec3 {
	rotated := dq.TransformVector(n)
	return rotated.Normalize()
}

// ApplyNormal is an alias for TransformNormal.
func (dq DualQuat) ApplyNormal(n Vec3) Vec3 {
	return dq.TransformNormal(n)
}

// IsRigid reports whether dq is a finite unit dual quaternion with real·dual
// close to zero.
func (dq DualQuat) IsRigid(tolerance float64) bool {
	if !dq.Real.isFinite() || !dq.Dual.isFinite() || math.IsNaN(tolerance) || tolerance < 0 {
		return false
	}
	return math.Abs(dq.Real.Norm()-1) <= tolerance &&
		math.Abs(dq.Real.Dot(dq.Dual)) <= tolerance
}

var (
	// ErrDegenerateDualQuaternion is returned when blending produces a zero
	// real part or the input real part is zero.
	ErrDegenerateDualQuaternion = errors.New("taskpkg: degenerate dual quaternion")
	// ErrNonRigid is returned when a bone transform is not a proper rigid
	// transform.
	ErrNonRigid = errors.New("taskpkg: non-rigid transform")
)
