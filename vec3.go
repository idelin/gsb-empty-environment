package taskpkg

import "math"

// Vec3 is a three-component vector.
type Vec3 struct {
	X float64
	Y float64
	Z float64
}

// Vector3 is an alias for Vec3.
type Vector3 = Vec3

// Add returns v + other.
func (v Vec3) Add(other Vec3) Vec3 {
	return Vec3{v.X + other.X, v.Y + other.Y, v.Z + other.Z}
}

// Sub returns v - other.
func (v Vec3) Sub(other Vec3) Vec3 {
	return Vec3{v.X - other.X, v.Y - other.Y, v.Z - other.Z}
}

// Scale returns v multiplied by s.
func (v Vec3) Scale(s float64) Vec3 {
	return Vec3{v.X * s, v.Y * s, v.Z * s}
}

// Dot returns the dot product of v and other.
func (v Vec3) Dot(other Vec3) float64 {
	return v.X*other.X + v.Y*other.Y + v.Z*other.Z
}

// Cross returns the cross product of v and other.
func (v Vec3) Cross(other Vec3) Vec3 {
	return Vec3{
		v.Y*other.Z - v.Z*other.Y,
		v.Z*other.X - v.X*other.Z,
		v.X*other.Y - v.Y*other.X,
	}
}

// Length returns the Euclidean length of v.
func (v Vec3) Length() float64 {
	return math.Sqrt(v.Dot(v))
}

// Normalize returns v scaled to unit length. A zero vector is returned unchanged.
func (v Vec3) Normalize() Vec3 {
	length := v.Length()
	if length == 0 || math.IsNaN(length) || math.IsInf(length, 0) {
		return v
	}
	return v.Scale(1 / length)
}

func (v Vec3) isFinite() bool {
	return !math.IsNaN(v.X) && !math.IsInf(v.X, 0) &&
		!math.IsNaN(v.Y) && !math.IsInf(v.Y, 0) &&
		!math.IsNaN(v.Z) && !math.IsInf(v.Z, 0)
}
