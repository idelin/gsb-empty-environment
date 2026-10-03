package grid

import "math"

// Vec3 单位球面/三维向量。
type Vec3 struct{ X, Y, Z float64 }

func (a Vec3) Add(b Vec3) Vec3      { return Vec3{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }
func (a Vec3) Sub(b Vec3) Vec3      { return Vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func (a Vec3) Scale(s float64) Vec3 { return Vec3{a.X * s, a.Y * s, a.Z * s} }
func (a Vec3) Dot(b Vec3) float64   { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }
func (a Vec3) Cross(b Vec3) Vec3 {
	return Vec3{a.Y*b.Z - a.Z*b.Y, a.Z*b.X - a.X*b.Z, a.X*b.Y - a.Y*b.X}
}
func (a Vec3) Norm() float64 { return math.Sqrt(a.Dot(a)) }
func (a Vec3) Unit() Vec3    { return a.Scale(1 / a.Norm()) }

// phi 黄金比例；二十面体 12 顶点。
var phi = (1 + math.Sqrt(5)) / 2

var icoVertices = []Vec3{
	{0, 1, phi}, {0, 1, -phi}, {0, -1, phi}, {0, -1, -phi},
	{1, phi, 0}, {1, -phi, 0}, {-1, phi, 0}, {-1, -phi, 0},
	{phi, 0, 1}, {phi, 0, -1}, {-phi, 0, 1}, {-phi, 0, -1},
}

// icoFaces 二十面体 20 个三角面（顶点索引，逆时针）。
var icoFaces = [][3]int{
	{0, 2, 8}, {0, 8, 4}, {0, 4, 6}, {0, 6, 10}, {0, 10, 2}, // 顶帽
	{3, 1, 11}, {3, 11, 7}, {3, 7, 5}, {3, 5, 9}, {3, 9, 1}, // 底帽
	{2, 5, 8}, {8, 5, 9}, {8, 4, 9}, {4, 9, 1}, {4, 1, 6}, // 中间带
	{6, 1, 11}, {6, 11, 10}, {10, 11, 7}, {10, 7, 2}, {2, 7, 5},
}

// NumBaseCells 基础单元数：每个二十面体面一个六边形基单元。
const NumBaseCells = 20

// facePlane 每个面的切平面局部坐标系：center 为单位法向，u/v 为正交基。
type facePlane struct{ center, u, v Vec3 }

var facePlanes [NumBaseCells]facePlane

func init() {
	for i, f := range icoFaces {
		c := icoVertices[f[0]].Add(icoVertices[f[1]]).Add(icoVertices[f[2]]).Unit()
		z := Vec3{0, 0, 1}
		u := c.Cross(z).Unit()
		v := c.Cross(u) // c⊥u 且均为单位向量 => v 为单位向量
		facePlanes[i] = facePlane{c, u, v}
	}
}
