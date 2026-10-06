package main

import (
	"fmt"
	"math/big"
	"sort"
)

// Knot 映射关键点：原时间 T -> 压缩后时间 M。
type Knot struct {
	T *big.Rat
	M *big.Rat
}

// PiecewiseMap 单调分段线性映射；所有压缩段共享同一映射。
// 压缩段内斜率 ∈ (0,1)，其余段斜率 = 1，全程严格递增，因此可逆。
type PiecewiseMap struct {
	Knots []Knot
}

// Interval 闭区间 [Lo, Hi]。
type Interval struct {
	Lo *big.Rat
	Hi *big.Rat
}

func offset(k Knot) *big.Rat { return rsub(k.M, k.T) }

func lerp(a, b Knot, t *big.Rat) *big.Rat {
	dt := rsub(t, a.T)
	span := rsub(b.T, a.T)
	rise := rsub(b.M, a.M)
	return radd(a.M, rmul(dt, rquo(rise, span)))
}

// Forward 正映射：原时间 -> 压缩后时间。
func (pm *PiecewiseMap) Forward(t *big.Rat) *big.Rat {
	ks := pm.Knots
	n := len(ks)
	if n == 0 {
		return new(big.Rat).Set(t)
	}
	if t.Cmp(ks[0].T) <= 0 {
		return radd(t, offset(ks[0]))
	}
	if t.Cmp(ks[n-1].T) >= 0 {
		return radd(t, offset(ks[n-1]))
	}
	i := sort.Search(n-1, func(i int) bool { return t.Cmp(ks[i+1].T) <= 0 })
	return lerp(ks[i], ks[i+1], t)
}

// Inverse 反映射：压缩后时间 -> 原时间。
func (pm *PiecewiseMap) Inverse(m *big.Rat) *big.Rat {
	ks := pm.Knots
	n := len(ks)
	if n == 0 {
		return new(big.Rat).Set(m)
	}
	if m.Cmp(ks[0].M) <= 0 {
		return rsub(m, offset(ks[0]))
	}
	if m.Cmp(ks[n-1].M) >= 0 {
		return rsub(m, offset(ks[n-1]))
	}
	i := sort.Search(n-1, func(i int) bool { return m.Cmp(ks[i+1].M) <= 0 })
	a := Knot{T: ks[i].M, M: ks[i].T}
	b := Knot{T: ks[i+1].M, M: ks[i+1].T}
	return lerp(a, b, m)
}

// InvertRange 反向定位：返回映射到 m 的全部原时间区间。
// 映射严格递增，退化为单点区间；保留区间形式以统一接口。
func (pm *PiecewiseMap) InvertRange(m *big.Rat) Interval {
	t := pm.Inverse(m)
	return Interval{Lo: t, Hi: new(big.Rat).Set(t)}
}

// InvertWindow 将压缩域窗口 [center-half, center+half] 反映射回原时间区间。
func (pm *PiecewiseMap) InvertWindow(center, half *big.Rat) Interval {
	return Interval{
		Lo: pm.Inverse(rsub(center, half)),
		Hi: pm.Inverse(radd(center, half)),
	}
}

// validate 关键点检查：T 与 M 均严格递增（各段斜率 > 0，整体可逆）。
func (pm *PiecewiseMap) validate() error {
	for i := 0; i+1 < len(pm.Knots); i++ {
		a, b := pm.Knots[i], pm.Knots[i+1]
		if a.T.Cmp(b.T) >= 0 {
			return fmt.Errorf("关键点 %d: 原时间非严格递增 (%s >= %s)", i, a.T, b.T)
		}
		if a.M.Cmp(b.M) >= 0 {
			return fmt.Errorf("关键点 %d: 映射时间非严格递增 (%s >= %s)", i, a.M, b.M)
		}
	}
	return nil
}
