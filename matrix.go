package taskpkg

import (
	"math"
	"reflect"
)

// Mat4 is a row-major 4x4 matrix. The last row of an affine matrix is
// (0, 0, 0, 1).
type Mat4 [4][4]float64

// Matrix4 is an alias for Mat4.
type Matrix4 = Mat4

// Matrix4Input accepts the common row-major or flat matrix representations
// used by Go renderers. Flat 16-element matrices are detected as row-major or
// column-major from their affine row.
type Matrix4Input interface {
	~[4][4]float64 | ~[4][4]float32 | ~[16]float64 | ~[16]float32
}

// At returns the matrix element at row and column.
func (m Mat4) At(row, column int) float64 {
	return m[row][column]
}

// RigidMatrix returns the row-major 4x4 matrix for a rotation and translation.
func RigidMatrix(rotation Quat, translation Vec3) (Mat4, error) {
	normalized, err := rotation.Normalize()
	if err != nil {
		return Mat4{}, err
	}
	if !translation.isFinite() {
		return Mat4{}, ErrNonFinite
	}
	r, err := normalized.RotationMatrix()
	if err != nil {
		return Mat4{}, err
	}
	return Mat4{
		{r[0][0], r[0][1], r[0][2], translation.X},
		{r[1][0], r[1][1], r[1][2], translation.Y},
		{r[2][0], r[2][1], r[2][2], translation.Z},
		{0, 0, 0, 1},
	}, nil
}

// RotationTranslationMatrix is an alias for RigidMatrix.
func RotationTranslationMatrix(rotation Quat, translation Vec3) (Mat4, error) {
	return RigidMatrix(rotation, translation)
}

// FromMatrix4 converts a rigid 4x4 matrix to a dual quaternion. Matrices with
// scale, shear, reflection, or a non-affine last row are rejected.
func FromMatrix4[M Matrix4Input](m M) (DualQuat, error) {
	rows, err := normalizeMatrixInput(m)
	if err != nil {
		return DualQuat{}, err
	}
	if err := validateRigidRows(rows); err != nil {
		return DualQuat{}, err
	}
	rotation := [3][3]float64{
		{rows[0][0], rows[0][1], rows[0][2]},
		{rows[1][0], rows[1][1], rows[1][2]},
		{rows[2][0], rows[2][1], rows[2][2]},
	}
	q, err := QuatFromRotationMatrix(rotation)
	if err != nil {
		return DualQuat{}, ErrNonRigid
	}
	translation := Vec3{rows[0][3], rows[1][3], rows[2][3]}
	return FromRotationTranslation(q, translation)
}

// FromMat4 is an alias for FromMatrix4.
func FromMat4[M Matrix4Input](m M) (DualQuat, error) {
	return FromMatrix4(m)
}

// FromMatrix is an alias for FromMatrix4.
func FromMatrix[M Matrix4Input](m M) (DualQuat, error) {
	return FromMatrix4(m)
}

// NewDualQuaternionFromMatrix is an alias for FromMatrix4.
func NewDualQuaternionFromMatrix[M Matrix4Input](m M) (DualQuat, error) {
	return FromMatrix4(m)
}

// FromMatrix16 converts a flat 16-element matrix. Row-major and column-major
// layouts are both accepted.
func FromMatrix16(m [16]float64) (DualQuat, error) {
	return FromMatrix4(m)
}

// FromMat4x4 converts a row-major or column-major 4x4 matrix.
func FromMat4x4(m [4][4]float64) (DualQuat, error) {
	return FromMatrix4(m)
}

// ToMat4 returns the row-major matrix represented by dq.
func (dq DualQuat) ToMat4() (Mat4, error) {
	normalized, err := dq.Normalize()
	if err != nil {
		return Mat4{}, err
	}
	translation, err := normalized.Translation()
	if err != nil {
		return Mat4{}, err
	}
	return RigidMatrix(normalized.Real, translation)
}

// ToMatrix4 is an alias for ToMat4.
func (dq DualQuat) ToMatrix4() (Mat4, error) {
	return dq.ToMat4()
}

// IsRigid reports whether m is a proper rigid transform within tolerance.
func (m Mat4) IsRigid(tolerance float64) bool {
	if math.IsNaN(tolerance) || tolerance < 0 {
		return false
	}
	rows := [4][4]float64(m)
	return validateRigidRows(rows, tolerance) == nil
}

const defaultMatrixTolerance = 1e-6

func normalizeMatrixInput[M Matrix4Input](m M) ([4][4]float64, error) {
	switch value := any(m).(type) {
	case [4][4]float64:
		return normalize4x4(value)
	case [4][4]float32:
		return normalize4x4(float64Matrix(value))
	case [16]float64:
		return normalizeFlat16(value)
	case [16]float32:
		var converted [16]float64
		for i, component := range value {
			converted[i] = float64(component)
		}
		return normalizeFlat16(converted)
	}

	value := reflect.ValueOf(m)
	if value.Kind() != reflect.Array {
		return [4][4]float64{}, ErrNonRigid
	}
	switch value.Len() {
	case 4:
		var rows [4][4]float64
		for row := 0; row < 4; row++ {
			rowValue := value.Index(row)
			if rowValue.Kind() != reflect.Array || rowValue.Len() != 4 {
				return [4][4]float64{}, ErrNonRigid
			}
			for column := 0; column < 4; column++ {
				rows[row][column] = numericValue(rowValue.Index(column))
			}
		}
		return normalize4x4(rows)
	case 16:
		var flat [16]float64
		for i := 0; i < 16; i++ {
			flat[i] = numericValue(value.Index(i))
		}
		return normalizeFlat16(flat)
	default:
		return [4][4]float64{}, ErrNonRigid
	}
}

func float64Matrix(m [4][4]float32) [4][4]float64 {
	var result [4][4]float64
	for row := 0; row < 4; row++ {
		for column := 0; column < 4; column++ {
			result[row][column] = float64(m[row][column])
		}
	}
	return result
}

func numericValue(value reflect.Value) float64 {
	switch value.Kind() {
	case reflect.Float32, reflect.Float64:
		return value.Float()
	default:
		return math.NaN()
	}
}

func normalize4x4(m [4][4]float64) ([4][4]float64, error) {
	if isAffineLastRow(m) {
		return m, nil
	}
	transposed := transpose4x4(m)
	if isAffineLastRow(transposed) {
		return transposed, nil
	}
	return [4][4]float64{}, ErrNonRigid
}

func normalizeFlat16(m [16]float64) ([4][4]float64, error) {
	rowMajor := [4][4]float64{
		{m[0], m[1], m[2], m[3]},
		{m[4], m[5], m[6], m[7]},
		{m[8], m[9], m[10], m[11]},
		{m[12], m[13], m[14], m[15]},
	}
	columnMajor := transpose4x4(rowMajor)

	rowAffine := isAffineLastRow(rowMajor)
	columnAffine := isAffineLastRow(columnMajor)
	if rowAffine {
		if validateRigidRows(rowMajor, defaultMatrixTolerance) == nil {
			return rowMajor, nil
		}
		if !columnAffine {
			return [4][4]float64{}, ErrNonRigid
		}
	}
	if columnAffine {
		if validateRigidRows(columnMajor, defaultMatrixTolerance) == nil {
			return columnMajor, nil
		}
	}
	return [4][4]float64{}, ErrNonRigid
}

func transpose4x4(m [4][4]float64) [4][4]float64 {
	var result [4][4]float64
	for row := 0; row < 4; row++ {
		for column := 0; column < 4; column++ {
			result[row][column] = m[column][row]
		}
	}
	return result
}

func isAffineLastRow(m [4][4]float64) bool {
	tolerance := defaultMatrixTolerance
	return finite(m[3][0]) && finite(m[3][1]) && finite(m[3][2]) && finite(m[3][3]) &&
		math.Abs(m[3][0]) <= tolerance &&
		math.Abs(m[3][1]) <= tolerance &&
		math.Abs(m[3][2]) <= tolerance &&
		math.Abs(m[3][3]-1) <= tolerance
}

func validateRigidRows(m [4][4]float64, tolerances ...float64) error {
	tolerance := defaultMatrixTolerance
	if len(tolerances) > 0 {
		tolerance = tolerances[0]
	}
	for row := 0; row < 4; row++ {
		for column := 0; column < 4; column++ {
			if !finite(m[row][column]) {
				return ErrNonFinite
			}
		}
	}
	if !isAffineLastRow(m) {
		return ErrNonRigid
	}
	for first := 0; first < 3; first++ {
		for second := first; second < 3; second++ {
			dot := m[0][first]*m[0][second] + m[1][first]*m[1][second] + m[2][first]*m[2][second]
			expected := 0.0
			if first == second {
				expected = 1
			}
			if math.Abs(dot-expected) > tolerance {
				return ErrNonRigid
			}
		}
	}
	determinant :=
		m[0][0]*(m[1][1]*m[2][2]-m[1][2]*m[2][1]) -
			m[0][1]*(m[1][0]*m[2][2]-m[1][2]*m[2][0]) +
			m[0][2]*(m[1][0]*m[2][1]-m[1][1]*m[2][0])
	if math.Abs(determinant-1) > tolerance*3 {
		return ErrNonRigid
	}
	return nil
}
