package hsengine

import "math"

// Vector3 三维向量，单位：米 或 米/秒。
type Vector3 struct {
	X, Y, Z float64
}

// Add 向量相加。
func (a Vector3) Add(b Vector3) Vector3 {
	return Vector3{a.X + b.X, a.Y + b.Y, a.Z + b.Z}
}

// Sub 向量相减。
func (a Vector3) Sub(b Vector3) Vector3 {
	return Vector3{a.X - b.X, a.Y - b.Y, a.Z - b.Z}
}

// Scale 标量乘法。
func (a Vector3) Scale(k float64) Vector3 {
	return Vector3{a.X * k, a.Y * k, a.Z * k}
}

// Dot 点积。
func (a Vector3) Dot(b Vector3) float64 {
	return a.X*b.X + a.Y*b.Y + a.Z*b.Z
}

// Norm 向量模长。
func (a Vector3) Norm() float64 {
	return math.Sqrt(a.Dot(a))
}

// Distance 两点间欧氏距离。
func (a Vector3) Distance(b Vector3) float64 {
	return a.Sub(b).Norm()
}
