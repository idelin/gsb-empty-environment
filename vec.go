package taskpkg

import "math"

// Vec3 is a three-dimensional vector using float64 precision.
type Vec3 struct {
	X, Y, Z float64
}

// Vec returns a new vector.
func Vec(x, y, z float64) Vec3 {
	return Vec3{X: x, Y: y, Z: z}
}

func (v Vec3) Add(o Vec3) Vec3 {
	return Vec3{X: v.X + o.X, Y: v.Y + o.Y, Z: v.Z + o.Z}
}

func (v Vec3) Sub(o Vec3) Vec3 {
	return Vec3{X: v.X - o.X, Y: v.Y - o.Y, Z: v.Z - o.Z}
}

func (v Vec3) Mul(s float64) Vec3 {
	return Vec3{X: v.X * s, Y: v.Y * s, Z: v.Z * s}
}

func (v Vec3) Neg() Vec3 {
	return Vec3{X: -v.X, Y: -v.Y, Z: -v.Z}
}

func (v Vec3) Dot(o Vec3) float64 {
	return v.X*o.X + v.Y*o.Y + v.Z*o.Z
}

func (v Vec3) Cross(o Vec3) Vec3 {
	return Vec3{
		X: v.Y*o.Z - v.Z*o.Y,
		Y: v.Z*o.X - v.X*o.Z,
		Z: v.X*o.Y - v.Y*o.X,
	}
}

func (v Vec3) LengthSquared() float64 {
	return v.Dot(v)
}

func (v Vec3) Length() float64 {
	return math.Hypot(math.Hypot(v.X, v.Y), v.Z)
}

// Normalize returns the unit vector in v's direction. It returns the zero
// vector when v is zero or non-finite.
func (v Vec3) Normalize() Vec3 {
	l := v.Length()
	if l == 0 || math.IsNaN(l) || math.IsInf(l, 0) {
		return Vec3{}
	}
	return v.Mul(1 / l)
}

func (v Vec3) IsFinite() bool {
	return isFinite(v.X) && isFinite(v.Y) && isFinite(v.Z)
}

func isFinite(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0)
}

// Mat3 is a row-major 3x3 matrix. Vectors are treated as column vectors, so
// (m * v)[i] = sum_j m[i][j] * v[j].
type Mat3 [3][3]float64

func IdentityMat3() Mat3 {
	return Mat3{
		{1, 0, 0},
		{0, 1, 0},
		{0, 0, 1},
	}
}

func (m Mat3) MulVec(v Vec3) Vec3 {
	return Vec3{
		X: m[0][0]*v.X + m[0][1]*v.Y + m[0][2]*v.Z,
		Y: m[1][0]*v.X + m[1][1]*v.Y + m[1][2]*v.Z,
		Z: m[2][0]*v.X + m[2][1]*v.Y + m[2][2]*v.Z,
	}
}

func (m Mat3) Mul(n Mat3) Mat3 {
	var r Mat3
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			r[i][j] = m[i][0]*n[0][j] + m[i][1]*n[1][j] + m[i][2]*n[2][j]
		}
	}
	return r
}

func (m Mat3) Transpose() Mat3 {
	return Mat3{
		{m[0][0], m[1][0], m[2][0]},
		{m[0][1], m[1][1], m[2][1]},
		{m[0][2], m[1][2], m[2][2]},
	}
}

// Determinant returns the determinant of m in float64 arithmetic. Very small
// determinants may underflow to zero even when the represented matrix is
// invertible; use Inverse when that distinction matters.
func (m Mat3) Determinant() float64 {
	return m[0][0]*(m[1][1]*m[2][2]-m[1][2]*m[2][1]) -
		m[0][1]*(m[1][0]*m[2][2]-m[1][2]*m[2][0]) +
		m[0][2]*(m[1][0]*m[2][1]-m[1][1]*m[2][0])
}

// DeterminantSign returns -1, 0, or +1 without underflowing for matrices
// whose entries have extremely different magnitudes.
func (m Mat3) DeterminantSign() int {
	var n Mat3
	for i := 0; i < 3; i++ {
		scale := math.Max(math.Abs(m[i][0]), math.Max(math.Abs(m[i][1]), math.Abs(m[i][2])))
		if scale == 0 || !isFinite(scale) {
			return 0
		}
		for j := 0; j < 3; j++ {
			n[i][j] = m[i][j] / scale
		}
	}
	d := n.Determinant()
	switch {
	case d > 0:
		return 1
	case d < 0:
		return -1
	default:
		return 0
	}
}

// Inverse returns the matrix inverse. It uses Gauss-Jordan elimination with
// partial pivoting, which remains usable for matrices with very small (but
// non-zero) scales. ok is false for singular or non-finite matrices.
func (m Mat3) Inverse() (inv Mat3, ok bool) {
	var a [3][6]float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			a[i][j] = m[i][j]
		}
		a[i][3+i] = 1
	}

	for col := 0; col < 3; col++ {
		pivot := col
		pivotAbs := math.Abs(a[pivot][col])
		for row := col + 1; row < 3; row++ {
			candidate := math.Abs(a[row][col])
			if candidate > pivotAbs {
				pivot = row
				pivotAbs = candidate
			}
		}
		if pivotAbs == 0 || !isFinite(pivotAbs) {
			return Mat3{}, false
		}
		if pivot != col {
			a[pivot], a[col] = a[col], a[pivot]
		}

		p := a[col][col]
		if p == 0 || !isFinite(p) {
			return Mat3{}, false
		}
		invPivot := 1 / p
		for j := col; j < 6; j++ {
			a[col][j] *= invPivot
		}

		for row := 0; row < 3; row++ {
			if row == col {
				continue
			}
			f := a[row][col]
			if f == 0 {
				continue
			}
			for j := col; j < 6; j++ {
				a[row][j] -= f * a[col][j]
			}
		}
	}

	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			inv[i][j] = a[i][3+j]
			if !isFinite(inv[i][j]) {
				return Mat3{}, false
			}
		}
	}
	return inv, true
}

// Affine is an invertible affine transform x_world = Linear*x_local + Translation.
type Affine struct {
	Linear      Mat3
	Translation Vec3
}

func IdentityAffine() Affine {
	return Affine{Linear: IdentityMat3()}
}

func NewAffine(linear Mat3, translation Vec3) Affine {
	return Affine{Linear: linear, Translation: translation}
}

func Translate(v Vec3) Affine {
	return Affine{Linear: IdentityMat3(), Translation: v}
}

func Scale(x, y, z float64) Affine {
	return Affine{
		Linear: Mat3{
			{x, 0, 0},
			{0, y, 0},
			{0, 0, z},
		},
	}
}

func (a Affine) TransformPoint(p Vec3) Vec3 {
	return a.Linear.MulVec(p).Add(a.Translation)
}

func (a Affine) TransformVector(v Vec3) Vec3 {
	return a.Linear.MulVec(v)
}

// Inverse returns the inverse affine transform.
func (a Affine) Inverse() (Affine, bool) {
	invLinear, ok := a.Linear.Inverse()
	if !ok {
		return Affine{}, false
	}
	invTranslation := invLinear.MulVec(a.Translation).Neg()
	if !invTranslation.IsFinite() {
		return Affine{}, false
	}
	return Affine{Linear: invLinear, Translation: invTranslation}, true
}

// Determinant returns the determinant of the affine transform's linear part.
func (a Affine) Determinant() float64 {
	return a.Linear.Determinant()
}

// DeterminantSign returns the sign of the linear part's determinant.
func (a Affine) DeterminantSign() int {
	return a.Linear.DeterminantSign()
}

func (a Affine) IsFinite() bool {
	if !a.Translation.IsFinite() {
		return false
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if !isFinite(a.Linear[i][j]) {
				return false
			}
		}
	}
	return true
}

// Compose applies a first, then b: result(p) = b(a(p)).
func (a Affine) Compose(b Affine) Affine {
	return Affine{
		Linear:      b.Linear.Mul(a.Linear),
		Translation: b.Linear.MulVec(a.Translation).Add(b.Translation),
	}
}
