package taskpkg

import (
	"math"
	"testing"
)

const testTolerance = 1e-9

func requireNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > testTolerance {
		t.Fatalf("got %.15g, want %.15g (difference %.15g)", got, want, math.Abs(got-want))
	}
}

func requireVecNear(t *testing.T, got, want Vec3) {
	t.Helper()
	requireNear(t, got.X, want.X)
	requireNear(t, got.Y, want.Y)
	requireNear(t, got.Z, want.Z)
}

func mustQuat(t *testing.T, axis Vec3, angle float64) Quat {
	t.Helper()
	q, err := AxisAngle(axis, angle)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func mustRigid(t *testing.T, rotation Quat, translation Vec3) DualQuat {
	t.Helper()
	dq, err := FromRotationTranslation(rotation, translation)
	if err != nil {
		t.Fatal(err)
	}
	return dq
}

func TestSingleBoneAnalytic(t *testing.T) {
	rotation := mustQuat(t, Vec3{Z: 1}, math.Pi/2)
	bone := mustRigid(t, rotation, Vec3{X: 1, Y: 2, Z: 3})

	position, err := SkinPoint([]float64{1}, []DualQuat{bone}, Vec3{X: 1})
	if err != nil {
		t.Fatal(err)
	}
	requireVecNear(t, position, Vec3{X: 1, Y: 3, Z: 3})

	normal, err := SkinNormal([]float64{1}, []DualQuat{bone}, Vec3{X: 1})
	if err != nil {
		t.Fatal(err)
	}
	requireVecNear(t, normal, Vec3{Y: 1})
	requireNear(t, normal.Length(), 1)

	translation, err := bone.Translation()
	if err != nil {
		t.Fatal(err)
	}
	requireVecNear(t, translation, Vec3{X: 1, Y: 2, Z: 3})
	requireNear(t, bone.Real.Norm(), 1)
	requireNear(t, bone.Real.Dot(bone.Dual), 0)
}

func TestAntipodalRepresentations(t *testing.T) {
	rotation := mustQuat(t, Vec3{X: 1, Y: 2, Z: 3}, 1.37)
	translation := Vec3{X: -2, Y: 4, Z: 7}
	original := mustRigid(t, rotation, translation)
	negated := original.Negate()

	originalPoint := original.TransformPoint(Vec3{X: 1, Y: 2, Z: 3})
	negatedPoint := negated.TransformPoint(Vec3{X: 1, Y: 2, Z: 3})
	requireVecNear(t, originalPoint, negatedPoint)

	blendedOriginal, err := Blend([]float64{1}, []DualQuat{original})
	if err != nil {
		t.Fatal(err)
	}
	blendedNegated, err := Blend([]float64{1}, []DualQuat{negated})
	if err != nil {
		t.Fatal(err)
	}
	if blendedOriginal != blendedNegated {
		t.Fatalf("antipodal representatives produced different canonical blends: %#v vs %#v", blendedOriginal, blendedNegated)
	}

	fromNegativeQuat := mustRigid(t, rotation.Negate(), translation)
	if fromNegativeQuat != original {
		t.Fatalf("constructor did not canonicalize antipodal rotation: %#v vs %#v", fromNegativeQuat, original)
	}
}

func TestDoubleCoveredBoneBlend(t *testing.T) {
	rotation := mustQuat(t, Vec3{Z: 1}, 0.8)
	a := mustRigid(t, rotation, Vec3{X: 2})
	b := mustRigid(t, rotation.Negate(), Vec3{X: 4})

	blended, err := Blend([]float64{0.25, 0.75}, []DualQuat{a, b})
	if err != nil {
		t.Fatal(err)
	}
	translation, err := blended.Translation()
	if err != nil {
		t.Fatal(err)
	}
	requireVecNear(t, translation, Vec3{X: 3.5})

	point, err := SkinPoint([]float64{0.25, 0.75}, []DualQuat{a, b}, Vec3{X: 1})
	if err != nil {
		t.Fatal(err)
	}
	expected := rotation.Rotate(Vec3{X: 1}).Add(Vec3{X: 3.5})
	requireVecNear(t, point, expected)
}

func TestOneHundredEightyDegreeJointRotation(t *testing.T) {
	positive := mustRigid(t, mustQuat(t, Vec3{Z: 1}, math.Pi/2), Vec3{})
	negative := mustRigid(t, mustQuat(t, Vec3{Z: 1}, -math.Pi/2), Vec3{})
	weights := []float64{0.5, 0.5}
	bones := []DualQuat{positive, negative}

	first, err := SkinPoint(weights, bones, Vec3{X: 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := SkinPoint(weights, bones, Vec3{Y: 1})
	if err != nil {
		t.Fatal(err)
	}
	requireVecNear(t, first, Vec3{X: 1})
	requireVecNear(t, second, Vec3{Y: 1})
	requireNear(t, first.Sub(second).Length(), math.Sqrt2)

	normal, err := SkinNormal(weights, bones, Vec3{X: 1})
	if err != nil {
		t.Fatal(err)
	}
	requireVecNear(t, normal, Vec3{X: 1})
	requireNear(t, normal.Length(), 1)

	blended, err := Blend(weights, bones)
	if err != nil {
		t.Fatal(err)
	}
	requireNear(t, blended.Real.Norm(), 1)
	requireNear(t, blended.Real.Dot(blended.Dual), 0)

	// Linear matrix blending would average the two rotations to a rank-deficient
	// matrix. The dual-quaternion result remains a proper rigid transform.
	rotatedX := mustQuat(t, Vec3{Z: 1}, math.Pi/2).Rotate(Vec3{X: 1})
	rotatedY := mustQuat(t, Vec3{Z: 1}, -math.Pi/2).Rotate(Vec3{Y: 1})
	linearX := rotatedX.Scale(0.5).Add(rotatedY.Scale(0.5))
	if linearX.Length() >= 1 {
		t.Fatalf("test setup should demonstrate linear-skinning collapse, length=%g", linearX.Length())
	}
}

func TestOneHundredEightyDegreeBoneRotation(t *testing.T) {
	rotation := mustQuat(t, Vec3{Z: 1}, math.Pi)
	bone := mustRigid(t, rotation, Vec3{X: 1, Y: 2, Z: 3})

	position, err := SkinPoint([]float64{1}, []DualQuat{bone}, Vec3{X: 2, Y: -1, Z: 4})
	if err != nil {
		t.Fatal(err)
	}
	requireVecNear(t, position, Vec3{X: -1, Y: 3, Z: 7})

	normal, err := SkinNormal([]float64{1}, []DualQuat{bone}, Vec3{X: 1, Y: 2, Z: 0})
	if err != nil {
		t.Fatal(err)
	}
	expectedNormal := Vec3{X: -1, Y: -2, Z: 0}.Normalize()
	requireVecNear(t, normal, expectedNormal)
	requireNear(t, normal.Length(), 1)
}

func TestRigidLengthAndUnitNormals(t *testing.T) {
	xRotation := mustQuat(t, Vec3{X: 1, Y: 1, Z: 0}, 1.1)
	yRotation := mustQuat(t, Vec3{X: 0, Y: 1, Z: 2}, -2.4)
	bones := []DualQuat{
		mustRigid(t, xRotation, Vec3{X: 2, Y: -1, Z: 4}),
		mustRigid(t, yRotation, Vec3{X: -3, Y: 2, Z: 1}),
	}
	weights := []float64{0.35, 0.65}

	first, err := SkinPoint(weights, bones, Vec3{X: 1, Y: 2, Z: 3})
	if err != nil {
		t.Fatal(err)
	}
	second, err := SkinPoint(weights, bones, Vec3{X: -2, Y: 1, Z: 5})
	if err != nil {
		t.Fatal(err)
	}
	inputDistance := Vec3{X: 1, Y: 2, Z: 3}.Sub(Vec3{X: -2, Y: 1, Z: 5}).Length()
	requireNear(t, first.Sub(second).Length(), inputDistance)

	for _, normal := range []Vec3{
		{X: 1},
		{Y: 1},
		{Z: 1},
		{X: 1, Y: 1, Z: 1},
		{X: -2, Y: 4, Z: 1},
	} {
		output, err := SkinNormal(weights, bones, normal)
		if err != nil {
			t.Fatal(err)
		}
		requireNear(t, output.Length(), 1)
	}
}

func TestBonePermutationInvariance(t *testing.T) {
	bones := []DualQuat{
		mustRigid(t, mustQuat(t, Vec3{X: 1, Y: 2, Z: 3}, 0.7), Vec3{X: 1, Y: -2, Z: 3}),
		mustRigid(t, mustQuat(t, Vec3{X: -2, Y: 1, Z: 1}, -1.2), Vec3{X: -3, Y: 2, Z: 1}),
		mustRigid(t, mustQuat(t, Vec3{X: 0, Y: 1, Z: 1}, 2.1), Vec3{X: 4, Y: 5, Z: -6}),
	}
	weights := []float64{0.2, 0.3, 0.5}
	position := Vec3{X: 1.5, Y: -2.5, Z: 3.5}
	normal := Vec3{X: 1, Y: 2, Z: -2}.Normalize()

	firstPoint, err := SkinPoint(weights, bones, position)
	if err != nil {
		t.Fatal(err)
	}
	firstNormal, err := SkinNormal(weights, bones, normal)
	if err != nil {
		t.Fatal(err)
	}

	permutedBones := []DualQuat{bones[2], bones[0], bones[1]}
	permutedWeights := []float64{weights[2], weights[0], weights[1]}
	secondPoint, err := SkinPoint(permutedWeights, permutedBones, position)
	if err != nil {
		t.Fatal(err)
	}
	secondNormal, err := SkinNormal(permutedWeights, permutedBones, normal)
	if err != nil {
		t.Fatal(err)
	}
	requireVecNear(t, firstPoint, secondPoint)
	requireVecNear(t, firstNormal, secondNormal)

	// Equal maximum weights also leave the represented transform unchanged.
	equalWeights := []float64{0.5, 0.5, 0}
	equalPermuted := []float64{0.5, 0.5, 0}
	equalBones := []DualQuat{bones[0], bones[1], bones[2]}
	equalPermutedBones := []DualQuat{bones[1], bones[0], bones[2]}
	equalPointA, err := SkinPoint(equalWeights, equalBones, position)
	if err != nil {
		t.Fatal(err)
	}
	equalPointB, err := SkinPoint(equalPermuted, equalPermutedBones, position)
	if err != nil {
		t.Fatal(err)
	}
	requireVecNear(t, equalPointA, equalPointB)
}

func TestMatrixRoundTripAndLayouts(t *testing.T) {
	rotation := mustQuat(t, Vec3{X: 1, Y: -2, Z: 3}, 1.9)
	translation := Vec3{X: 3, Y: -4, Z: 5}
	matrix, err := RigidMatrix(rotation, translation)
	if err != nil {
		t.Fatal(err)
	}

	from4x4, err := FromMatrix4(matrix)
	if err != nil {
		t.Fatal(err)
	}
	fromFlat, err := FromMatrix4([16]float64{
		matrix[0][0], matrix[0][1], matrix[0][2], matrix[0][3],
		matrix[1][0], matrix[1][1], matrix[1][2], matrix[1][3],
		matrix[2][0], matrix[2][1], matrix[2][2], matrix[2][3],
		matrix[3][0], matrix[3][1], matrix[3][2], matrix[3][3],
	})
	if err != nil {
		t.Fatal(err)
	}
	columnMajor := [16]float64{
		matrix[0][0], matrix[1][0], matrix[2][0], matrix[3][0],
		matrix[0][1], matrix[1][1], matrix[2][1], matrix[3][1],
		matrix[0][2], matrix[1][2], matrix[2][2], matrix[3][2],
		matrix[0][3], matrix[1][3], matrix[2][3], matrix[3][3],
	}
	fromColumnMajor, err := FromMatrix4(columnMajor)
	if err != nil {
		t.Fatal(err)
	}

	for _, dq := range []DualQuat{from4x4, fromFlat, fromColumnMajor} {
		convertedTranslation, err := dq.Translation()
		if err != nil {
			t.Fatal(err)
		}
		requireVecNear(t, convertedTranslation, translation)
		requireVecNear(t, dq.TransformPoint(Vec3{X: 1, Y: 2, Z: 3}), from4x4.TransformPoint(Vec3{X: 1, Y: 2, Z: 3}))
	}
}

func TestRejectZeroWeights(t *testing.T) {
	bones := []DualQuat{IdentityDualQuat(), IdentityDualQuat()}
	if _, err := Blend([]float64{0, 0}, bones); err == nil {
		t.Fatal("expected zero total weight to be rejected")
	} else if err != ErrZeroTotalWeight {
		t.Fatalf("expected ErrZeroTotalWeight, got %v", err)
	}
	if _, err := Blend([]float64{0.5}, bones); err != ErrWeightCount {
		t.Fatalf("expected ErrWeightCount, got %v", err)
	}
	if _, err := SkinNormal([]float64{0, 0}, bones, Vec3{}); err != ErrZeroTotalWeight {
		t.Fatalf("expected normal skinning to validate weights before a zero normal, got %v", err)
	}
}

func TestRejectInvalidWeights(t *testing.T) {
	bone := IdentityDualQuat()
	tests := []struct {
		name    string
		weights []float64
		want    error
	}{
		{"negative", []float64{-0.2, 1.2}, ErrNegativeWeight},
		{"not normalized", []float64{0.2, 0.2}, ErrWeightSum},
		{"nan", []float64{math.NaN(), 1}, ErrNonFinite},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Blend(test.weights, []DualQuat{bone, bone})
			if err != test.want {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}

func TestRejectNonRigidMatrices(t *testing.T) {
	identity := Mat4{
		{1, 0, 0, 0},
		{0, 1, 0, 0},
		{0, 0, 1, 0},
		{0, 0, 0, 1},
	}
	if _, err := FromMatrix4(identity); err != nil {
		t.Fatalf("identity should be accepted: %v", err)
	}

	scaled := identity
	scaled[0][0] = 2
	if _, err := FromMatrix4(scaled); err != ErrNonRigid {
		t.Fatalf("scale matrix: got %v, want ErrNonRigid", err)
	}

	sheared := identity
	sheared[0][1] = 0.5
	if _, err := FromMatrix4(sheared); err != ErrNonRigid {
		t.Fatalf("shear matrix: got %v, want ErrNonRigid", err)
	}

	reflected := identity
	reflected[0][0] = -1
	if _, err := FromMatrix4(reflected); err != ErrNonRigid {
		t.Fatalf("reflection matrix: got %v, want ErrNonRigid", err)
	}

	projection := identity
	projection[3][2] = 0.25
	projection[2][3] = -0.5
	if _, err := FromMatrix4(projection); err != ErrNonRigid {
		t.Fatalf("non-affine matrix: got %v, want ErrNonRigid", err)
	}
}

func TestRejectNonRigidBones(t *testing.T) {
	scaledBone := DualQuat{Real: Quat{W: 2}}
	if _, err := Blend([]float64{1}, []DualQuat{scaledBone}); err != ErrNonRigid {
		t.Fatalf("scaled bone: got %v, want ErrNonRigid", err)
	}

	nonOrthogonalDual := DualQuat{
		Real: IdentityQuat(),
		Dual: Quat{W: 1},
	}
	if _, err := Blend([]float64{1}, []DualQuat{nonOrthogonalDual}); err != ErrNonRigid {
		t.Fatalf("non-orthogonal dual part: got %v, want ErrNonRigid", err)
	}
}

func TestNormalIgnoresTranslation(t *testing.T) {
	rotation := mustQuat(t, Vec3{X: 1, Y: 2, Z: 3}, 0.9)
	left := mustRigid(t, rotation, Vec3{X: -100, Y: 20, Z: 30})
	right := mustRigid(t, rotation, Vec3{X: 100, Y: 20, Z: 30})
	normal := Vec3{X: 1, Y: -1, Z: 2}
	weights := []float64{0.4, 0.6}

	leftNormal, err := SkinNormal([]float64{1}, []DualQuat{left}, normal)
	if err != nil {
		t.Fatal(err)
	}
	rightNormal, err := SkinNormal([]float64{1}, []DualQuat{right}, normal)
	if err != nil {
		t.Fatal(err)
	}
	requireVecNear(t, leftNormal, rightNormal)

	blendedNormal, err := SkinNormal(weights, []DualQuat{left, right}, normal)
	if err != nil {
		t.Fatal(err)
	}
	requireVecNear(t, blendedNormal, leftNormal)
}
