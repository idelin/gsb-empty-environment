package sdf

import "math"

// Vec2 is a 2D vector / point.
type Vec2 struct {
	X, Y float64
}

// V2 constructs a Vec2.
func V2(x, y float64) Vec2 { return Vec2{X: x, Y: y} }

func (a Vec2) Add(b Vec2) Vec2      { return Vec2{a.X + b.X, a.Y + b.Y} }
func (a Vec2) Sub(b Vec2) Vec2      { return Vec2{a.X - b.X, a.Y - b.Y} }
func (a Vec2) Scale(s float64) Vec2 { return Vec2{a.X * s, a.Y * s} }
func (a Vec2) Dot(b Vec2) float64   { return a.X*b.X + a.Y*b.Y }
func (a Vec2) LenSq() float64       { return a.X*a.X + a.Y*a.Y }
func (a Vec2) Len() float64         { return math.Sqrt(a.LenSq()) }

func (a Vec2) Normalize() Vec2 {
	l := a.Len()
	if l == 0 {
		return Vec2{}
	}
	return a.Scale(1 / l)
}

func (a Vec2) Abs() Vec2 { return Vec2{math.Abs(a.X), math.Abs(a.Y)} }

// Lerp linearly interpolates between a and b.
func (a Vec2) Lerp(b Vec2, t float64) Vec2 {
	return a.Add(b.Sub(a).Scale(t))
}
