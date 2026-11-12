package taskpkg

import (
	"errors"
	"math"
)

// ErrWeightCount is returned when a vertex has a different number of weights
// and bones.
var ErrWeightCount = errors.New("taskpkg: weight and bone counts differ")

// ErrNegativeWeight is returned when any vertex weight is negative.
var ErrNegativeWeight = errors.New("taskpkg: negative vertex weight")

// ErrZeroTotalWeight is returned when all weights for a vertex are zero.
var ErrZeroTotalWeight = errors.New("taskpkg: zero total vertex weight")

// ErrWeightSum is returned when a vertex's weights do not sum to one.
var ErrWeightSum = errors.New("taskpkg: vertex weights do not sum to one")

// ErrNoBones is returned when a skinning operation has no bone transforms.
var ErrNoBones = errors.New("taskpkg: no bone transforms")

const weightTolerance = 1e-6

// ValidateWeights verifies that weights are finite, non-negative, contain a
// positive total, and sum to one.
func ValidateWeights(weights []float64) error {
	if len(weights) == 0 {
		return ErrZeroTotalWeight
	}
	sum := 0.0
	for _, weight := range weights {
		if !finite(weight) {
			return ErrNonFinite
		}
		if weight < 0 {
			return ErrNegativeWeight
		}
		sum += weight
	}
	if sum == 0 {
		return ErrZeroTotalWeight
	}
	if math.Abs(sum-1) > weightTolerance {
		return ErrWeightSum
	}
	return nil
}

// NormalizeWeights checks that weights are finite and non-negative, rejects a
// zero total, and returns weights scaled to sum to one.
func NormalizeWeights(weights []float64) ([]float64, float64, error) {
	if len(weights) == 0 {
		return nil, 0, ErrZeroTotalWeight
	}
	sum := 0.0
	for _, weight := range weights {
		if !finite(weight) {
			return nil, 0, ErrNonFinite
		}
		if weight < 0 {
			return nil, 0, ErrNegativeWeight
		}
		sum += weight
	}
	if sum == 0 {
		return nil, 0, ErrZeroTotalWeight
	}
	normalized := make([]float64, len(weights))
	for i, weight := range weights {
		normalized[i] = weight / sum
	}
	return normalized, sum, nil
}

// BlendDualQuaternions blends rigid bone transforms with the given per-vertex
// weights. Weights must be non-negative and sum to one. Quaternion signs are
// aligned to the maximum-weight bone before blending, and the result is
// normalized so its real and dual parts are orthogonal.
func BlendDualQuaternions(weights []float64, bones []DualQuat) (DualQuat, error) {
	if len(bones) == 0 {
		return DualQuat{}, ErrNoBones
	}
	if len(weights) != len(bones) {
		return DualQuat{}, ErrWeightCount
	}
	if err := ValidateWeights(weights); err != nil {
		return DualQuat{}, err
	}

	reference := 0
	for i := 1; i < len(weights); i++ {
		if weights[i] > weights[reference] {
			reference = i
		}
	}

	real := Quat{}
	dual := Quat{}
	for i, bone := range bones {
		if !bone.IsRigid(weightTolerance) {
			return DualQuat{}, ErrNonRigid
		}
		aligned := bone
		if bone.Real.Dot(bones[reference].Real) < 0 {
			aligned = bone.Negate()
		}
		weight := weights[i]
		real.W += weight * aligned.Real.W
		real.X += weight * aligned.Real.X
		real.Y += weight * aligned.Real.Y
		real.Z += weight * aligned.Real.Z
		dual.W += weight * aligned.Dual.W
		dual.X += weight * aligned.Dual.X
		dual.Y += weight * aligned.Dual.Y
		dual.Z += weight * aligned.Dual.Z
	}

	blended, err := DualQuat{Real: real, Dual: dual}.Normalize()
	if err != nil {
		return DualQuat{}, err
	}
	if !blended.IsRigid(weightTolerance) {
		return DualQuat{}, ErrNonRigid
	}
	return blended, nil
}

// Blend is an alias for BlendDualQuaternions.
func Blend(weights []float64, bones []DualQuat) (DualQuat, error) {
	return BlendDualQuaternions(weights, bones)
}

// BlendBones is an alias for BlendDualQuaternions.
func BlendBones(weights []float64, bones []DualQuat) (DualQuat, error) {
	return BlendDualQuaternions(weights, bones)
}

// BlendTransforms is an alias for BlendDualQuaternions.
func BlendTransforms(weights []float64, bones []DualQuat) (DualQuat, error) {
	return BlendDualQuaternions(weights, bones)
}

// SkinPoint transforms a vertex position by blending the bone transforms.
func SkinPoint(weights []float64, bones []DualQuat, position Vec3) (Vec3, error) {
	if !position.isFinite() {
		return Vec3{}, ErrNonFinite
	}
	blended, err := BlendDualQuaternions(weights, bones)
	if err != nil {
		return Vec3{}, err
	}
	return blended.TransformPoint(position), nil
}

// SkinPosition is an alias for SkinPoint.
func SkinPosition(weights []float64, bones []DualQuat, position Vec3) (Vec3, error) {
	return SkinPoint(weights, bones, position)
}

// SkinNormal rotates and normalizes a vertex normal. It never applies the
// blended translation to a normal.
func SkinNormal(weights []float64, bones []DualQuat, normal Vec3) (Vec3, error) {
	if !normal.isFinite() {
		return Vec3{}, ErrNonFinite
	}
	blended, err := BlendDualQuaternions(weights, bones)
	if err != nil {
		return Vec3{}, err
	}
	if normal.Length() == 0 {
		return Vec3{}, nil
	}
	return blended.TransformNormal(normal), nil
}

// SkinVertex transforms a position and normal with one blend. The normal path
// uses only the rotational part of the blended transform.
func SkinVertex(weights []float64, bones []DualQuat, position, normal Vec3) (Vec3, Vec3, error) {
	if !position.isFinite() || !normal.isFinite() {
		return Vec3{}, Vec3{}, ErrNonFinite
	}
	blended, err := BlendDualQuaternions(weights, bones)
	if err != nil {
		return Vec3{}, Vec3{}, err
	}
	return blended.TransformPoint(position), blended.TransformNormal(normal), nil
}

// BlendMatrices converts rigid matrices to dual quaternions and blends them.
func BlendMatrices(weights []float64, matrices [][4][4]float64) (DualQuat, error) {
	if len(weights) != len(matrices) {
		return DualQuat{}, ErrWeightCount
	}
	bones := make([]DualQuat, len(matrices))
	for i, matrix := range matrices {
		bone, err := FromMatrix4(matrix)
		if err != nil {
			return DualQuat{}, err
		}
		bones[i] = bone
	}
	return BlendDualQuaternions(weights, bones)
}

// BlendMatrix16 converts flat rigid matrices and blends them.
func BlendMatrix16(weights []float64, matrices [][16]float64) (DualQuat, error) {
	if len(weights) != len(matrices) {
		return DualQuat{}, ErrWeightCount
	}
	bones := make([]DualQuat, len(matrices))
	for i, matrix := range matrices {
		bone, err := FromMatrix4(matrix)
		if err != nil {
			return DualQuat{}, err
		}
		bones[i] = bone
	}
	return BlendDualQuaternions(weights, bones)
}

// SkinPointMatrices is the matrix-input form of SkinPoint.
func SkinPointMatrices(weights []float64, matrices [][4][4]float64, position Vec3) (Vec3, error) {
	blended, err := BlendMatrices(weights, matrices)
	if err != nil {
		return Vec3{}, err
	}
	return blended.TransformPoint(position), nil
}

// SkinNormalMatrices is the matrix-input form of SkinNormal.
func SkinNormalMatrices(weights []float64, matrices [][4][4]float64, normal Vec3) (Vec3, error) {
	blended, err := BlendMatrices(weights, matrices)
	if err != nil {
		return Vec3{}, err
	}
	if normal.Length() == 0 {
		return Vec3{}, nil
	}
	return blended.TransformNormal(normal), nil
}
