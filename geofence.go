// Package geofence 实现支持时间有效期的动态多边形地理围栏监控组件。
//
// 核心能力：
//   - 多边形围栏在指定时间窗口内生效，窗口外不产生任何事件；
//   - 围栏形状可随时间动态缩放（ScaleFunc）；
//   - 基于符号距离 + 迟滞带（hysteresis band）的状态机，
//     识别 Enter / Dwell / Exit 事件，消除边界抖动造成的毛刺。
package geofence

import (
	"math"
	"time"
)

// Point 表示平面坐标点（经纬度投影后的平面坐标或任意二维坐标）。
type Point struct {
	X float64
	Y float64
}

// Polygon 表示简单多边形（顶点按顺时针或逆时针排列，首尾无需重复）。
type Polygon []Point

// Centroid 返回多边形顶点均值中心，用作缩放基准点。
func (p Polygon) Centroid() Point {
	var c Point
	for _, v := range p {
		c.X += v.X
		c.Y += v.Y
	}
	n := float64(len(p))
	return Point{X: c.X / n, Y: c.Y / n}
}

// Scaled 返回以 center 为基准、按 factor 缩放后的新多边形。
func (p Polygon) Scaled(center Point, factor float64) Polygon {
	out := make(Polygon, len(p))
	for i, v := range p {
		out[i] = Point{
			X: center.X + (v.X-center.X)*factor,
			Y: center.Y + (v.Y-center.Y)*factor,
		}
	}
	return out
}

// Contains 使用射线法判断点是否在多边形内部（边界上视为内部）。
func (p Polygon) Contains(pt Point) bool {
	n := len(p)
	if n < 3 {
		return false
	}
	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		yi, yj := p[i].Y, p[j].Y
		xi, xj := p[i].X, p[j].X
		if (yi > pt.Y) != (yj > pt.Y) {
			xInt := (xj-xi)*(pt.Y-yi)/(yj-yi) + xi
			if pt.X < xInt {
				inside = !inside
			}
		}
		j = i
	}
	return inside
}

// distanceToBoundary 返回点到多边形各边的最小欧氏距离（无符号）。
func (p Polygon) distanceToBoundary(pt Point) float64 {
	min := math.Inf(1)
	n := len(p)
	for i := 0; i < n; i++ {
		a := p[i]
		b := p[(i+1)%n]
		if d := pointToSegmentDistance(pt, a, b); d < min {
			min = d
		}
	}
	return min
}

// SignedDistance 返回点到多边形边界的符号距离：
// 内部为正，外部为负，边界上为 0。
func (p Polygon) SignedDistance(pt Point) float64 {
	d := p.distanceToBoundary(pt)
	if p.Contains(pt) {
		return d
	}
	return -d
}

// pointToSegmentDistance 计算点到线段的最短距离。
func pointToSegmentDistance(pt, a, b Point) float64 {
	dx := b.X - a.X
	dy := b.Y - a.Y
	lengthSq := dx*dx + dy*dy
	if lengthSq == 0 {
		return math.Hypot(pt.X-a.X, pt.Y-a.Y)
	}
	t := ((pt.X-a.X)*dx + (pt.Y-a.Y)*dy) / lengthSq
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	proj := Point{X: a.X + t*dx, Y: a.Y + t*dy}
	return math.Hypot(pt.X-proj.X, pt.Y-proj.Y)
}

// Fence 表示一个带时间有效期、可动态缩放的多边形围栏。
type Fence struct {
	// Polygon 为基准多边形（缩放因子为 1 时的形状）。
	Polygon Polygon
	// ActiveFrom / ActiveUntil 定义围栏生效的绝对时间窗口 [from, until)。
	ActiveFrom  time.Time
	ActiveUntil time.Time
	// Hysteresis 为迟滞带半宽（与坐标同单位）。
	// 进入需符号距离 > +Hysteresis，离开需符号距离 < -Hysteresis，
	// 迟滞带内的移动不改变状态，从而消除边界抖动毛刺。
	Hysteresis float64
	// DwellAfter 为停留判定阈值：进入后持续停留超过该时长触发 Dwell。
	// 取 0 表示禁用 Dwell 事件。
	DwellAfter time.Duration
	// ScaleFunc 可选：返回 t 时刻的缩放因子（相对基准多边形形心缩放）。
	// 为 nil 时围栏形状恒定。
	ScaleFunc func(t time.Time) float64
}

// ActiveAt 判断围栏在 t 时刻是否生效。
func (f *Fence) ActiveAt(t time.Time) bool {
	return !t.Before(f.ActiveFrom) && t.Before(f.ActiveUntil)
}

// shapeAt 返回 t 时刻的实际多边形（应用动态缩放）。
func (f *Fence) shapeAt(t time.Time) Polygon {
	if f.ScaleFunc == nil {
		return f.Polygon
	}
	factor := f.ScaleFunc(t)
	if factor == 1 {
		return f.Polygon
	}
	return f.Polygon.Scaled(f.Polygon.Centroid(), factor)
}

// SignedDistanceAt 返回 t 时刻点到围栏（动态形状）边界的符号距离。
func (f *Fence) SignedDistanceAt(pt Point, t time.Time) float64 {
	return f.shapeAt(t).SignedDistance(pt)
}
